# Kubernetes Resource CLI Tool

A CLI tool written in Go that interfaces with the Kubernetes API to retrieve resource requests and resource usage of pods in deployments.

## Features

- Query CPU and memory usage/requests for Kubernetes deployments
- Support for HPA (Horizontal Pod Autoscaler) max replica calculations
- Multiple output modes
- Configurable namespace and deployment filtering
- Two access modes:
  - Direct Kubernetes API access via kubeconfig
  - Porter API access for managed applications

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

#### Common Arguments

| Argument | Description | Default |
|----------|-------------|---------|
| `-output` | Output type: `usage`, `requests`, or `max-requests` | `requests` |
| `-deployment` | Specific deployment/application name | All deployments/applications |

#### Kubernetes Direct Access

| Argument | Description | Default |
|----------|-------------|---------|
| `-namespace` | Kubernetes namespace to query | Current context namespace or `default` |
| `-kubeconfig` | Path to kubeconfig file | `$KUBECONFIG` or `~/.kube/config` |

#### Porter API Access

| Argument | Description | Default |
|----------|-------------|---------|
| `-porter` | Enable Porter API mode | `false` |
| `-porter-token` | Porter API bearer token | `$PORTER_TOKEN` env var |
| `-porter-project-id` | Porter project ID | `$PORTER_PROJECT_ID` env var |
| `-porter-url` | Porter API base URL | `https://dashboard.porter.run` |

### Configuration

**Kubeconfig File Resolution**

The tool uses the following precedence order to find the kubeconfig file:

1. Command-line flag: `-kubeconfig /path/to/config` (highest priority)
2. Environment variable: `KUBECONFIG=/path/to/config`
3. Default location: `~/.kube/config`

Example:
```bash
# Use custom kubeconfig via environment variable
export KUBECONFIG=/path/to/my/kubeconfig
./k8s-resource-cli -output requests

# Override environment variable with command-line flag
./k8s-resource-cli -kubeconfig /different/path/config
```

**Porter API Configuration**

To use Porter API mode, you need to provide authentication credentials:

1. Via environment variables (recommended):
```bash
export PORTER_TOKEN="your-porter-api-token"
export PORTER_PROJECT_ID="your-project-id"
./k8s-resource-cli -porter -output requests
```

2. Via command-line flags:
```bash
./k8s-resource-cli -porter -porter-token "your-token" -porter-project-id "12345" -output requests
```

3. For self-hosted Porter instances:
```bash
export PORTER_BASE_URL="https://your-porter-instance.com"
./k8s-resource-cli -porter -output max-requests
```

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
Shows the total CPU and memory requests if the deployment were scaled to the maximum replicas specified in its HPA (Horizontal Pod Autoscaler). For deployments without an HPA, it shows the current resource requests (same as the `requests` output type).

```bash
./k8s-resource-cli -output max-requests
```

### Replicas Column

The `REPLICAS` column shows different information based on the output type:

- **`usage` and `requests`**: Shows `current/max` format (e.g., `2/5`)
  - `current`: Number of pods currently running (from deployment status)
  - `max`: Maximum replicas from HPA, or desired replicas if no HPA exists

- **`max-requests`**: Shows only the maximum replicas (e.g., `5`)
  - This is the HPA max replicas if configured, otherwise the deployment's desired replicas

## Examples

### Example 1: View current usage for all deployments

```bash
./k8s-resource-cli -output usage
```

Output:
```
DEPLOYMENT    NAMESPACE    REPLICAS   CPU           MEMORY
web-frontend  production   2/5        1.50 cores    2.50 GB
api-backend   production   3/10       3.20 cores    4.00 GB
worker        production   1/1        800m          1.20 GB
TOTAL                                 5.50 cores    7.70 GB
```

### Example 2: View resource requests for a specific deployment

```bash
./k8s-resource-cli -output requests -deployment web-frontend -namespace production
```

Output:
```
DEPLOYMENT    NAMESPACE    REPLICAS   CPU          MEMORY
web-frontend  production   2/5        2.00 cores   4.00 GB
TOTAL                                 2.00 cores   4.00 GB
```

### Example 3: View max requests based on HPA

```bash
./k8s-resource-cli -output max-requests -namespace production
```

Output:
```
DEPLOYMENT    NAMESPACE    REPLICAS   CPU           MEMORY
web-frontend  production   10         10.00 cores   20.00 GB
api-backend   production   3          3.20 cores    4.00 GB
TOTAL                                 13.20 cores   24.00 GB
```

Note: `web-frontend` has an HPA with max replicas of 10, showing scaled-up resources. `api-backend` has no HPA, so it shows current resource requests with max replicas being the desired replicas (3).

### Example 4: View Porter applications resource requests

```bash
export PORTER_TOKEN="your-api-token"
export PORTER_PROJECT_ID="12345"
./k8s-resource-cli -porter -output requests
```

Output:
```
DEPLOYMENT         NAMESPACE                                REPLICAS   CPU          MEMORY
web-app-web        dt-abc123-def456-ghi789                 1/3        1.00 cores   2.00 GB
web-app-worker     dt-abc123-def456-ghi789                 2/5        2.00 cores   4.00 GB
api-service-web    dt-xyz789-uvw456-rst123                 1/2        500m         1.00 GB
TOTAL                                                                  3.50 cores   7.00 GB
```

### Example 5: View Porter applications max resource requests

```bash
./k8s-resource-cli -porter -output max-requests
```

Output:
```
DEPLOYMENT         NAMESPACE                                REPLICAS   CPU          MEMORY
web-app-web        dt-abc123-def456-ghi789                 3          3.00 cores   6.00 GB
web-app-worker     dt-abc123-def456-ghi789                 5          5.00 cores   10.00 GB
api-service-web    dt-xyz789-uvw456-rst123                 2          1.00 cores   2.00 GB
TOTAL                                                                  9.00 cores   18.00 GB
```

## How It Works

### Kubernetes Direct Access

1. **Kubernetes Client**: The tool uses the official Kubernetes Go client library and connects to your cluster using the kubeconfig file.

2. **Resource Requests**: Reads the pod specifications for each deployment and sums up the CPU and memory requests across all containers and pods.

3. **Current Usage**: Queries the Metrics Server API to get real-time CPU and memory usage for running pods.

4. **HPA Integration**: Looks up HorizontalPodAutoscaler resources associated with each deployment and calculates the total resources needed if scaled to max replicas.

### Porter API Access

1. **Porter Client**: The tool uses the Porter REST API with bearer token authentication to retrieve application information.

2. **Application Discovery**: Calls `/api/v2/alpha/projects/{project_id}/applications` to list all applications in the project.

3. **Service Details**: For each application, retrieves detailed service configuration including resource specifications and instance counts (min/max replicas).

4. **Resource Calculation**:
   - Parses CPU and memory values from service configurations (supports formats like "1000m", "1.5 cores", "512Mi", "2Gi")
   - Calculates current requests based on minimum replicas
   - Calculates max requests based on maximum replicas (similar to HPA max)
   - Aggregates totals across all services and applications

5. **Output Modes**:
   - `requests`: Shows current resource requests based on minimum replicas
   - `max-requests`: Shows maximum potential resource requests based on maximum replicas
   - `usage`: Not supported in Porter API mode (requires direct cluster access)

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

### Porter API errors

**"Error: Porter token required"**
- Set the `PORTER_TOKEN` environment variable or use `-porter-token` flag
- Get your token from the Porter dashboard settings

**"Error: Porter project ID required"**
- Set the `PORTER_PROJECT_ID` environment variable or use `-porter-project-id` flag
- Find your project ID in the Porter dashboard URL or project settings

**"API request failed with status 401"**
- Your Porter token is invalid or expired
- Generate a new token from the Porter dashboard

**"API request failed with status 403"**
- Your token doesn't have access to the specified project
- Verify the project ID is correct and your account has access

**"API request failed with status 404"**
- The project ID doesn't exist or the API endpoint has changed
- Verify your project ID and check if using the correct Porter instance URL

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
