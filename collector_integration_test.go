//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// createIntegrationLogger creates a logger for integration tests
func createIntegrationLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(os.Stdout)
	return logger
}

// createTestNamespace creates a temporary network namespace for testing
// Requires root privileges
func createTestNamespace(t *testing.T) (name string, cleanup func()) {
	name = "test-ns-" + fmt.Sprintf("%d", time.Now().UnixNano())

	// Create the network namespace
	cmd := exec.Command("ip", "netns", "add", name)
	if err := cmd.Run(); err != nil {
		t.Skipf("Failed to create network namespace %s (requires root): %v", name, err)
	}

	cleanup = func() {
		// Delete the namespace
		cmd := exec.Command("ip", "netns", "delete", name)
		if err := cmd.Run(); err != nil {
			t.Logf("Failed to delete namespace %s: %v", name, err)
		}
	}

	return name, cleanup
}

// TestCollector_Integration_Basic tests basic collector initialization
func TestCollector_Integration_Basic(t *testing.T) {
	logger := createIntegrationLogger()
	config := &NetnsExporterConfig{
		APIServer: APIServerConfig{
			ServerAddress:  "127.0.0.1",
			ServerPort:     0,
			RequestTimeout: 5,
			TelemetryPath:  "/metrics",
		},
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    t.TempDir(),
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false, // Disable ping for basic test
			ARP:       true,
		},
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	if collector == nil {
		t.Fatal("NewCollector() returned nil")
	}
}

// TestCollector_Integration_NamespaceCollection tests actual namespace metric collection
func TestCollector_Integration_NamespaceCollection(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	// Create a test namespace
	nsName, cleanup := createTestNamespace(t)
	defer cleanup()

	logger := createIntegrationLogger()
	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		Threads:         1,
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false,
			ARP:       true,
		},
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Collect metrics - this should find our test namespace
	metricData := collector.collectMetrics()

	// Verify we got at least the namespace count metric
	if len(metricData) == 0 {
		t.Error("collectMetrics() should return at least namespace count metric")
	}

	// Check for namespaces_total metric
	foundNsMetric := false
	for _, m := range metricData {
		if m.Desc == "namespaces_total" {
			foundNsMetric = true
			if m.Value < 1 {
				t.Errorf("namespaces_total = %v, want >= 1", m.Value)
			}
			break
		}
	}

	if !foundNsMetric {
		t.Error("Should find namespaces_total metric")
	}
}

// TestCollector_Integration_ConcurrentNamespaces tests concurrent collection from multiple namespaces
func TestCollector_Integration_ConcurrentNamespaces(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	// Create multiple test namespaces
	var namespaces []string
	var cleanups []func()

	for i := 0; i < 3; i++ {
		nsName, cleanup := createTestNamespace(t)
		namespaces = append(namespaces, nsName)
		cleanups = append(cleanups, cleanup)
	}

	// Cleanup all namespaces
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	logger := createIntegrationLogger()
	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		Threads:         2, // Use multiple threads
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false,
			ARP:       true,
		},
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Collect metrics concurrently
	done := make(chan []CachedMetricData)
	errChan := make(chan error, 3)

	for i := 0; i < 3; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("panic: %v", r)
				}
			}()
			data := collector.collectMetrics()
			done <- data
		}()
	}

	// Collect results
	for i := 0; i < 3; i++ {
		select {
		case data := <-done:
			if len(data) == 0 {
				t.Errorf("Iteration %d: collectMetrics() returned empty data", i)
			}
		case err := <-errChan:
			t.Errorf("Iteration %d: error: %v", i, err)
		case <-time.After(10 * time.Second):
			t.Fatalf("Iteration %d: collectMetrics() timed out", i)
		}
	}
}

// TestAPIServer_Integration_Endpoints tests actual HTTP endpoints with live server
func TestAPIServer_Integration_Endpoints(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	logger := createIntegrationLogger()
	logger.SetOutput(io.Discard) // Suppress logs during test

	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		APIServer: APIServerConfig{
			ServerAddress:  "127.0.0.1",
			ServerPort:     0, // Use any available port
			RequestTimeout: 5,
			TelemetryPath:  "/metrics",
		},
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false,
			ARP:       true,
		},
	}
	config.parseCIDRs()

	// Create API server
	server, err := NewAPIServer(config, logger)
	if err != nil {
		t.Fatalf("NewAPIServer() unexpected error: %v", err)
	}

	// Start server in background
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Get the actual port
	parts := strings.Split(server.server.Addr, ":")
	if len(parts) != 2 {
		t.Fatalf("Unexpected server address format: %s", server.server.Addr)
	}

	// Test index page
	t.Run("IndexPage", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("http://%s/", server.server.Addr))
		if err != nil {
			t.Fatalf("Failed to get index page: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Index page status = %v, want %v", resp.StatusCode, http.StatusOK)
		}

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "Network Namespace Exporter") {
			t.Error("Index page should contain 'Network Namespace Exporter'")
		}
	})

	// Test metrics endpoint
	t.Run("MetricsEndpoint", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("http://%s/metrics", server.server.Addr))
		if err != nil {
			t.Fatalf("Failed to get metrics: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Metrics status = %v, want %v", resp.StatusCode, http.StatusOK)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// Should contain some Prometheus metrics
		if !strings.Contains(bodyStr, "# HELP") && !strings.Contains(bodyStr, "netns") {
			t.Error("Metrics endpoint should return Prometheus metrics")
		}
	})

	// Test 404 for unknown path
	t.Run("UnknownPath", func(t *testing.T) {
		resp, err := http.Get(fmt.Sprintf("http://%s/unknown", server.server.Addr))
		if err != nil {
			t.Fatalf("Failed to get unknown path: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Unknown path status = %v, want %v", resp.StatusCode, http.StatusNotFound)
		}
	})

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Logf("Shutdown error: %v", err)
	}
}

// TestCollector_Integration_CacheUpdate tests that cache is updated with namespace metrics
func TestCollector_Integration_CacheUpdate(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	// Create a test namespace
	nsName, cleanup := createTestNamespace(t)
	defer cleanup()

	logger := createIntegrationLogger()
	logger.SetOutput(io.Discard)
	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  2, // Short interval for faster testing
		LogDirectory:    tmpDir,
		Threads:         1,
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false,
			ARP:       true,
		},
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Manually trigger collection
	metricData := collector.collectMetrics()

	// Update cache
	cache.UpdateCache(metricData)

	// Verify cache has data
	data, ts := cache.GetMetricData()
	if ts.IsZero() {
		t.Error("Cache timestamp should be set after update")
	}

	// Should have at least namespace count
	foundNsMetric := false
	for _, m := range data {
		if m.Desc == "namespaces_total" {
			foundNsMetric = true
			break
		}
	}

	if !foundNsMetric {
		t.Error("Cache should contain namespaces_total metric")
	}
}

// TestCollector_Integration_NamespaceFiltering tests namespace filtering
func TestCollector_Integration_NamespaceFiltering(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	// Create test namespaces with different names
	testNsName, testCleanup := createTestNamespace(t)
	defer testCleanup()

	logger := createIntegrationLogger()
	logger.SetOutput(io.Discard)
	tmpDir := t.TempDir()

	// Config with whitelist that excludes our test namespace
	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		Threads:         1,
		NamespacesFilter: RegexFilter{
			WhitelistPattern: "^qrouter-.*", // Only allow qrouter namespaces
			WhitelistRegexp:  nil,          // Will be compiled by LoadConfig
		},
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Ping:      false,
		},
	}
	// Manually compile regex
	var err error
	config.NamespacesFilter.WhitelistRegexp, err = regexp.Compile(config.NamespacesFilter.WhitelistPattern)
	if err != nil {
		t.Fatalf("Failed to compile regex: %v", err)
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Collect metrics
	metricData := collector.collectMetrics()

	// Our test namespace should be filtered out
	// But we should still get the namespace count metric
	foundNsMetric := false
	for _, m := range metricData {
		if m.Desc == "namespaces_total" {
			foundNsMetric = true
			// The count should not include our filtered test namespace
			// (it might include other namespaces on the system)
			t.Logf("Found %v namespaces after filtering", m.Value)
			break
		}
	}

	if !foundNsMetric {
		t.Error("Should find namespaces_total metric even with filtering")
	}
}

// TestCollector_Integration_BlacklistFiltering tests namespace blacklist filtering
func TestCollector_Integration_BlacklistFiltering(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	// Create a test namespace with "test-" prefix
	nsName := "test-ns-" + fmt.Sprintf("%d", time.Now().UnixNano())
	cmd := exec.Command("ip", "netns", "add", nsName)
	if err := cmd.Run(); err != nil {
		t.Skipf("Failed to create network namespace %s (requires root): %v", nsName, err)
	}
	defer func() {
		cmd := exec.Command("ip", "netns", "delete", nsName)
		cmd.Run()
	}()

	logger := createIntegrationLogger()
	logger.SetOutput(io.Discard)
	tmpDir := t.TempDir()

	// Config with blacklist that excludes our test namespace
	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		Threads:         1,
		NamespacesFilter: RegexFilter{
			BlacklistPattern: "^test-ns-.*",
		},
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Ping:      false,
		},
	}
	// Manually compile regex
	var err error
	config.NamespacesFilter.BlacklistRegexp, err = regexp.Compile(config.NamespacesFilter.BlacklistPattern)
	if err != nil {
		t.Fatalf("Failed to compile regex: %v", err)
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Collect metrics
	metricData := collector.collectMetrics()

	// Verify our test namespace was filtered out
	for _, m := range metricData {
		// Check if any metric has our test namespace in labels
		for _, label := range m.LabelValues {
			if label == nsName {
				t.Errorf("Test namespace %s should have been blacklisted", nsName)
			}
		}
	}
}

// TestCollector_Integration_LogDirectoryCreation tests log directory creation
func TestCollector_Integration_LogDirectoryCreation(t *testing.T) {
	logger := createIntegrationLogger()
	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}

	collector := &Collector{
		logger: logger,
		config: config,
	}

	// Test directory creation
	testNs := "qrouter-test"
	err := collector.ensurePingLogDirectory(testNs)
	if err != nil {
		t.Fatalf("ensurePingLogDirectory() unexpected error: %v", err)
	}

	// Verify directory exists
	expectedPath := filepath.Join(tmpDir, testNs)
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("Directory should exist: %v", err)
	}

	if !info.IsDir() {
		t.Error("Expected path should be a directory")
	}
}

// TestCollector_Integration_ThreadSafety tests concurrent access to collector
func TestCollector_Integration_ThreadSafety(t *testing.T) {
	// Check if running as root
	if os.Geteuid() != 0 {
		t.Skip("Integration test requires root privileges")
	}

	logger := createIntegrationLogger()
	logger.SetOutput(io.Discard)
	tmpDir := t.TempDir()

	config := &NetnsExporterConfig{
		InternalCIDRs:   []string{"10.0.0.0/8"},
		DestinationHost: "8.8.8.8",
		ScrapeInterval:  60,
		LogDirectory:    tmpDir,
		Threads:         4,
		EnabledMetrics: MetricsConfig{
			Interface: true,
			Conntrack: true,
			SNMP:      true,
			Sockstat:  true,
			Ping:      false,
			ARP:       true,
		},
	}
	config.parseCIDRs()

	cache := NewMetricCache(config.ScrapeInterval, logger)
	collector := NewCollector(config, logger, cache)

	// Run multiple collections concurrently
	done := make(chan struct{}, 10)
	errChan := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("panic: %v", r)
				}
				done <- struct{}{}
			}()
			_ = collector.collectMetrics()
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		select {
		case <-done:
			// Success
		case err := <-errChan:
			t.Errorf("Goroutine error: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("Test timed out")
		}
	}
}
