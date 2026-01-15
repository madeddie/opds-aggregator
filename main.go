package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	OutputTypeUsage      = "usage"
	OutputTypeRequests   = "requests"
	OutputTypeMaxRequests = "max-requests"
)

type ResourceMetrics struct {
	CPU    int64 // in millicores
	Memory int64 // in bytes
}

type DeploymentMetrics struct {
	Name            string
	Namespace       string
	CurrentReplicas int32
	DesiredReplicas int32
	MaxReplicas     int32
	Usage           ResourceMetrics
	Requests        ResourceMetrics
	MaxRequests     ResourceMetrics
}

// Porter API data structures
type PorterApplication struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PorterApplicationDetail struct {
	ID                 string                    `json:"id"`
	Name               string                    `json:"name"`
	DeploymentTargetID string                    `json:"deployment_target_id"`
	Services           map[string]PorterService  `json:"services"`
}

type PorterService struct {
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Config    PorterServiceConfig    `json:"config"`
	Instances PorterServiceInstances `json:"instances"`
}

type PorterServiceConfig struct {
	Resources PorterResources `json:"resources"`
}

type PorterResources struct {
	CPU    PorterResourceValue `json:"cpu"`
	Memory PorterResourceValue `json:"memory"`
}

type PorterResourceValue struct {
	Value string `json:"value"`
}

type PorterServiceInstances struct {
	Min int32 `json:"min"`
	Max int32 `json:"max"`
}

type PorterClient struct {
	BaseURL    string
	Token      string
	ProjectID  string
	HTTPClient *http.Client
}

func main() {
	var outputType string
	var namespace string
	var deploymentName string
	var kubeconfig string
	var usePorter bool
	var porterToken string
	var porterProjectID string
	var porterBaseURL string

	// Default kubeconfig path: KUBECONFIG env var, then ~/.kube/config
	defaultKubeconfig := os.Getenv("KUBECONFIG")
	if defaultKubeconfig == "" {
		if home := os.Getenv("HOME"); home != "" {
			defaultKubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	flag.StringVar(&outputType, "output", OutputTypeRequests, "Output type: usage, requests, or max-requests")
	flag.StringVar(&namespace, "namespace", "", "Namespace (defaults to current context or 'default')")
	flag.StringVar(&deploymentName, "deployment", "", "Deployment name (defaults to all deployments)")
	flag.StringVar(&kubeconfig, "kubeconfig", defaultKubeconfig, "Path to kubeconfig file")
	flag.BoolVar(&usePorter, "porter", false, "Use Porter API instead of direct Kubernetes access")
	flag.StringVar(&porterToken, "porter-token", os.Getenv("PORTER_TOKEN"), "Porter API token (or set PORTER_TOKEN env var)")
	flag.StringVar(&porterProjectID, "porter-project-id", os.Getenv("PORTER_PROJECT_ID"), "Porter project ID (or set PORTER_PROJECT_ID env var)")
	flag.StringVar(&porterBaseURL, "porter-url", getEnvDefault("PORTER_BASE_URL", "https://dashboard.porter.run"), "Porter API base URL")
	flag.Parse()

	// Validate output type
	if outputType != OutputTypeUsage && outputType != OutputTypeRequests && outputType != OutputTypeMaxRequests {
		fmt.Fprintf(os.Stderr, "Error: Invalid output type '%s'. Must be 'usage', 'requests', or 'max-requests'\n", outputType)
		os.Exit(1)
	}

	ctx := context.Background()
	var deployments []DeploymentMetrics

	if usePorter {
		// Use Porter API
		if porterToken == "" {
			fmt.Fprintf(os.Stderr, "Error: Porter token required. Set PORTER_TOKEN env var or use -porter-token flag\n")
			os.Exit(1)
		}
		if porterProjectID == "" {
			fmt.Fprintf(os.Stderr, "Error: Porter project ID required. Set PORTER_PROJECT_ID env var or use -porter-project-id flag\n")
			os.Exit(1)
		}

		client := &PorterClient{
			BaseURL:    porterBaseURL,
			Token:      porterToken,
			ProjectID:  porterProjectID,
			HTTPClient: &http.Client{},
		}

		var err error
		deployments, err = getPorterApplicationMetrics(ctx, client, deploymentName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting Porter application metrics: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Use direct Kubernetes API
		// Build config from kubeconfig
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error building kubeconfig: %v\n", err)
			os.Exit(1)
		}

		// Create kubernetes clientset
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Kubernetes client: %v\n", err)
			os.Exit(1)
		}

		// Create metrics clientset (for usage metrics)
		metricsClientset, err := versioned.NewForConfig(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating metrics client: %v\n", err)
			os.Exit(1)
		}

		// Get namespace from kubeconfig if not specified
		if namespace == "" {
			namespace, err = getNamespaceFromKubeconfig(kubeconfig)
			if err != nil {
				namespace = "default"
			}
		}

		// Get deployments
		if deploymentName != "" {
			// Get specific deployment
			deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting deployment %s: %v\n", deploymentName, err)
				os.Exit(1)
			}
			metrics, err := getDeploymentMetrics(ctx, clientset, metricsClientset, deployment.Namespace, deployment.Name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting metrics for deployment %s: %v\n", deploymentName, err)
				os.Exit(1)
			}
			deployments = append(deployments, metrics)
		} else {
			// Get all deployments in namespace
			deploymentList, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error listing deployments: %v\n", err)
				os.Exit(1)
			}
			for _, deployment := range deploymentList.Items {
				metrics, err := getDeploymentMetrics(ctx, clientset, metricsClientset, deployment.Namespace, deployment.Name)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: Error getting metrics for deployment %s: %v\n", deployment.Name, err)
					continue
				}
				deployments = append(deployments, metrics)
			}
		}
	}

	// Output results
	printResults(deployments, outputType)
}

func getNamespaceFromKubeconfig(kubeconfigPath string) (string, error) {
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return "", err
	}

	if config.CurrentContext == "" {
		return "", fmt.Errorf("no current context")
	}

	context, ok := config.Contexts[config.CurrentContext]
	if !ok {
		return "", fmt.Errorf("current context not found")
	}

	if context.Namespace != "" {
		return context.Namespace, nil
	}

	return "default", nil
}

func getDeploymentMetrics(ctx context.Context, clientset *kubernetes.Clientset, metricsClientset *versioned.Clientset, namespace, name string) (DeploymentMetrics, error) {
	// Get the deployment first to get replicas information
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return DeploymentMetrics{}, fmt.Errorf("error getting deployment: %w", err)
	}

	dm := DeploymentMetrics{
		Name:            name,
		Namespace:       namespace,
		CurrentReplicas: deployment.Status.Replicas,
		DesiredReplicas: *deployment.Spec.Replicas,
		MaxReplicas:     *deployment.Spec.Replicas, // Default to desired, will be overridden by HPA if exists
	}

	// Get label selector from deployment
	var labelSelector string
	if deployment.Spec.Selector != nil {
		labelSelector = metav1.FormatLabelSelector(deployment.Spec.Selector)
	} else {
		labelSelector = fmt.Sprintf("app=%s", name)
	}

	// Get pods for this deployment
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return dm, fmt.Errorf("error listing pods: %w", err)
	}

	// Calculate requests from pod specs
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			if cpu := container.Resources.Requests.Cpu(); cpu != nil {
				dm.Requests.CPU += cpu.MilliValue()
			}
			if memory := container.Resources.Requests.Memory(); memory != nil {
				dm.Requests.Memory += memory.Value()
			}
		}
	}

	// Get current usage from metrics API
	podMetricsList, err := metricsClientset.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err == nil {
		for _, podMetrics := range podMetricsList.Items {
			for _, container := range podMetrics.Containers {
				if cpu := container.Usage.Cpu(); cpu != nil {
					dm.Usage.CPU += cpu.MilliValue()
				}
				if memory := container.Usage.Memory(); memory != nil {
					dm.Usage.Memory += memory.Value()
				}
			}
		}
	}

	// Get HPA information
	hpaList, err := clientset.AutoscalingV1().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, hpa := range hpaList.Items {
			if hpa.Spec.ScaleTargetRef.Name == name && hpa.Spec.ScaleTargetRef.Kind == "Deployment" {
				dm.MaxReplicas = hpa.Spec.MaxReplicas
				// Calculate max requests based on HPA max replicas
				if dm.MaxReplicas > dm.DesiredReplicas && len(pods.Items) > 0 {
					// Get requests per pod (average from current pods)
					requestsPerPod := ResourceMetrics{
						CPU:    dm.Requests.CPU / int64(len(pods.Items)),
						Memory: dm.Requests.Memory / int64(len(pods.Items)),
					}
					dm.MaxRequests.CPU = requestsPerPod.CPU * int64(dm.MaxReplicas)
					dm.MaxRequests.Memory = requestsPerPod.Memory * int64(dm.MaxReplicas)
				}
				break
			}
		}
	}

	return dm, nil
}

func printResults(deployments []DeploymentMetrics, outputType string) {
	if len(deployments) == 0 {
		fmt.Println("No deployments found")
		return
	}

	// Create a tabwriter for aligned output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Print header
	fmt.Fprintln(w, "DEPLOYMENT\tNAMESPACE\tREPLICAS\tCPU\tMEMORY")

	var totalCPU, totalMemory int64

	for _, dm := range deployments {
		var cpu, memory, replicas string

		switch outputType {
		case OutputTypeUsage, OutputTypeRequests:
			// Show current/max replicas
			replicas = fmt.Sprintf("%d/%d", dm.CurrentReplicas, dm.MaxReplicas)
		case OutputTypeMaxRequests:
			// Show only max replicas
			replicas = fmt.Sprintf("%d", dm.MaxReplicas)
		}

		switch outputType {
		case OutputTypeUsage:
			cpu = formatCPU(dm.Usage.CPU)
			memory = formatMemory(dm.Usage.Memory)
			totalCPU += dm.Usage.CPU
			totalMemory += dm.Usage.Memory
		case OutputTypeRequests:
			cpu = formatCPU(dm.Requests.CPU)
			memory = formatMemory(dm.Requests.Memory)
			totalCPU += dm.Requests.CPU
			totalMemory += dm.Requests.Memory
		case OutputTypeMaxRequests:
			if dm.MaxReplicas > dm.DesiredReplicas {
				// Has HPA, use max requests
				cpu = formatCPU(dm.MaxRequests.CPU)
				memory = formatMemory(dm.MaxRequests.Memory)
				totalCPU += dm.MaxRequests.CPU
				totalMemory += dm.MaxRequests.Memory
			} else {
				// No HPA, use current requests as max
				cpu = formatCPU(dm.Requests.CPU)
				memory = formatMemory(dm.Requests.Memory)
				totalCPU += dm.Requests.CPU
				totalMemory += dm.Requests.Memory
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", dm.Name, dm.Namespace, replicas, cpu, memory)
	}

	// Print totals row
	fmt.Fprintf(w, "TOTAL\t\t\t%s\t%s\n", formatCPU(totalCPU), formatMemory(totalMemory))

	// Flush the writer to output everything
	w.Flush()
}

func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getPorterApplicationMetrics(ctx context.Context, client *PorterClient, appName string) ([]DeploymentMetrics, error) {
	// List all applications
	apps, err := client.ListApplications(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}

	var deployments []DeploymentMetrics

	for _, app := range apps {
		// Skip if filtering by name
		if appName != "" && app.Name != appName {
			continue
		}

		// Get application details
		detail, err := client.GetApplication(ctx, app.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Error getting application %s: %v\n", app.Name, err)
			continue
		}

		// Process each service in the application
		for serviceName, service := range detail.Services {
			dm := DeploymentMetrics{
				Name:            fmt.Sprintf("%s-%s", app.Name, serviceName),
				Namespace:       detail.DeploymentTargetID,
				CurrentReplicas: service.Instances.Min,
				DesiredReplicas: service.Instances.Min,
				MaxReplicas:     service.Instances.Max,
			}

			// Parse CPU and memory resources
			cpuMillis, err := parseResourceValue(service.Config.Resources.CPU.Value, true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Error parsing CPU for %s: %v\n", dm.Name, err)
			}
			memoryBytes, err := parseResourceValue(service.Config.Resources.Memory.Value, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Error parsing memory for %s: %v\n", dm.Name, err)
			}

			// Calculate current requests (min replicas)
			dm.Requests.CPU = cpuMillis * int64(dm.DesiredReplicas)
			dm.Requests.Memory = memoryBytes * int64(dm.DesiredReplicas)

			// Calculate max requests (max replicas)
			dm.MaxRequests.CPU = cpuMillis * int64(dm.MaxReplicas)
			dm.MaxRequests.Memory = memoryBytes * int64(dm.MaxReplicas)

			deployments = append(deployments, dm)
		}
	}

	return deployments, nil
}

func (c *PorterClient) ListApplications(ctx context.Context) ([]PorterApplication, error) {
	url := fmt.Sprintf("%s/api/v2/alpha/projects/%s/applications", c.BaseURL, c.ProjectID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var apps []PorterApplication
	if err := json.NewDecoder(resp.Body).Decode(&apps); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return apps, nil
}

func (c *PorterClient) GetApplication(ctx context.Context, appID string) (*PorterApplicationDetail, error) {
	url := fmt.Sprintf("%s/api/v2/alpha/projects/%s/applications/%s", c.BaseURL, c.ProjectID, appID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var app PorterApplicationDetail
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &app, nil
}

func parseResourceValue(value string, isCPU bool) (int64, error) {
	if value == "" {
		return 0, nil
	}

	value = strings.TrimSpace(value)

	if isCPU {
		// Parse CPU: can be "1000m", "1.5", "2 cores", etc.
		if strings.HasSuffix(value, "m") {
			// Millicores
			var millis int64
			_, err := fmt.Sscanf(value, "%dm", &millis)
			return millis, err
		} else if strings.Contains(value, "core") {
			// Cores format like "1.5 cores" or "2 cores"
			var cores float64
			_, err := fmt.Sscanf(value, "%f", &cores)
			return int64(cores * 1000), err
		} else {
			// Assume it's a decimal number of cores
			var cores float64
			_, err := fmt.Sscanf(value, "%f", &cores)
			return int64(cores * 1000), err
		}
	} else {
		// Parse Memory: can be "256Mi", "1Gi", "512M", "1G", etc.
		value = strings.ToUpper(value)

		if strings.HasSuffix(value, "GI") {
			var gib float64
			_, err := fmt.Sscanf(value, "%fGI", &gib)
			return int64(gib * 1024 * 1024 * 1024), err
		} else if strings.HasSuffix(value, "G") {
			var gb float64
			_, err := fmt.Sscanf(value, "%fG", &gb)
			return int64(gb * 1000 * 1000 * 1000), err
		} else if strings.HasSuffix(value, "MI") {
			var mib float64
			_, err := fmt.Sscanf(value, "%fMI", &mib)
			return int64(mib * 1024 * 1024), err
		} else if strings.HasSuffix(value, "M") {
			var mb float64
			_, err := fmt.Sscanf(value, "%fM", &mb)
			return int64(mb * 1000 * 1000), err
		} else if strings.HasSuffix(value, "KI") {
			var kib float64
			_, err := fmt.Sscanf(value, "%fKI", &kib)
			return int64(kib * 1024), err
		} else if strings.HasSuffix(value, "K") {
			var kb float64
			_, err := fmt.Sscanf(value, "%fK", &kb)
			return int64(kb * 1000), err
		} else {
			// Assume bytes
			var bytes int64
			_, err := fmt.Sscanf(value, "%d", &bytes)
			return bytes, err
		}
	}
}

func formatCPU(milliCores int64) string {
	if milliCores >= 1000 {
		return fmt.Sprintf("%.2f cores", float64(milliCores)/1000.0)
	}
	return fmt.Sprintf("%dm", milliCores)
}

func formatMemory(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	if bytes >= GB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	} else if bytes >= MB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	} else if bytes >= KB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	}
	return fmt.Sprintf("%d B", bytes)
}
