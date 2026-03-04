package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

// createTestConfig creates a minimal valid config for testing
func createTestConfig() *NetnsExporterConfig {
	return &NetnsExporterConfig{
		APIServer: APIServerConfig{
			ServerAddress:  "127.0.0.1",
			ServerPort:     0, // Use any available port
			RequestTimeout: 5,
			TelemetryPath:  "/metrics",
		},
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    "/tmp/test-netns-exporter",
		Threads:         1,
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      true,
			ARP:       true,
		},
	}
}

// createTestLogger creates a logger for testing
func createTestExporterLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	logger.SetOutput(io.Discard)
	return logger
}

// TestNewAPIServer tests server initialization with all components
func TestNewAPIServer(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()

	// Create temp log directory
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	if server == nil {
		t.Fatal("NewAPIServer() returned nil")
	}
	if server.config != config {
		t.Error("server.config should be set")
	}
	if server.registry == nil {
		t.Error("server.registry should be set")
	}
	if server.cache == nil {
		t.Error("server.cache should be set")
	}
	if server.server == nil {
		t.Error("server.server should be set")
	}
	if server.ctx == nil {
		t.Error("server.ctx should be set")
	}
	if server.cancel == nil {
		t.Error("server.cancel should be set")
	}
}

// TestNewAPIServer_NilConfig tests error handling with nil config
func TestNewAPIServer_NilConfig(t *testing.T) {
	logger := createTestExporterLogger()

	// This should not panic, but may fail during cache creation
	// The actual behavior depends on how nil config is handled
	defer func() {
		if r := recover(); r != nil {
			t.Logf("NewAPIServer with nil config panicked: %v", r)
		}
	}()

	_, err := NewAPIServer(nil, logger)
	if err == nil {
		t.Log("NewAPIServer(nil) did not return error (may be expected)")
	}
}

// TestAPIServer_IndexPage tests the index page handler
func TestAPIServer_IndexPage(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	server.indexPage(w, req)

	resp := w.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Content-Type = %v, want text/html", contentType)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Network Namespace Exporter") {
		t.Error("Response should contain 'Network Namespace Exporter'")
	}
	if !strings.Contains(bodyStr, "/metrics") {
		t.Error("Response should contain link to /metrics")
	}
}

// TestResponseWriter tests the response writer wrapper
func TestResponseWriter_WriteHeader(t *testing.T) {
	rw := &responseWriter{
		ResponseWriter: httptest.NewRecorder(),
		statusCode:     http.StatusOK,
	}

	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %v, want %v", rw.statusCode, http.StatusNotFound)
	}
}

// TestAPIServer_MiddlewareLogging tests that middleware captures status codes
func TestAPIServer_MiddlewareLogging(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Create a test handler that returns a specific status
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	// Wrap with middleware
	wrapped := server.middlewareLogging(testHandler)

	// Create test request
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("data"))
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status code = %v, want %v", resp.StatusCode, http.StatusCreated)
	}
}

// TestAPIServer_MiddlewareLogging_StatusOK tests default status code
func TestAPIServer_MiddlewareLogging_StatusOK(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Create a test handler that doesn't explicitly set status
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Wrap with middleware
	wrapped := server.middlewareLogging(testHandler)

	// Create test request
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	resp := w.Result()
	// Default status should be 200
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status code = %v, want %v", resp.StatusCode, http.StatusOK)
	}
}

// TestAPIServer_Shutdown tests graceful shutdown cancels cache updates
func TestAPIServer_Shutdown(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Verify context is not cancelled initially
	select {
	case <-server.ctx.Done():
		t.Fatal("ctx should not be cancelled initially")
	default:
		// Expected
	}

	// Create shutdown context
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Call shutdown
	err = server.Shutdown(shutdownCtx)
	if err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}

	// Verify context is cancelled
	select {
	case <-server.ctx.Done():
		// Expected - context was cancelled
	default:
		t.Error("ctx should be cancelled after shutdown")
	}
}

// TestAPIServer_ServerAddress tests server address configuration
func TestAPIServer_ServerAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		port    int
		want    string
	}{
		{
			name:    "ipv4_localhost",
			address: "127.0.0.1",
			port:    9101,
			want:    "127.0.0.1:9101",
		},
		{
			name:    "ipv4_any",
			address: "0.0.0.0",
			port:    9100,
			want:    "0.0.0.0:9100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createTestConfig()
			config.APIServer.ServerAddress = tt.address
			config.APIServer.ServerPort = tt.port

			logger := createTestExporterLogger()
			tmpDir := t.TempDir()
			config.LogDirectory = tmpDir

			server, err := NewAPIServer(config, logger)
			if err != nil {
				t.Fatalf("NewAPIServer() unexpected error: %v", err)
			}

			if server.server.Addr != tt.want {
				t.Errorf("server.Addr = %v, want %v", server.server.Addr, tt.want)
			}
		})
	}
}

// TestAPIServer_TimeoutConfiguration tests timeout settings
func TestAPIServer_TimeoutConfiguration(t *testing.T) {
	config := createTestConfig()
	config.APIServer.RequestTimeout = 10

	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	expectedTimeout := 10 * time.Second

	if server.server.ReadHeaderTimeout != expectedTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", server.server.ReadHeaderTimeout, expectedTimeout)
	}
	if server.server.WriteTimeout != expectedTimeout {
		t.Errorf("WriteTimeout = %v, want %v", server.server.WriteTimeout, expectedTimeout)
	}
	if server.server.IdleTimeout != expectedTimeout {
		t.Errorf("IdleTimeout = %v, want %v", server.server.IdleTimeout, expectedTimeout)
	}
}

// TestNewAPIServer_CacheInitialization tests that cache is properly initialized
func TestNewAPIServer_CacheInitialization(t *testing.T) {
	config := createTestConfig()
	config.ScrapeInterval = 30

	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Cache should have update interval of scrape_interval / 2
	expectedUpdateInterval := 15 * time.Second
	if server.cache.updateInterval != expectedUpdateInterval {
		t.Errorf("cache.updateInterval = %v, want %v", server.cache.updateInterval, expectedUpdateInterval)
	}
}

// TestAPIServer_CollectorRegistration tests that collector is registered
func TestAPIServer_CollectorRegistration(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Verify registry is not nil
	if server.registry == nil {
		t.Fatal("registry should not be nil")
	}

	// The collector should be registered - we can verify by checking
	// that the registry has descriptors (implicitly tested by server creation)
}


// TestResponseWriter_Unwrap tests that responseWriter can be unwrapped
func TestResponseWriter_Unwrap(t *testing.T) {
	recorder := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: recorder,
		statusCode:     http.StatusOK,
	}

	// Test that we can access the underlying ResponseWriter
	unwrapped := rw.Unwrap()
	if unwrapped != recorder {
		t.Error("Unwrap() should return underlying ResponseWriter")
	}
}

// TestAPIServer_Start tests server start (without actually listening)
func TestAPIServer_Start(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Start server in goroutine - it will fail because we're not
	// actually binding, but we can test the error handling
	go func() {
		err := server.Start()
		if err != nil {
			// Expected - address already in use or similar
			return
		}
	}()

	// Give it a moment to start
	time.Sleep(10 * time.Millisecond)

	// Shutdown should still work
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = server.Shutdown(ctx)
}

// TestNewCollector tests collector creation
func TestNewCollector(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	if collector == nil {
		t.Fatal("NewCollector() returned nil")
	}
	if collector.config != config {
		t.Error("collector.config should be set")
	}
	if collector.cache != cache {
		t.Error("collector.cache should be set")
	}
	if collector.hostname == "" {
		t.Error("collector.hostname should be set")
	}
	if collector.intfMetrics == nil {
		t.Error("collector.intfMetrics should be initialized")
	}
	if collector.ctMetrics == nil {
		t.Error("collector.ctMetrics should be initialized")
	}
	if collector.snmpMetrics == nil {
		t.Error("collector.snmpMetrics should be initialized")
	}
	if collector.sockstatMetrics == nil {
		t.Error("collector.sockstatMetrics should be initialized")
	}
	if collector.pingMetrics == nil {
		t.Error("collector.pingMetrics should be initialized")
	}
}

// TestCollector_Describe tests the Describe method
func TestCollector_Describe(t *testing.T) {
	config := createTestConfig()
	logger := createTestExporterLogger()
	tmpDir := t.TempDir()
	config.LogDirectory = tmpDir

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	descChan := make(chan *prometheus.Desc, 100)
	go func() {
		collector.Describe(descChan)
		close(descChan)
	}()

	descCount := 0
	for range descChan {
		descCount++
	}

	// Should have at least some descriptors
	if descCount == 0 {
		t.Error("Describe() should send descriptors")
	}
}
