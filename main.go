package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

func main() {
	var outputType string
	var namespace string
	var deploymentName string
	var kubeconfig string

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
	flag.Parse()

	// Validate output type
	if outputType != OutputTypeUsage && outputType != OutputTypeRequests && outputType != OutputTypeMaxRequests {
		fmt.Fprintf(os.Stderr, "Error: Invalid output type '%s'. Must be 'usage', 'requests', or 'max-requests'\n", outputType)
		os.Exit(1)
	}

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

	ctx := context.Background()

	// Get deployments
	var deployments []DeploymentMetrics
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

	fmt.Printf("%-30s %-15s %-12s %-15s %-15s\n", "DEPLOYMENT", "NAMESPACE", "REPLICAS", "CPU", "MEMORY")
	fmt.Println("==========================================================================================")

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

		fmt.Printf("%-30s %-15s %-12s %-15s %-15s\n", dm.Name, dm.Namespace, replicas, cpu, memory)
	}

	// Print totals row
	fmt.Println("==========================================================================================")
	fmt.Printf("%-30s %-15s %-12s %-15s %-15s\n", "TOTAL", "", "", formatCPU(totalCPU), formatMemory(totalMemory))
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
