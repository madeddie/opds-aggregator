# Kubernetes Resource CLI Tool

A CLI tool written in Go that interfaces with the Kubernetes API to retrieve resource requests and resource usage of pods in deployments.

## Features

- Query CPU and memory usage/requests for Kubernetes deployments
- Support for HPA (Horizontal Pod Autoscaler) max replica calculations
- Multiple output modes
- Configurable namespace and deployment filtering
- Uses kubeconfig for Kubernetes API access

## Prerequisites

- Go 1.24+ (for building from source)
- Access to a Kubernetes cluster
- Valid kubeconfig file (typically at `~/.kube/config`)
- Metrics Server running in your cluster (for usage metrics)

## Installation

### Build from source

```bash
git clone https://github.com/madeddie/k8s-resource-cli.git
cd k8s-resource-cli
go build -o k8s-resource-cli
```

### Install

```bash
go install
```

Or move the binary to your PATH:

```bash
sudo mv k8s-resource-cli /usr/local/bin/
```

## Usage

### Basic Usage

```bash
# Show resource requests for all deployments in current namespace
./k8s-resource-cli

# Show current usage for a specific deployment
./k8s-resource-cli -output usage -deployment my-app

# Show max requests based on HPA for all deployments in a namespace
./k8s-resource-cli -output max-requests -namespace production
```

### Command Line Arguments

| Argument | Description | Default |
|----------|-------------|---------|
| `-output` | Output type: `usage`, `requests`, or `max-requests` | `requests` |
| `-namespace` | Kubernetes namespace to query | Current context namespace or `default` |
| `-deployment` | Specific deployment name | All deployments in namespace |
| `-kubeconfig` | Path to kubeconfig file | `~/.kube/config` |

### Output Types

#### `usage`
Shows current CPU and memory usage of running pods in the deployment. Requires Metrics Server to be running in your cluster.

```bash
./k8s-resource-cli -output usage
```

#### `requests`
Shows total CPU and memory requests configured for all pods in the deployment.

```bash
./k8s-resource-cli -output requests
```

#### `max-requests`
Shows the total CPU and memory requests if the deployment were scaled to the maximum replicas specified in its HPA (Horizontal Pod Autoscaler). Only applicable for deployments with an HPA configured.

```bash
./k8s-resource-cli -output max-requests
```

## Examples

### Example 1: View current usage for all deployments

```bash
./k8s-resource-cli -output usage
```

Output:
```
DEPLOYMENT                     NAMESPACE       CPU             MEMORY
================================================================================
web-frontend                   production      1.50 cores      2.50 GB
api-backend                    production      3.20 cores      4.00 GB
worker                         production      800m            1.20 GB
================================================================================
TOTAL                                          5.50 cores      7.70 GB
```

### Example 2: View resource requests for a specific deployment

```bash
./k8s-resource-cli -output requests -deployment web-frontend -namespace production
```

Output:
```
DEPLOYMENT                     NAMESPACE       CPU             MEMORY
================================================================================
web-frontend                   production      2.00 cores      4.00 GB
================================================================================
TOTAL                                          2.00 cores      4.00 GB
```

### Example 3: View max requests based on HPA

```bash
./k8s-resource-cli -output max-requests -namespace production
```

Output:
```
DEPLOYMENT                     NAMESPACE       CPU             MEMORY
================================================================================
web-frontend                   production      10.00 cores     20.00 GB
api-backend                    production      N/A (no HPA)    N/A (no HPA)
================================================================================
TOTAL                                          10.00 cores     20.00 GB
```

## How It Works

1. **Kubernetes Client**: The tool uses the official Kubernetes Go client library and connects to your cluster using the kubeconfig file.

2. **Resource Requests**: Reads the pod specifications for each deployment and sums up the CPU and memory requests across all containers and pods.

3. **Current Usage**: Queries the Metrics Server API to get real-time CPU and memory usage for running pods.

4. **HPA Integration**: Looks up HorizontalPodAutoscaler resources associated with each deployment and calculates the total resources needed if scaled to max replicas.

## Label Selection

The tool attempts to find pods belonging to a deployment using the following strategy:

1. First tries using `app=<deployment-name>` label selector
2. If no pods are found, uses the deployment's actual selector labels

This ensures compatibility with different labeling conventions.

## Troubleshooting

### "No deployments found"
- Verify you're querying the correct namespace
- Check that deployments exist: `kubectl get deployments -n <namespace>`

### "Error creating metrics client"
- Ensure Metrics Server is installed in your cluster
- Verify with: `kubectl top nodes`

### "Error building kubeconfig"
- Verify your kubeconfig file path is correct
- Test with: `kubectl cluster-info`

### Usage metrics show 0 or N/A
- Metrics Server needs a few minutes to collect data after pods start
- Verify Metrics Server is running: `kubectl get pods -n kube-system | grep metrics`

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
