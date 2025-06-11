// Package swagger provides a Swagger UI server for displaying OpenAPI specifications
package swagger

import (
	"bytes" // Added for proxy request body handling
	"embed"
	"encoding/json"
	"fmt"
	"io" // Restored for proxy request body and response handling
	"net/http"
	"net/url"
	"os"
	"path/filepath" // Added for joining embed paths
	"strings"
	"sync"

	"github.com/sirupsen/logrus" // Added for structured logging
)

//go:embed swagger-ui/*
var swaggerUI embed.FS

// Constants for environment variable names
const (
	envVarSwaggerBasePath = "SWAGGER_BASE_PATH"
	envVarDevMode         = "DEV_MODE"
	envVarLogLevel        = "LOG_LEVEL" // Added for logrus configuration
)

// Constants for dev mode
const (
	devModeTrue                  = "true"
	devModeLocalhostReplacement  = "http://localhost:8080"   // Replacement URL for local development
)

// Constants for embedded file system paths
const (
	swaggerUIEmbedRoot    = "swagger-ui"
	swaggerUIIndexHTML    = "index.html"
	swaggerUIAssetsSubDir = "assets"
)

// Constants for HTTP paths and prefixes
const (
	httpPathRoot         = "/"
	httpPathIndexHTML    = "/index.html"
	httpPathSwaggerSpecs = "/swagger-specs" // Endpoint to serve the list of specs for UI consumption
	httpPathAPI          = "/api/"          // Prefix for serving individual, processed API specs
	httpPathProxy        = "/proxy/"        // Prefix for proxying requests
	queryParamProxyURL   = "proxyUrl"
)

// Constants for HTTP headers and values
const (
	headerContentType                = "Content-Type"
	headerCacheControl               = "Cache-Control"
	headerAccessControlAllowOrigin   = "Access-Control-Allow-Origin"
	headerAccessControlAllowMethods  = "Access-Control-Allow-Methods"
	headerAccessControlAllowHeaders  = "Access-Control-Allow-Headers"
	headerAccessControlExposeHeaders = "Access-Control-Expose-Headers"

	contentTypeHTML        = "text/html"
	contentTypeJSON        = "application/json"
	contentTypeCSS         = "text/css"
	contentTypeJS          = "application/javascript"
	contentTypePNG         = "image/png"
	contentTypeOctetStream = "application/octet-stream"

	cacheControlPublicMaxAge3600 = "public, max-age=3600"
	corsAllowOriginAll           = "*"
	corsAllowMethodsDefault      = "GET, POST, OPTIONS"
	corsAllowHeadersDefault      = "Content-Type, Authorization"
	corsExposeHeadersDefault     = "Content-Length"
)

// Constants for error messages and log formats
const (
	logErrFailedToCloseResponseBody = "Failed to close response body: %v"
	logErrCopyingResponseBody       = "Error copying response body: %v"
	logMsgServingFile               = "Serving file: %s, Content-Type: %s"
	logMsgServingEmbeddedFile       = "Serving embedded file: %s"
	logMsgFailedToServeEmbeddedFile = "Failed to serve embedded file %s: %v"
	logMsgBasePathMetaTagAdded      = "Added base path meta tag: %s"
	logMsgHandlingRequest           = "Handling request: %s %s"
	logMsgProxyingRequestTo         = "Proxying request for %s to %s"
	logMsgDevModeURLRewrite         = "DEV_MODE: Rewriting URL %s to %s"
	logMsgUpdatingSpecServerInfo    = "Updating server info for spec: %s"
	logMsgAPISpecsUpdated           = "API specs updated. Total specs: %d"
	logMsgStartingServer            = "Starting Swagger UI server on port %d (HTTP)"

	errMsgFailedToReadIndexHTML      = "Failed to read index.html"
	errMsgFailedToWriteResponse      = "Failed to write response"
	errMsgFailedToEncodeSpecs        = "Failed to encode specs"
	errMsgAPINotFound                = "API not found"
	errMsgFailedToFetchSpec          = "Failed to fetch spec: %v"
	errMsgFailedToFetchSpecStatus    = "Failed to fetch spec, status: %d"
	errMsgFailedToParseMetaURL       = "Failed to parse metadata URL: %v"
	errMsgFailedToDecodeOpenAPISpec  = "Failed to decode OpenAPI spec: %v"
	errMsgFailedToEncodeModifiedSpec = "Failed to encode modified spec"
	errMsgProxyURLRequired           = "proxyUrl query parameter is required for all requests"
	errMsgFailedToCreateProxyReq     = "Failed to create proxy request: %v"
	errMsgFailedToForwardProxyReq    = "Failed to forward request: %v"
	errMsgStaticFileNotFound         = "Static file not found"
)

// APIMetadata represents metadata about an OpenAPI specification
type APIMetadata struct {
	Name           string   `json:"name"`           // API name
	URL            string   `json:"url"`            // URL to fetch the OpenAPI spec
	Title          string   `json:"title"`          // Display title
	Version        string   `json:"version"`        // API version
	Description    string   `json:"description"`    // API description
	ResourceType   string   `json:"resourceType"`   // Type of resource (e.g., Service, Deployment)
	ResourceName   string   `json:"resourceName"`   // Name of the Kubernetes resource
	Namespace      string   `json:"namespace"`      // Kubernetes namespace
	LastUpdated    string   `json:"lastUpdated"`    // Last update timestamp
	AllowedMethods []string `json:"allowedMethods"` // Allowed HTTP methods for Swagger UI
	Error          string   `json:"error,omitempty"`
}

// Server serves the Swagger UI and aggregated OpenAPI specs
type Server struct {
	specs    map[string]APIMetadata // Map of API name to metadata
	specsMux sync.RWMutex           // Mutex for thread-safe access to specs
	basePath string                 // Base path for the server (for Ingress/Route support), e.g., /swagger
	logger   *logrus.Logger         // Added for structured logging
}

// NewServer creates a new Swagger UI server
func NewServer() *Server {
	logger := logrus.New()
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
		logger.Debug("DEV_MODE enabled, setting log level to DEBUG")
	}

	return &Server{
		specs:    make(map[string]APIMetadata),
		basePath: os.Getenv(envVarSwaggerBasePath),
		logger:   logger,
	}
}

// UpdateSpecs updates the stored OpenAPI specs based on the current status
func (s *Server) UpdateSpecs(apis []APIMetadata) {
	s.specsMux.Lock()
	defer s.specsMux.Unlock()

	newSpecs := make(map[string]APIMetadata)
	for _, api := range apis {
		// Skip APIs with errors
		if api.Error != "" {
			s.logger.Warnf("Skipping API spec for %s due to existing error: %s", api.Name, api.Error)
			continue
		}

		metadata := APIMetadata{
			Name:           api.Name,
			URL:            api.URL,
			Title:          api.Name,
			Description:    fmt.Sprintf("API from %s/%s", api.Namespace, api.ResourceName),
			ResourceType:   api.ResourceType,
			ResourceName:   api.ResourceName,
			Namespace:      api.Namespace,
			LastUpdated:    api.LastUpdated,
			AllowedMethods: api.AllowedMethods,
		}

		newSpecs[api.Name] = metadata
	}
	s.specs = newSpecs
	s.logger.Infof(logMsgAPISpecsUpdated, len(s.specs))
}

// stripBasePath removes the base path prefix from the request path
func (s *Server) stripBasePath(path string) string {
	if s.basePath != "" && strings.HasPrefix(path, s.basePath) {
		return strings.TrimPrefix(path, s.basePath)
	}
	return path
}

// logAndSendError logs a detailed error message on the server and sends a generic error response to the client.
// internalFullLogMsg should be a complete, formatted message for server logs.
// userMsg is the generic message sent to the HTTP client.
func (s *Server) logAndSendError(w http.ResponseWriter, r *http.Request, statusCode int, internalFullLogMsg string, userMsg string) {
	s.logger.Errorf("[%s %s] HTTP %d - %s", r.Method, r.URL.Path, statusCode, internalFullLogMsg)
	http.Error(w, userMsg, statusCode)
}

// ServeHTTP is the main HTTP handler for the server.
// It routes requests to the appropriate handlers based on the path.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers for all responses
	w.Header().Set(headerAccessControlAllowOrigin, corsAllowOriginAll)
	w.Header().Set(headerAccessControlAllowMethods, corsAllowMethodsDefault)
	w.Header().Set(headerAccessControlAllowHeaders, corsAllowHeadersDefault)
	w.Header().Set(headerAccessControlExposeHeaders, corsExposeHeadersDefault)

	// Handle OPTIONS requests for CORS preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Strip base path if configured, to normalize routing logic
	path := s.stripBasePath(r.URL.Path)
	s.logger.Debugf(logMsgHandlingRequest, r.Method, path)

	// Route to appropriate handler
	switch {
	case path == httpPathRoot || path == httpPathIndexHTML:
		s.serveIndex(w, r)
	case path == httpPathSwaggerSpecs:
		s.serveSpecs(w, r)
	case strings.HasPrefix(path, httpPathAPI):
		s.serveIndividualSpec(w, r)
	case strings.HasPrefix(path, httpPathProxy):
		s.proxyRequest(w, r)
	default:
		// Assume it's a request for a static file from the embedded swagger-ui
		s.serveStaticFiles(w, r)
	}
}

// serveIndex serves the Swagger UI index page
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	// Construct path to embedded index.html
	indexFile := filepath.Join(swaggerUIEmbedRoot, swaggerUIIndexHTML)
	s.logger.Debugf(logMsgServingEmbeddedFile, indexFile)

	indexContent, err := swaggerUI.ReadFile(indexFile)
	if err != nil {
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(logMsgFailedToServeEmbeddedFile, indexFile, err), errMsgFailedToReadIndexHTML)
		return
	}

	// Add base path meta tag if basePath is configured
	htmlContent := string(indexContent)
	if s.basePath != "" {
		metaTag := fmt.Sprintf(`<meta name="base-path" content="%s">`, s.basePath)
		htmlContent = strings.Replace(htmlContent, "</head>", metaTag+"</head>", 1)
		s.logger.Debugf(logMsgBasePathMetaTagAdded, s.basePath)
	}

	w.Header().Set(headerContentType, contentTypeHTML)
	if _, err := w.Write([]byte(htmlContent)); err != nil {
		// Use a formatted internal message
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", errMsgFailedToWriteResponse, err), errMsgFailedToWriteResponse)
	}
}

// serveSpecs serves the list of available API specs, used by Swagger UI to populate the dropdown.
func (s *Server) serveSpecs(w http.ResponseWriter, r *http.Request) {
	s.specsMux.RLock()
	defer s.specsMux.RUnlock()

	w.Header().Set(headerContentType, contentTypeJSON)
	if err := json.NewEncoder(w).Encode(s.specs); err != nil {
		// Use a formatted internal message
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", errMsgFailedToEncodeSpecs, err), errMsgFailedToEncodeSpecs)
	}
}

// serveIndividualSpec serves individual OpenAPI spec by fetching it in real-time
// and potentially modifying its server information.
func (s *Server) serveIndividualSpec(w http.ResponseWriter, r *http.Request) {
	apiName := strings.TrimPrefix(r.URL.Path, httpPathAPI)

	s.specsMux.RLock()
	metadata, exists := s.specs[apiName]
	s.specsMux.RUnlock()

	if !exists {
		// Use a formatted internal message
		s.logAndSendError(w, r, http.StatusNotFound, fmt.Sprintf("%s: %s", errMsgAPINotFound, apiName), errMsgAPINotFound)
		return
	}

	// Fetch the spec in real-time
	urlStr := metadata.URL
	if os.Getenv(envVarDevMode) == devModeTrue {
		// In development mode, rewrite any cluster URLs to localhost for easier local testing
		// Example: if you wanted to replace all http with https in dev mode
		// if strings.HasPrefix(urlStr, "http://") {
		//  originalURL := urlStr
		//  urlStr = strings.Replace(urlStr, "http://", "https://", 1)
		//  s.logger.Debugf("DEV_MODE: Rewriting URL %s to %s", originalURL, urlStr)
		// }
	}
	resp, err := http.Get(urlStr)
	if err != nil {
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(errMsgFailedToFetchSpec, err), fmt.Sprintf(errMsgFailedToFetchSpec, err))
		return
	}
	if resp != nil {
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				s.logger.Warnf(logErrFailedToCloseResponseBody, closeErr)
			}
		}()
	}

	if resp.StatusCode != http.StatusOK {
		s.logAndSendError(w, r, resp.StatusCode, fmt.Sprintf(errMsgFailedToFetchSpecStatus, resp.StatusCode), fmt.Sprintf(errMsgFailedToFetchSpecStatus, resp.StatusCode))
		return
	}

	// Parse metadata URL to get the server URL for potential spec modification
	metadataURL, err := url.Parse(metadata.URL)
	if err != nil {
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(errMsgFailedToParseMetaURL, err), fmt.Sprintf(errMsgFailedToParseMetaURL, err))
		return
	}

	// Read and parse the OpenAPI/Swagger spec
	var spec map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(errMsgFailedToDecodeOpenAPISpec, err), fmt.Sprintf(errMsgFailedToDecodeOpenAPISpec, err))
		return
	}

	// Update spec server info (e.g., host, servers array) based on OpenAPI/Swagger version
	s.logger.Debugf(logMsgUpdatingSpecServerInfo, apiName)
	s.updateSpecServerInfo(spec, metadataURL)

	w.Header().Set(headerContentType, contentTypeJSON)
	if err := json.NewEncoder(w).Encode(spec); err != nil {
		// Use a formatted internal message
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", errMsgFailedToEncodeModifiedSpec, err), errMsgFailedToEncodeModifiedSpec)
	}
}

// serveStaticFiles serves embedded static files (CSS, JS, images) for the Swagger UI.
func (s *Server) serveStaticFiles(w http.ResponseWriter, r *http.Request) {
	// Path relative to the swaggerUIEmbedRoot, after stripping any server basePath
	relativePath := s.stripBasePath(r.URL.Path)

	// Try to read from assets subdirectory first, then from the root of swagger-ui embed.
	filePathInEmbed := filepath.Join(swaggerUIEmbedRoot, swaggerUIAssetsSubDir, relativePath)
	content, err := swaggerUI.ReadFile(filePathInEmbed)
	if err != nil {
		filePathInEmbed = filepath.Join(swaggerUIEmbedRoot, relativePath) // Try root
		content, err = swaggerUI.ReadFile(filePathInEmbed)
		if err != nil {
			// Use a formatted internal message
			s.logAndSendError(w, r, http.StatusNotFound, fmt.Sprintf("%s: %s", errMsgStaticFileNotFound, relativePath), errMsgStaticFileNotFound)
			return
		}
	}

	// Set appropriate content type based on file extension
	contentType := s.getContentType(relativePath)
	w.Header().Set(headerContentType, contentType)
	w.Header().Set(headerCacheControl, cacheControlPublicMaxAge3600) // Add caching for static files
	s.logger.Debugf(logMsgServingFile, relativePath, contentType)

	if _, err := w.Write(content); err != nil {
		// Use a formatted internal message
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf("%s: %v", errMsgFailedToWriteResponse, err), errMsgFailedToWriteResponse)
	}
}

// getContentType determines the content type based on file extension
func (s *Server) getContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".css"):
		return contentTypeCSS
	case strings.HasSuffix(path, ".js"):
		return contentTypeJS
	case strings.HasSuffix(path, ".png"):
		return contentTypePNG
	case strings.HasSuffix(path, ".html"):
		return contentTypeHTML
	default:
		return contentTypeOctetStream
	}
}

// proxyRequest handles proxying requests to backend services.
// It requires a 'proxyUrl' query parameter specifying the target URL.
func (s *Server) proxyRequest(w http.ResponseWriter, r *http.Request) {
	targetProxyURL := r.URL.Query().Get(queryParamProxyURL)
	if targetProxyURL == "" {
		s.logAndSendError(w, r, http.StatusBadRequest, errMsgProxyURLRequired, errMsgProxyURLRequired)
		return
	}

	finalTargetURL := targetProxyURL

	if os.Getenv(envVarDevMode) == devModeTrue {
		// REMOVED: devModeClusterInternalSuffix logic
		// Example: if you wanted to ensure all proxy requests go to localhost in dev mode
		// if !strings.HasPrefix(finalTargetURL, "http://localhost") && !strings.HasPrefix(finalTargetURL, "https://localhost") {
		//  s.logger.Debugf("DEV_MODE: Potentially redirecting proxy for %s to a localhost equivalent if applicable", finalTargetURL)
		// }
	}
	s.logger.Debugf(logMsgProxyingRequestTo, r.URL.Path, finalTargetURL)

	var reqBodyReader io.Reader
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			// Use a formatted internal message
			s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf("Failed to read request body for proxy: %v", err), "Failed to read request body")
			return
		}
		r.Body.Close()                                    // Close original body after reading
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Allow re-reading of original body if necessary elsewhere
		reqBodyReader = bytes.NewBuffer(bodyBytes)        // Use a new buffer for the proxy request
	}

	proxyReq, err := http.NewRequest(r.Method, finalTargetURL, reqBodyReader)
	if err != nil {
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(errMsgFailedToCreateProxyReq, err), fmt.Sprintf(errMsgFailedToCreateProxyReq, err))
		return
	}

	for name, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(name, value)
		}
	}

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		s.logger.Errorf("Error forwarding proxy request: %v", err) // Log internal error before sending generic response
		s.logAndSendError(w, r, http.StatusInternalServerError, fmt.Sprintf(errMsgFailedToForwardProxyReq, err), fmt.Sprintf(errMsgFailedToForwardProxyReq, err))
		return
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			s.logger.Warnf(logErrFailedToCloseResponseBody, closeErr)
		}
	}()

	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.Errorf("[%s %s] HTTP %d - Error copying response body: %v", r.Method, r.URL.Path, http.StatusInternalServerError, err)
	}
}

// makeServerURL constructs a full URL from a base metadataURL and a pathComponent.
// If pathComponent is an absolute URL, it's returned directly.
// Otherwise, it's resolved relative to the metadataURL.
func (s *Server) makeServerURL(metadataURL *url.URL, pathComponent string) string {
	if pathComponent == "" {
		return fmt.Sprintf("%s://%s", metadataURL.Scheme, metadataURL.Host)
	}

	parsedPathComponent, err := url.Parse(pathComponent)
	if err == nil && parsedPathComponent.IsAbs() {
		return pathComponent // It's already a full URL
	}

	base := *metadataURL
	if base.Path == "" || base.Path[len(base.Path)-1] != '/' {
		if pathComponent != "" && (len(pathComponent) == 0 || pathComponent[0] != '/') {
			base.Path += "/"
		}
	}

	if strings.HasPrefix(pathComponent, "/") {
		return fmt.Sprintf("%s://%s%s", metadataURL.Scheme, metadataURL.Host, pathComponent)
	}

	resolvedURL := base.ResolveReference(&url.URL{Path: pathComponent})
	return resolvedURL.String()
}

// updateSpecServerInfo updates the server information (host, servers array) in the OpenAPI spec
// based on its version (OpenAPI 3.x or Swagger 2.0) using the metadataURL as the base.
// For OpenAPI 3.x, it modifies the servers array.
// For Swagger 2.0, it sets the host field.
// For Swagger 1.2 or undefined, it updates the basePath field.
// No HTTP responses sent from here, so logging is direct.
func (s *Server) updateSpecServerInfo(spec map[string]interface{}, metadataURL *url.URL) {
	openAPIVersion, _ := spec["openapi"].(string)
	swaggerVersion, _ := spec["swagger"].(string)

	// OpenAPI 3.x
	if openAPIVersion != "" && strings.HasPrefix(openAPIVersion, "3.") {
		existingServers, _ := spec["servers"].([]interface{})
		newServers := make([]interface{}, 0)

		// If there are existing servers, get the URI part from the first server
		if len(existingServers) > 0 {
			if firstServer, ok := existingServers[0].(map[string]interface{}); ok {
				if serverURL, ok := firstServer["url"].(string); ok {
					newServers = append(newServers, map[string]interface{}{
						"url": s.makeServerURL(metadataURL, serverURL),
					})
				}
			}
		}

		// If we couldn't get URI from existing servers, add just the host
		if len(newServers) == 0 {
			newServers = append(newServers, map[string]interface{}{
				"url": s.makeServerURL(metadataURL, ""),
			})
		}

		// Append existing servers
		newServers = append(newServers, existingServers...)
		spec["servers"] = newServers

	} else if swaggerVersion == "2.0" { // Swagger/OpenAPI 2.0
		spec["host"] = metadataURL.Host

	} else { // Swagger 1.2 or undefined
		if basePath, ok := spec["basePath"].(string); ok {
			spec["basePath"] = s.makeServerURL(metadataURL, basePath)
		}
	}
}

// Start starts the Swagger UI server on the specified port.
func (s *Server) Start(port int) error {
	// Use a new ServeMux for routing.
	// The main ServeHTTP method of the Server struct will handle all requests to "/".
	mux := http.NewServeMux()
	// Note: The individual handlers like serveSpecs, serveIndividualSpec are now called
	// from within s.ServeHTTP based on path matching.
	// We register s.ServeHTTP as the handler for the root path.
	// Specific paths like /swagger-specs, /api/ are handled inside s.ServeHTTP.
	mux.Handle(httpPathRoot, s) // Register the Server itself as the handler for all paths

	srv := &http.Server{
		Addr:      fmt.Sprintf(":%d", port),
		Handler:   mux,
		TLSConfig: nil, // TLS is not configured for this server
	}

	s.logger.Infof(logMsgStartingServer, port)
	return srv.ListenAndServe()
}
