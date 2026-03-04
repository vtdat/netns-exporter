package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"github.com/sirupsen/logrus"
)

const exporterName = "netns_exporter"

// ResponseWriter wrapper to capture status code in middleware
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Unwrap returns the underlying ResponseWriter (for Go 1.20+ compatibility)
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

type APIServer struct {
	config   *NetnsExporterConfig
	server   *http.Server
	logger   logrus.FieldLogger
	registry *prometheus.Registry // Use a specific registry instead of global
	cache    *MetricCache
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewAPIServer(config *NetnsExporterConfig, logger *logrus.Logger) (*APIServer, error) {
	// 1. Create a dedicated registry (Best Practice)
	// This isolates your metrics from global state pollution.
	registry := prometheus.NewRegistry()

	// Register standard Go runtime metrics and process metrics
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(version.NewCollector(exporterName))

	// 2. Create metric cache
	cache := NewMetricCache(config.ScrapeInterval, logger)

	// 3. Create context for cache lifecycle management
	ctx, cancel := context.WithCancel(context.Background())

	apiServer := &APIServer{
		config:   config,
		logger:   logger.WithField("component", "api-server"),
		registry: registry,
		cache:    cache,
		ctx:      ctx,
		cancel:   cancel,
	}

	// 4. Register your custom collector
	collector := NewCollector(config, logger, cache)
	if err := registry.Register(collector); err != nil {
		cancel()
		return nil, fmt.Errorf("registering collector failed: %w", err)
	}

	// 5. Start periodic cache updates in background
	cache.StartPeriodicUpdate(ctx, collector)

	// 6. Configure HTTP Server
	httpMux := http.NewServeMux()

	// Use net.JoinHostPort for robust address formatting (handles IPv6)
	address := net.JoinHostPort(config.APIServer.ServerAddress, strconv.Itoa(config.APIServer.ServerPort))

	timeout := time.Duration(config.APIServer.RequestTimeout) * time.Second

	apiServer.server = &http.Server{
		Addr:              address,
		Handler:           httpMux,
		ReadHeaderTimeout: timeout,
		WriteTimeout:      timeout,
		IdleTimeout:       timeout,
	}

	// 7. Setup Routes
	// Use the dedicated registry for the handler
	promHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog:      logger,
		ErrorHandling: promhttp.ContinueOnError,
	})

	httpMux.HandleFunc("/", apiServer.indexPage)
	httpMux.Handle(config.APIServer.TelemetryPath, apiServer.middlewareLogging(promHandler))

	return apiServer, nil
}

// Start runs the HTTP server. This method blocks.
func (s *APIServer) Start() error {
	s.logger.Infof("Starting API server on %s", s.server.Addr)
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server and cache updates.
func (s *APIServer) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down API server...")

	// Stop periodic cache updates
	s.cancel()

	return s.server.Shutdown(ctx)
}

func (s *APIServer) middlewareLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		s.logger.WithFields(logrus.Fields{
			"addr":     r.RemoteAddr,
			"method":   r.Method,
			"path":     r.URL.Path,
			"status":   rw.statusCode,
			"duration": duration,
			"agent":    r.UserAgent(),
		}).Debug("HTTP Request")
	})
}

func (s *APIServer) indexPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	html := fmt.Sprintf(`<html>
<head><title>Network Namespace Exporter</title></head>
<body>
<h1>Network Namespace Exporter</h1>
<p><a href='%s'>Metrics</a></p>
</body>
</html>`, s.config.APIServer.TelemetryPath)

	if _, err := w.Write([]byte(html)); err != nil {
		s.logger.Errorf("error writing index page: %s", err)
	}
}
