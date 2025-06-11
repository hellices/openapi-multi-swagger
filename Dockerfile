FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum files to download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project
COPY . .

# Build the application
# CGO_ENABLED=0 is important for a static build, especially in Alpine
# -ldflags="-s -w" strips debug information, reducing binary size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/openapi-multi-swagger cmd/main.go

# --- Final Stage ---
FROM alpine:latest

WORKDIR /app

# Copy the built binary from the builder stage
COPY --from=builder /app/openapi-multi-swagger /app/openapi-multi-swagger

# Copy swagger-ui static assets
COPY swagger-ui /app/swagger-ui

# Expose the port the application runs on
# This should match the port your application listens on (default or from PORT env var)
EXPOSE 9090

# Environment variables (can be overridden at runtime)
ENV NAMESPACE="default"
ENV CONFIGMAP_NAME="openapi-specs"
ENV PORT="9090"
ENV WATCH_INTERVAL_SECONDS="10"
# Optional: Set Gin mode if using Gin (e.g., ENV GIN_MODE="release")

# Command to run the application
CMD ["/app/openapi-multi-swagger"]
