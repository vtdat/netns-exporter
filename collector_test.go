package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetType tests namespace type detection
func TestGetType(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      string
	}{
		{
			name:      "qrouter_namespace",
			namespace: "qrouter-1",
			want:      "qrouter",
		},
		{
			name:      "qrouter_namespace_double_digit",
			namespace: "qrouter-10",
			want:      "qrouter",
		},
		{
			name:      "qdhcp_namespace",
			namespace: "qdhcp-1",
			want:      "qdhcp",
		},
		{
			name:      "qdhcp_namespace_double_digit",
			namespace: "qdhcp-10",
			want:      "qdhcp",
		},
		{
			name:      "other_namespace",
			namespace: "custom-ns",
			want:      "other",
		},
		{
			name:      "empty_namespace",
			namespace: "",
			want:      "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getType(tt.namespace)
			if got != tt.want {
				t.Errorf("getType(%q) = %v, want %v", tt.namespace, got, tt.want)
			}
		})
	}
}

// TestCollector_readFloatFromFile tests reading float values from files
func TestCollector_readFloatFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	collector := &Collector{logger: logger}

	// Test valid integer
	intFile := filepath.Join(tmpDir, "int.txt")
	if err := os.WriteFile(intFile, []byte("42\n"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	val := collector.readFloatFromFile(intFile)
	if val != 42.0 {
		t.Errorf("readFloatFromFile(int) = %v, want 42.0", val)
	}

	// Test valid float
	floatFile := filepath.Join(tmpDir, "float.txt")
	if err := os.WriteFile(floatFile, []byte("3.14159\n"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	val = collector.readFloatFromFile(floatFile)
	if val != 3.14159 {
		t.Errorf("readFloatFromFile(float) = %v, want 3.14159", val)
	}

	// Test with whitespace
	whitespaceFile := filepath.Join(tmpDir, "whitespace.txt")
	if err := os.WriteFile(whitespaceFile, []byte("  100.5  \n"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	val = collector.readFloatFromFile(whitespaceFile)
	if val != 100.5 {
		t.Errorf("readFloatFromFile(whitespace) = %v, want 100.5", val)
	}
}

// TestCollector_readFloatFromFile_InvalidFile tests error handling for missing files
func TestCollector_readFloatFromFile_InvalidFile(t *testing.T) {
	logger := createTestLogger()
	logger.SetOutput(io.Discard) // Suppress error logs
	collector := &Collector{logger: logger}

	val := collector.readFloatFromFile("/nonexistent/file/path")
	if val != -1 {
		t.Errorf("readFloatFromFile(nonexistent) = %v, want -1", val)
	}
}

// TestCollector_readFloatFromFile_InvalidFormat tests error handling for non-numeric content
func TestCollector_readFloatFromFile_InvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	logger.SetOutput(io.Discard) // Suppress warning logs
	collector := &Collector{logger: logger}

	// Test invalid content
	invalidFile := filepath.Join(tmpDir, "invalid.txt")
	if err := os.WriteFile(invalidFile, []byte("not a number\n"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	val := collector.readFloatFromFile(invalidFile)
	if val != -1 {
		t.Errorf("readFloatFromFile(invalid) = %v, want -1", val)
	}

	// Test empty file
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(emptyFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	val = collector.readFloatFromFile(emptyFile)
	if val != -1 {
		t.Errorf("readFloatFromFile(empty) = %v, want -1", val)
	}
}

// TestCollector_isIPInInternalCIDRs tests CIDR matching
func TestCollector_isIPInInternalCIDRs(t *testing.T) {
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		InternalCIDRs: []string{
			"10.0.0.0/8",
			"192.168.0.0/16",
			"172.16.0.0/12",
		},
	}
	// Pre-parse CIDRs
	config.parseCIDRs()

	collector := &Collector{
		logger: logger,
		config: config,
	}

	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{
			name: "ip_in_10_0_0_0_8",
			ip:   "10.1.2.3",
			want: true,
		},
		{
			name: "ip_in_192_168_0_0_16",
			ip:   "192.168.1.1",
			want: true,
		},
		{
			name: "ip_in_172_16_0_0_12",
			ip:   "172.16.5.5",
			want: true,
		},
		{
			name: "ip_not_in_cidrs",
			ip:   "8.8.8.8",
			want: false,
		},
		{
			name: "ip_on_boundary",
			ip:   "10.255.255.255",
			want: true,
		},
		{
			name: "ip_just_outside_range",
			ip:   "11.0.0.0",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collector.isIPInInternalCIDRs(tt.ip)
			if got != tt.want {
				t.Errorf("isIPInInternalCIDRs(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

// TestCollector_isIPInInternalCIDRs_InvalidIP tests handling of invalid IP strings
func TestCollector_isIPInInternalCIDRs_InvalidIP(t *testing.T) {
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		InternalCIDRs: []string{"10.0.0.0/8"},
	}
	config.parseCIDRs()

	collector := &Collector{
		logger: logger,
		config: config,
	}

	// Invalid IP should return false
	got := collector.isIPInInternalCIDRs("not-an-ip")
	if got != false {
		t.Errorf("isIPInInternalCIDRs(invalid) = %v, want false", got)
	}

	// Empty string should return false
	got = collector.isIPInInternalCIDRs("")
	if got != false {
		t.Errorf("isIPInInternalCIDRs(empty) = %v, want false", got)
	}
}

// TestCollector_ensurePingLogDirectory tests log directory creation
func TestCollector_ensurePingLogDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	namespace := "qrouter-1"
	err := collector.ensurePingLogDirectory(namespace)
	if err != nil {
		t.Fatalf("ensurePingLogDirectory() unexpected error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, namespace)
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("Directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected path should be a directory")
	}
}

// TestCollector_ensurePingLogDirectory_NestedNamespace tests nested namespace paths
func TestCollector_ensurePingLogDirectory_NestedNamespace(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	// Test with namespace that looks like a path
	namespace := "qrouter-1/sub-ns"
	err := collector.ensurePingLogDirectory(namespace)
	if err != nil {
		t.Fatalf("ensurePingLogDirectory() unexpected error: %v", err)
	}

	expectedPath := filepath.Join(tmpDir, namespace)
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("Directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected path should be a directory")
	}
}

// TestCollector_getPingLogPath tests ping log path construction
func TestCollector_getPingLogPath(t *testing.T) {
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: "/var/log/netns-exporter",
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	namespace := "qrouter-1"
	path := collector.getPingLogPath(namespace)

	expected := filepath.Join("/var/log/netns-exporter", "qrouter-1", "ping_log")
	if path != expected {
		t.Errorf("getPingLogPath() = %v, want %v", path, expected)
	}
}

// TestCollector_rotatePingLog tests log rotation
func TestCollector_rotatePingLog(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	// Create a ping log with more than maxLines
	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write 10 lines
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		content.WriteString("2024-01-01T00:00:00Z success 10.0\n")
	}
	if err := os.WriteFile(logPath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	// Rotate keeping only 5 lines
	collector.rotatePingLog(logPath, 5)

	// Read back and verify
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Errorf("rotatePingLog() resulted in %d lines, want 5", len(lines))
	}

	// Verify we kept the last 5 lines
	for i, line := range lines {
		expectedLine := "2024-01-01T00:00:00Z success 10.0"
		if line != expectedLine {
			t.Errorf("line %d = %v, want %v", i, line, expectedLine)
		}
	}
}

// TestCollector_rotatePingLog_NoRotationNeeded tests when file is under limit
func TestCollector_rotatePingLog_NoRotationNeeded(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write 3 lines
	content := "2024-01-01T00:00:00Z success 10.0\n" +
		"2024-01-01T00:00:01Z success 11.0\n" +
		"2024-01-01T00:00:02Z success 12.0\n"

	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	// Rotate with maxLines=10 (more than current)
	collector.rotatePingLog(logPath, 10)

	// File should be unchanged
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if string(data) != content {
		t.Errorf("rotatePingLog() modified file when it shouldn't have")
	}
}

// TestCollector_parsePingLogResults tests parsing ping results
func TestCollector_parsePingLogResults(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory:   tmpDir,
		ScrapeInterval: 60,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write ping results
	content := "2024-01-01T00:00:00Z success 10.5\n" +
		"2024-01-01T00:00:01Z success 11.0\n" +
		"2024-01-01T00:00:02Z failure 0\n" +
		"2024-01-01T00:00:03Z success 12.5\n"

	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	successRate, avgLatency, err := collector.parsePingLogResults(logPath)
	if err != nil {
		t.Fatalf("parsePingLogResults() unexpected error: %v", err)
	}

	// 3 successes out of 4 = 75%
	expectedSuccessRate := 75.0
	if successRate != expectedSuccessRate {
		t.Errorf("successRate = %v, want %v", successRate, expectedSuccessRate)
	}

	// Average latency of successful pings: (10.5 + 11.0 + 12.5) / 3 = 11.333...
	expectedAvgLatency := 11.333333333333334
	if avgLatency != expectedAvgLatency {
		t.Errorf("avgLatency = %v, want %v", avgLatency, expectedAvgLatency)
	}
}

// TestCollector_parsePingLogResults_EmptyFile tests handling of empty ping log
func TestCollector_parsePingLogResults_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	logger.SetOutput(io.Discard)
	config := &NetnsExporterConfig{
		LogDirectory:   tmpDir,
		ScrapeInterval: 60,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write empty file
	if err := os.WriteFile(logPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	_, _, err := collector.parsePingLogResults(logPath)
	if err == nil {
		t.Fatal("parsePingLogResults() expected error for empty file, got nil")
	}
}

// TestCollector_parsePingLogResults_AllFailures tests all failures case
func TestCollector_parsePingLogResults_AllFailures(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory:   tmpDir,
		ScrapeInterval: 60,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Write all failures
	content := "2024-01-01T00:00:00Z failure 0\n" +
		"2024-01-01T00:00:01Z failure 0\n" +
		"2024-01-01T00:00:02Z failure 0\n"

	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write log file: %v", err)
	}

	successRate, avgLatency, err := collector.parsePingLogResults(logPath)
	if err != nil {
		t.Fatalf("parsePingLogResults() unexpected error: %v", err)
	}

	if successRate != 0 {
		t.Errorf("successRate = %v, want 0", successRate)
	}

	if avgLatency != 0 {
		t.Errorf("avgLatency = %v, want 0", avgLatency)
	}
}

// TestCollector_extractLatencyFromPingOutput tests latency extraction
func TestCollector_extractLatencyFromPingOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   float64
	}{
		{
			name:   "standard_ping_output",
			output: "64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=10.5 ms",
			want:   10.5,
		},
		{
			name:   "integer_latency",
			output: "64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=5 ms",
			want:   5.0,
		},
		{
			name:   "high_precision_latency",
			output: "64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=10.123 ms",
			want:   10.123,
		},
		{
			name:   "multiline_output",
			output: "PING 8.8.8.8 (8.8.8.8) 56(84) bytes of data.\n" +
				"64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=15.7 ms\n" +
				"64 bytes from 8.8.8.8: icmp_seq=2 ttl=117 time=16.2 ms",
			want: 15.7, // First match
		},
		{
			name:   "no_time_found",
			output: "Destination Host Unreachable",
			want:   0,
		},
		{
			name:   "invalid_time_format",
			output: "64 bytes from 8.8.8.8: icmp_seq=1 ttl=117 time=abc ms",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLatencyFromPingOutput(tt.output)
			if got != tt.want {
				t.Errorf("extractLatencyFromPingOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCollector_appendPingResult tests appending ping results to log
func TestCollector_appendPingResult(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	err := collector.appendPingResult(logPath, "success 10.5")
	if err != nil {
		t.Fatalf("appendPingResult() unexpected error: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "success 10.5") {
		t.Errorf("Log should contain 'success 10.5', got: %s", content)
	}
	// Verify timestamp format (RFC3339 timestamp ends with Z or timezone offset)
	if !strings.Contains(content, "T") || !strings.Contains(content, ":") {
		t.Error("Log should contain timestamp")
	}
}

// TestCollector_appendPingResult_CreatesDirectory tests that directory is created
func TestCollector_appendPingResult_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	err := collector.appendPingResult(logPath, "success 5.0")
	if err != nil {
		t.Fatalf("appendPingResult() unexpected error: %v", err)
	}

	// Verify file was created
	_, err = os.Stat(logPath)
	if err != nil {
		t.Errorf("Log file should exist: %v", err)
	}
}

// TestCollector_appendPingResult_AppendsMultipleLines tests appending multiple results
func TestCollector_appendPingResult_AppendsMultipleLines(t *testing.T) {
	tmpDir := t.TempDir()
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		LogDirectory: tmpDir,
	}
	collector := &Collector{
		logger: logger,
		config: config,
	}

	logPath := filepath.Join(tmpDir, "qrouter-1", "ping_log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Append multiple results
	err := collector.appendPingResult(logPath, "success 10.0")
	if err != nil {
		t.Fatalf("appendPingResult() unexpected error: %v", err)
	}

	err = collector.appendPingResult(logPath, "failure 0")
	if err != nil {
		t.Fatalf("appendPingResult() unexpected error: %v", err)
	}

	err = collector.appendPingResult(logPath, "success 15.5")
	if err != nil {
		t.Fatalf("appendPingResult() unexpected error: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("Expected 3 lines, got %d", len(lines))
	}
}

// TestIsIPInInternalCIDRs_EmptyCIDRs tests behavior with empty CIDR list
func TestIsIPInInternalCIDRs_EmptyCIDRs(t *testing.T) {
	logger := createTestLogger()
	config := &NetnsExporterConfig{
		InternalCIDRs: []string{},
	}
	config.parseCIDRs()

	collector := &Collector{
		logger: logger,
		config: config,
	}

	// Any IP should be "not internal" when no CIDRs are defined
	got := collector.isIPInInternalCIDRs("10.0.0.1")
	if got != false {
		t.Errorf("isIPInInternalCIDRs() with empty CIDRs = %v, want false", got)
	}
}

// TestGetType_CaseSensitivity tests that type detection is case-sensitive
func TestGetType_CaseSensitivity(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      string
	}{
		{
			name:      "uppercase_qrouter",
			namespace: "QROUTER-1",
			want:      "other",
		},
		{
			name:      "mixed_case_qrouter",
			namespace: "Qrouter-1",
			want:      "other",
		},
		{
			name:      "uppercase_qdhcp",
			namespace: "QDHCP-1",
			want:      "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getType(tt.namespace)
			if got != tt.want {
				t.Errorf("getType(%q) = %v, want %v", tt.namespace, got, tt.want)
			}
		})
	}
}
