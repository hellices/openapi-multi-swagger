<!-- README.md -->
# OpenAPI Multi-Swagger Aggregator

[![Go Report Card](https://goreportcard.com/badge/github.com/hellices/openapi-multi-swagger)](https://goreportcard.com/report/github.com/hellices/openapi-multi-swagger)
[![GitHub License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/hellices/openapi-multi-swagger)](go.mod)

## Overview

`openapi-multi-swagger` is a Go-based web service that serves a Swagger UI interface capable of displaying multiple OpenAPI (v2/v3) specifications. It dynamically loads these specifications from a Kubernetes ConfigMap and watches for updates, allowing for a centralized and auto-updating API documentation portal.

The service serves a custom Swagger UI, which is pre-configured to fetch a list of API specification URLs from a backend endpoint (`/specs`). This endpoint, in turn, gets its data from the configured Kubernetes ConfigMap.

## Features

*   **Multiple OpenAPI Specs:** Aggregates and displays multiple OpenAPI specifications in a single Swagger UI instance.
*   **Kubernetes Integration:** Loads API specification metadata (name and URL) from a Kubernetes ConfigMap.
*   **Dynamic Updates:** Watches the ConfigMap for changes and automatically updates the available API specifications without requiring a server restart.
*   **Static Swagger UI:** Serves the Swagger UI static files.
*   **Configurable:** Key parameters like namespace, ConfigMap name, port, and watch interval are configurable via environment variables.
*   **Docker Support:** Includes a `Dockerfile` for easy containerization and deployment.

## Prerequisites

*   **Go:** Version 1.22 or higher (for building locally).
*   **Docker:** (Optional) For building and running the containerized application.
*   **Kubernetes Cluster:** Required for loading API specifications. The service needs access to a Kubernetes cluster (either via in-cluster authentication or a local `kubeconfig` file).

## Configuration

### 1. Kubernetes ConfigMap

The service expects a ConfigMap in your Kubernetes cluster. Each data entry in the ConfigMap should be a JSON string representing an API specification's metadata.

**Structure of each JSON entry in ConfigMap data:**

```json
{
  "name": "Service Name Displayed in UI",
  "url": "URL to the openapi.json or openapi.yaml file"
}
```

**Example ConfigMap (`openapi-specs.yaml`):**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: openapi-specs # Must match CONFIGMAP_NAME environment variable
  namespace: default   # Must match NAMESPACE environment variable
data:
  service-a: |
    {
      "name": "My Awesome Service A",
      "url": "https://petstore.swagger.io/v2/swagger.json"
    }
  service-b: |
    {
      "name": "Another Great Service B (v3)",
      "url": "https://petstore3.swagger.io/api/v3/openapi.json"
    }
  # Add more services here
```

Apply this ConfigMap to your cluster: `kubectl apply -f openapi-specs.yaml -n <your-namespace>`

### 2. Environment Variables

The application can be configured using the following environment variables:

*   `NAMESPACE`: The Kubernetes namespace where the ConfigMap is located.
    *   Default: `default`
*   `CONFIGMAP_NAME`: The name of the ConfigMap containing the API specifications.
    *   Default: `openapi-specs`
*   `PORT`: The port on which the server will listen.
    *   Default: `9090`
*   `WATCH_INTERVAL_SECONDS`: The interval (in seconds) at which the service checks the ConfigMap for updates.
    *   Default: `10`
*   `LOG_LEVEL`: Sets the logging level.
    *   Supported values: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`.
    *   Default: `info`
*   `DEV_MODE`: If set to `true`, enables more verbose logging (equivalent to `LOG_LEVEL=debug`) and may enable other development-specific behaviors.
    *   Default: `false`
*   `SWAGGER_BASE_PATH`: If the service is run behind a reverse proxy with a subpath, set this to the subpath (e.g., `/swagger`).
    *   Default: `""` (empty string)

## Building and Running

### Local Development

1.  **Clone the repository (if you haven't already).**
2.  **Build the application:**
    ```bash
    go build -o openapi-multi-swagger cmd/main.go
    ```
3.  **Set environment variables (if not using defaults):**
    ```bash
    export NAMESPACE="my-namespace"
    export CONFIGMAP_NAME="my-api-docs"
    export PORT="8080"
    # etc.
    ```
4.  **Run the application:**
    ```bash
    ./openapi-multi-swagger
    ```
    The server will start, and you can access the Swagger UI at `http://localhost:<PORT>`.

### Using Docker

1.  **Build the Docker image:**
    ```bash
    docker build -t openapi-multi-swagger .
    ```
2.  **Run the Docker container:**
    ```bash
    docker run -p 9090:9090 \
      -e NAMESPACE="my-namespace" \
      -e CONFIGMAP_NAME="my-api-docs" \
      -e PORT="9090" \
      openapi-multi-swagger
    ```
    *   Adjust the port mapping (`-p <host_port>:<container_port>`) if your `PORT` environment variable is different from `9090`.
    *   Pass any necessary environment variables using the `-e` flag.
    Access the Swagger UI at `http://localhost:<host_port>`.

## Kubernetes Deployment Examples

Below are example `deployment.yaml` and `service.yaml` files for deploying the application to Kubernetes.

**`deployment.yaml`:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: openapi-multi-swagger
  labels:
    app: openapi-multi-swagger
spec:
  replicas: 1
  selector:
    matchLabels:
      app: openapi-multi-swagger
  template:
    metadata:
      labels:
        app: openapi-multi-swagger
    spec:
      # If your Kubernetes cluster requires specific RBAC permissions for the pod to read ConfigMaps,
      # you might need to specify a serviceAccountName here and create corresponding Role and RoleBinding.
      # serviceAccountName: openapi-multi-swagger-sa
      containers:
      - name: openapi-multi-swagger
        # Replace with your actual image path, e.g., ghcr.io/your-username/openapi-multi-swagger:latest
        image: ghcr.io/your-github-username/openapi-multi-swagger:latest
        imagePullPolicy: Always
        ports:
        - containerPort: 9090 # Should match the PORT env variable if changed
        env:
        - name: NAMESPACE
          value: "default" # Or use valueFrom: fieldRef: fieldPath: metadata.namespace to use the pod's namespace
        - name: CONFIGMAP_NAME
          value: "openapi-specs"
        - name: PORT
          value: "9090"
        - name: WATCH_INTERVAL_SECONDS
          value: "10"
        - name: LOG_LEVEL # Optional: trace, debug, info, warn, error, fatal, panic (defaults to info)
          value: "info"
        - name: DEV_MODE # Optional: set to "true" for more verbose logging (overrides LOG_LEVEL to debug)
          value: "false"
        # - name: SWAGGER_BASE_PATH # Optional: if running behind a proxy with a subpath
        #   value: "/swagger"
        resources:
          requests:
            cpu: "100m"
            memory: "128Mi"
          limits:
            cpu: "500m"
            memory: "256Mi"
```

**`service.yaml`:**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: openapi-multi-swagger-service
  labels:
    app: openapi-multi-swagger
spec:
  type: ClusterIP # Change to LoadBalancer for external access, or use an Ingress controller
  selector:
    app: openapi-multi-swagger
  ports:
  - protocol: TCP
    port: 80 # Port the service will be available on within the cluster
    targetPort: 9090 # Port the application is listening on in the container (must match PORT env)
```

**Notes:**
*   Remember to replace `ghcr.io/your-github-username/openapi-multi-swagger:latest` with the actual path to your Docker image.
*   If you need the service to be accessible externally, you might change `spec.type` in `service.yaml` to `LoadBalancer` or set up an Ingress resource.
*   The `deployment.yaml` includes placeholders for `LOG_LEVEL`, `DEV_MODE`, and `SWAGGER_BASE_PATH` environment variables.

## Project Structure

```
.
├── Dockerfile                # For building the Docker image
├── go.mod                    # Go module definition
├── go.sum                    # Go module checksums
├── server.go                 # Contains the core HTTP server logic and Swagger UI setup (package swagger)
├── cmd/
│   └── main.go               # Main application entry point, Kubernetes client logic, ConfigMap watcher
├── deployment.yaml           # Example Kubernetes Deployment manifest
├── service.yaml              # Example Kubernetes Service manifest
└── swagger-ui/               # Static assets for Swagger UI
    ├── index.html            # Main Swagger UI page
    └── assets/               # CSS, JS, and other assets for Swagger UI
```

## How It Works

1.  **Initialization (`cmd/main.go`):**
    *   Loads configuration from environment variables or uses default values.
    *   Creates an instance of the `Server` from the `swagger` package (defined in `server.go`).
    *   Starts a goroutine (`watchSpecsConfigMap`) that periodically:
        *   Connects to the Kubernetes cluster (using in-cluster config or local kubeconfig).
        *   Fetches the specified ConfigMap from the configured namespace.
        *   Parses each data entry in the ConfigMap as `APIMetadata` (name, URL).
        *   Calls `server.UpdateSpecs()` to update the list of API specifications served.

2.  **HTTP Server (`server.go`):**
    *   Defines the `Server` struct and its methods.
    *   `NewServer()`: Initializes the server.
    *   `Start(port)`: Starts the HTTP server.
    *   It serves the static files for Swagger UI from the `/swagger-ui/` directory.
    *   The `index.html` in `swagger-ui/` is modified to point its `urls` configuration to a `/specs` endpoint.
    *   The `/specs` endpoint (handled by `handleGetSpecs`) returns the current list of `APIMetadata` objects as JSON.
    *   `UpdateSpecs(specs)`: Atomically updates the list of specs that `/specs` will serve.

The Swagger UI then uses this `/specs` endpoint to populate its dropdown menu, allowing users to select and view different API specifications.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
