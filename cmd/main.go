package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv" // Added for parsing integer environment variables
	"time"

	server "openapi-multi-swagger" // Local module's swagger package (server.go in the root)

	"github.com/sirupsen/logrus" // Added for structured logging
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Constants for configuration. These can be overridden by environment variables.
const (
	defaultNamespaceValue = "default"
	configMapNameValue    = "openapi-specs"
	defaultPortValue      = 9090
	watchIntervalValue    = 10 * time.Second
	envVarLogLevel        = "LOG_LEVEL" // Added for logrus configuration
	envVarDevMode         = "DEV_MODE"  // Added for logrus configuration
	devModeTrue           = "true"      // Added for logrus configuration
)

var (
	namespace     string
	configMapName string
	port          int
	watchInterval time.Duration
	logger        *logrus.Logger // Global logger instance
)

func initLogger() {
	logger = logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	logLevelStr := os.Getenv(envVarLogLevel)
	level, err := logrus.ParseLevel(logLevelStr)
	if err != nil || logLevelStr == "" {
		level = logrus.InfoLevel // Default to Info level
	}
	logger.SetLevel(level)

	// Override to Debug level if DEV_MODE is true
	if os.Getenv(envVarDevMode) == devModeTrue {
		logger.SetLevel(logrus.DebugLevel)
		logger.Debug("DEV_MODE enabled, setting log level to DEBUG for main package")
	}
}

func loadConfig() {
	namespace = getStringEnv("NAMESPACE", defaultNamespaceValue)
	configMapName = getStringEnv("CONFIGMAP_NAME", configMapNameValue)
	port = getIntEnv("PORT", defaultPortValue)
	watchInterval = getDurationEnv("WATCH_INTERVAL_SECONDS", watchIntervalValue)

	logger.Info("Configuration loaded:")
	logger.Infof("  NAMESPACE: %s (Default: %s)", namespace, defaultNamespaceValue)
	logger.Infof("  CONFIGMAP_NAME: %s (Default: %s)", configMapName, configMapNameValue)
	logger.Infof("  PORT: %d (Default: %d)", port, defaultPortValue)
	logger.Infof("  WATCH_INTERVAL: %s (Default: %s)", watchInterval, watchIntervalValue)
}

// getStringEnv gets a string environment variable or returns a default value.
func getStringEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		logger.Debugf("Environment variable %s found: %s", key, value)
		return value
	}
	logger.Debugf("Environment variable %s not found, using default: %s", key, defaultValue)
	return defaultValue
}

// getIntEnv gets an integer environment variable or returns a default value.
func getIntEnv(key string, defaultValue int) int {
	if valueStr, exists := os.LookupEnv(key); exists {
		logger.Debugf("Environment variable %s found: %s", key, valueStr)
		if value, err := strconv.Atoi(valueStr); err == nil {
			return value
		}
		logger.Warnf("Invalid integer value for %s: '%s'. Using default: %d", key, valueStr, defaultValue)
	} else {
		logger.Debugf("Environment variable %s not found, using default: %d", key, defaultValue)
	}
	return defaultValue
}

// getDurationEnv gets a time.Duration environment variable (in seconds) or returns a default value.
func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	if valueStr, exists := os.LookupEnv(key); exists {
		logger.Debugf("Environment variable %s found: %s", key, valueStr)
		if seconds, err := strconv.Atoi(valueStr); err == nil {
			return time.Duration(seconds) * time.Second
		}
		logger.Warnf("Invalid duration value for %s: '%s'. Using default: %s", key, valueStr, defaultValue)
	} else {
		logger.Debugf("Environment variable %s not found, using default: %s", key, defaultValue)
	}
	return defaultValue
}

func main() {
	initLogger() // Initialize the global logger

	loadConfig() // Load configuration from environment variables or defaults

	// Create a new Swagger server instance
	// The server instance will initialize its own logger
	s := server.NewServer()

	logger.Infof("Using NAMESPACE: %s", namespace)

	// Start a goroutine to watch for ConfigMap changes and update API specs periodically
	go watchSpecsConfigMap(s)

	logger.Infof("Swagger UI server starting on port %d", port)
	// Start the server
	if err := s.Start(port); err != nil {
		logger.Fatalf("Failed to start server: %v", err)
	}
}

// watchSpecsConfigMap periodically checks the ConfigMap in the specified namespace,
// and if changes are detected, updates the API specifications and reflects them on the server.
func watchSpecsConfigMap(s *server.Server) {
	for {
		logger.Infof("Attempting to load API specs from ConfigMap '%s' in namespace '%s'", configMapName, namespace)
		// Load API specs from ConfigMap
		specs := loadSpecs(namespace, configMapName) // Pass configMapName
		if len(specs) > 0 {
			logger.Infof("Successfully loaded %d API spec(s). Updating server...", len(specs))
			s.UpdateSpecs(specs) // Update the server with the loaded specs
		} else {
			logger.Warn("No API specs loaded or an error occurred. Server not updated.")
		}
		// Wait for the next check
		time.Sleep(watchInterval)
	}
}

// loadSpecs loads the ConfigMap (by default "openapi-specs") from the specified namespace
// using either in-cluster Kubernetes configuration or local kubeconfig,
// parses the data within, and returns a list of API specifications.
func loadSpecs(configMapNamespace string, cmName string) []server.APIMetadata {
	var specs []server.APIMetadata

	// Try to use in-cluster config if running inside a Kubernetes cluster
	config, err := rest.InClusterConfig()
	if err != nil {
		// If in-cluster config fails, try to use local kubeconfig
		logger.Warnf("Failed to get in-cluster config: %v. Attempting to use local kubeconfig.", err)
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			logger.Errorf("Failed to get user home directory: %v", homeErr)
			return specs // Return empty list if home directory cannot be found
		}
		kubeconfigPath := filepath.Join(home, ".kube", "config")
		// Build config from local kubeconfig file
		localConfig, localErr := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if localErr != nil {
			logger.Errorf("Failed to load local kubeconfig at %s: %v", kubeconfigPath, localErr)
			return specs // Return empty list if local kubeconfig fails to load
		}
		config = localConfig // Use local config
		logger.Infof("Successfully loaded local kubeconfig from %s", kubeconfigPath)
	} else {
		logger.Info("Successfully loaded in-cluster config.")
	}

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Errorf("Failed to create Kubernetes client set: %v", err)
		return specs // Return empty list if clientset creation fails
	}

	logger.Infof("Attempting to load ConfigMap '%s' from namespace '%s'", cmName, configMapNamespace)
	// Get the ConfigMap
	cm, err := clientset.CoreV1().ConfigMaps(configMapNamespace).Get(context.Background(), cmName, metav1.GetOptions{})
	if err != nil {
		logger.Errorf("Failed to get ConfigMap '%s' in namespace '%s': %v", cmName, configMapNamespace, err)
		return specs // Return empty list if ConfigMap retrieval fails
	}

	logger.Infof("Successfully loaded ConfigMap '%s'", cmName)

	if len(cm.Data) == 0 {
		logger.Warnf("No data found in ConfigMap '%s'", cmName)
		return specs // Return empty list if no data in ConfigMap
	}

	// Parse each data entry in the ConfigMap into API specifications
	for key, dataStr := range cm.Data {
		logger.Debugf("Processing ConfigMap data key: %s", key)
		var apiInfo server.APIMetadata
		// Unmarshal JSON string into APIMetadata struct
		if err := json.Unmarshal([]byte(dataStr), &apiInfo); err != nil {
			logger.Errorf("Failed to unmarshal API info for key '%s': %v. Raw data: %s", key, err, dataStr)
			continue // Proceed to the next item if parsing fails for the current one
		}
		logger.Debugf("Successfully unmarshalled API info for key '%s': Name='%s', URL='%s'", key, apiInfo.Name, apiInfo.URL)
		specs = append(specs, apiInfo)
	}

	if len(specs) == 0 {
		logger.Warnf("No API specifications were successfully loaded from ConfigMap '%s'", cmName)
	} else {
		logger.Infof("Loaded %d API specification(s) from ConfigMap '%s'", len(specs), cmName)
	}
	return specs
}
