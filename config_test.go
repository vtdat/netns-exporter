package main

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	yaml "gopkg.in/yaml.v2"
)

// TestLoadConfig_ValidConfig tests successful loading of a valid configuration file
func TestLoadConfig_ValidConfig(t *testing.T) {
	configPath := filepath.Join("testdata", "valid_config.yaml")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error: %v", err)
	}

	// Verify basic config values
	if cfg.APIServer.ServerAddress != "0.0.0.0" {
		t.Errorf("ServerAddress = %v, want %v", cfg.APIServer.ServerAddress, "0.0.0.0")
	}
	if cfg.APIServer.ServerPort != 9101 {
		t.Errorf("ServerPort = %v, want %v", cfg.APIServer.ServerPort, 9101)
	}
	if cfg.Threads != 4 {
		t.Errorf("Threads = %v, want %v", cfg.Threads, 4)
	}
	if cfg.ScrapeInterval != 60 {
		t.Errorf("ScrapeInterval = %v, want %v", cfg.ScrapeInterval, 60)
	}
	if cfg.LogDirectory != "/var/log/netns-exporter" {
		t.Errorf("LogDirectory = %v, want %v", cfg.LogDirectory, "/var/log/netns-exporter")
	}
	if cfg.DestinationHost != "8.8.8.8" {
		t.Errorf("DestinationHost = %v, want %v", cfg.DestinationHost, "8.8.8.8")
	}

	// Verify CIDRs were parsed
	if len(cfg.parsedCIDRs) != 2 {
		t.Errorf("parsedCIDRs length = %v, want %v", len(cfg.parsedCIDRs), 2)
	}

	// Verify interface metrics
	expectedMetrics := []string{"rx_bytes", "tx_bytes", "rx_packets", "tx_packets", "rx_errors", "tx_errors", "rx_dropped", "tx_dropped"}
	if len(cfg.InterfaceMetrics) != len(expectedMetrics) {
		t.Errorf("InterfaceMetrics length = %v, want %v", len(cfg.InterfaceMetrics), len(expectedMetrics))
	}
}

// TestLoadConfig_FileNotFound tests error handling when config file doesn't exist
func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("nonexistent_config.yaml")
	if err == nil {
		t.Fatal("LoadConfig() expected error for missing file, got nil")
	}
	if got, want := err.Error(), "failed to read config file"; got[:len(got)-len(": open nonexistent_config.yaml: no such file or directory")] != want {
		t.Errorf("LoadConfig() error = %v, want prefix %v", err, want)
	}
}

// TestLoadConfig_InvalidYAML tests error handling for malformed YAML
func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "invalid.yaml")
	content := []byte(`
api_server:
  server_address: [invalid yaml
  server_port: not a number
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err := LoadConfig(tmpFile)
	if err == nil {
		t.Fatal("LoadConfig() expected error for invalid YAML, got nil")
	}
}

// TestLoadConfig_InvalidCIDR tests error handling for invalid CIDR notation
func TestLoadConfig_InvalidCIDR(t *testing.T) {
	configPath := filepath.Join("testdata", "invalid_cidr_config.yaml")

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig() expected error for invalid CIDR, got nil")
	}
	if got, want := err.Error(), "config validation error"; got[:len(got)-len(": invalid CIDR notation in internal_cidrs: invalid-cidr-notation")] != want {
		t.Errorf("LoadConfig() error = %v, want prefix %v", err, want)
	}
}

// TestLoadConfig_InvalidRegex tests error handling for invalid regex patterns
func TestLoadConfig_InvalidRegex(t *testing.T) {
	configPath := filepath.Join("testdata", "invalid_regex_config.yaml")

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig() expected error for invalid regex, got nil")
	}
	// Error should mention invalid regex pattern
	if got := err.Error(); got == "" {
		t.Errorf("LoadConfig() error should not be empty")
	}
}

// TestLoadConfig_MissingDestinationHost tests validation when ping is enabled but no destination
func TestLoadConfig_MissingDestinationHost(t *testing.T) {
	configPath := filepath.Join("testdata", "missing_destination_config.yaml")

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig() expected error for missing destination_host, got nil")
	}
}

// TestLoadConfig_Defaults tests that default values are applied correctly
func TestLoadConfig_Defaults(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "defaults.yaml")
	content := []byte(`
api_server:
  server_address: 127.0.0.1
  server_port: 9100
  telemetry_path: /metrics
internal_cidrs:
  - 10.0.0.0/8
destination_host: 1.1.1.1
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error: %v", err)
	}

	// Threads should default to NumCPU if not specified
	if cfg.Threads <= 0 {
		t.Errorf("Threads = %v, want positive value", cfg.Threads)
	}

	// ScrapeInterval should default to 60 if not specified
	if cfg.ScrapeInterval != 60 {
		t.Errorf("ScrapeInterval = %v, want %v", cfg.ScrapeInterval, 60)
	}

	// LogDirectory should default to /var/log/netns-exporter
	if cfg.LogDirectory != "/var/log/netns-exporter" {
		t.Errorf("LogDirectory = %v, want %v", cfg.LogDirectory, "/var/log/netns-exporter")
	}
}

// TestLoadConfig_MetricDefaults tests that all metrics are enabled by default
func TestLoadConfig_MetricDefaults(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "metrics.yaml")
	content := []byte(`
api_server:
  server_address: 127.0.0.1
  server_port: 9100
  telemetry_path: /metrics
internal_cidrs:
  - 10.0.0.0/8
destination_host: 1.1.1.1
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error: %v", err)
	}

	// All metrics should be enabled by default
	if !cfg.EnabledMetrics.Interface {
		t.Error("EnabledMetrics.Interface should be true by default")
	}
	if !cfg.EnabledMetrics.Conntrack {
		t.Error("EnabledMetrics.Conntrack should be true by default")
	}
	if !cfg.EnabledMetrics.SNMP {
		t.Error("EnabledMetrics.SNMP should be true by default")
	}
	if !cfg.EnabledMetrics.Sockstat {
		t.Error("EnabledMetrics.Sockstat should be true by default")
	}
	if !cfg.EnabledMetrics.Ping {
		t.Error("EnabledMetrics.Ping should be true by default")
	}
	if !cfg.EnabledMetrics.ARP {
		t.Error("EnabledMetrics.ARP should be true by default")
	}
}

// TestRegexFilter_IsAllowed tests the blacklist/whitelist filter logic
func TestRegexFilter_IsAllowed(t *testing.T) {
	tests := []struct {
		name             string
		blacklistPattern string
		whitelistPattern string
		testName         string
		want             bool
	}{
		// Blacklist tests
		{
			name:             "blacklist_match_denied",
			blacklistPattern: "^test-",
			whitelistPattern: "",
			testName:         "test-namespace",
			want:             false,
		},
		{
			name:             "blacklist_no_match_allowed",
			blacklistPattern: "^test-",
			whitelistPattern: "",
			testName:         "prod-namespace",
			want:             true,
		},
		// Whitelist tests
		{
			name:             "whitelist_match_allowed",
			blacklistPattern: "",
			whitelistPattern: "^qrouter-\\d$",
			testName:         "qrouter-1",
			want:             true,
		},
		{
			name:             "whitelist_no_match_denied",
			blacklistPattern: "",
			whitelistPattern: "^qrouter-\\d$",
			testName:         "qdhcp-1",
			want:             false,
		},
		// Combined tests (blacklist has priority)
		{
			name:             "blacklist_priority_over_whitelist",
			blacklistPattern: "^test-",
			whitelistPattern: "^test-\\d$",
			testName:         "test-1",
			want:             false,
		},
		// Empty patterns (should allow everything)
		{
			name:             "empty_patterns_allows_all",
			blacklistPattern: "",
			whitelistPattern: "",
			testName:         "anything",
			want:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := RegexFilter{
				BlacklistPattern: tt.blacklistPattern,
				WhitelistPattern: tt.whitelistPattern,
			}

			// Compile regexes manually since we're not going through YAML unmarshaling
			var err error
			if rf.BlacklistPattern != "" {
				rf.BlacklistRegexp, err = regexp.Compile(rf.BlacklistPattern)
				if err != nil {
					t.Fatalf("Failed to compile blacklist regex: %v", err)
				}
			}
			if rf.WhitelistPattern != "" {
				rf.WhitelistRegexp, err = regexp.Compile(rf.WhitelistPattern)
				if err != nil {
					t.Fatalf("Failed to compile whitelist regex: %v", err)
				}
			}

			got := rf.IsAllowed(tt.testName)
			if got != tt.want {
				t.Errorf("RegexFilter.IsAllowed(%q) = %v, want %v", tt.testName, got, tt.want)
			}
		})
	}
}

// TestConfig_parseCIDRs tests CIDR parsing and storage
func TestConfig_parseCIDRs(t *testing.T) {
	cfg := &NetnsExporterConfig{
		InternalCIDRs: []string{
			"10.0.0.0/8",
			"192.168.0.0/16",
			"172.16.0.0/12",
		},
	}

	err := cfg.parseCIDRs()
	if err != nil {
		t.Fatalf("parseCIDRs() unexpected error: %v", err)
	}

	if len(cfg.parsedCIDRs) != 3 {
		t.Errorf("parsedCIDRs length = %v, want %v", len(cfg.parsedCIDRs), 3)
	}

	// Test that parsed CIDRs work correctly
	testIP := net.ParseIP("10.1.2.3")
	found := false
	for _, ipNet := range cfg.parsedCIDRs {
		if ipNet.Contains(testIP) {
			found = true
			break
		}
	}
	if !found {
		t.Error("10.1.2.3 should be contained in 10.0.0.0/8")
	}
}

// TestConfig_parseCIDRs_InvalidCIDR tests error handling for invalid CIDR
func TestConfig_parseCIDRs_InvalidCIDR(t *testing.T) {
	cfg := &NetnsExporterConfig{
		InternalCIDRs: []string{
			"invalid-cidr",
		},
	}

	err := cfg.parseCIDRs()
	if err == nil {
		t.Fatal("parseCIDRs() expected error for invalid CIDR, got nil")
	}
}

// TestConfig_Validate tests the Validate function
func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *NetnsExporterConfig
		wantErr bool
	}{
		{
			name: "valid_config",
			cfg: &NetnsExporterConfig{
				InternalCIDRs:   []string{"10.0.0.0/8"},
				DestinationHost: "8.8.8.8",
				EnabledMetrics:  MetricsConfig{Ping: false},
			},
			wantErr: false,
		},
		{
			name: "invalid_cidr",
			cfg: &NetnsExporterConfig{
				InternalCIDRs:   []string{"invalid"},
				DestinationHost: "8.8.8.8",
			},
			wantErr: true,
		},
		{
			name: "ping_enabled_no_destination",
			cfg: &NetnsExporterConfig{
				InternalCIDRs:  []string{"10.0.0.0/8"},
				EnabledMetrics: MetricsConfig{Ping: true},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestRegexFilter_UnmarshalYAML tests YAML unmarshaling of regex filters
func TestRegexFilter_UnmarshalYAML(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "regex.yaml")
	content := []byte(`
blacklist_pattern: "^test-"
whitelist_pattern: "^prod-"
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	var rf RegexFilter
	if err := yaml.Unmarshal(data, &rf); err != nil {
		t.Fatalf("yaml.Unmarshal() unexpected error: %v", err)
	}

	if rf.BlacklistPattern != "^test-" {
		t.Errorf("BlacklistPattern = %v, want %v", rf.BlacklistPattern, "^test-")
	}
	if rf.WhitelistPattern != "^prod-" {
		t.Errorf("WhitelistPattern = %v, want %v", rf.WhitelistPattern, "^prod-")
	}
	if rf.BlacklistRegexp == nil {
		t.Error("BlacklistRegexp should be compiled")
	}
	if rf.WhitelistRegexp == nil {
		t.Error("WhitelistRegexp should be compiled")
	}
}

// TestRegexFilter_UnmarshalYAML_InvalidPattern tests error handling for invalid regex in YAML
func TestRegexFilter_UnmarshalYAML_InvalidPattern(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "invalid_regex.yaml")
	content := []byte(`
blacklist_pattern: "[invalid(regex"
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	var rf RegexFilter
	err = yaml.Unmarshal(data, &rf)
	if err == nil {
		t.Fatal("yaml.Unmarshal() expected error for invalid regex, got nil")
	}
}

// TestLoadConfig_ExplicitMetrics tests that explicitly configured metrics are respected
func TestLoadConfig_ExplicitMetrics(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "explicit_metrics.yaml")
	content := []byte(`
api_server:
  server_address: 127.0.0.1
  server_port: 9100
  telemetry_path: /metrics
internal_cidrs:
  - 10.0.0.0/8
destination_host: 1.1.1.1
enabled_metrics:
  interface: true
  conntrack: false
  snmp: true
  sockstat: false
  ping: false
  arp: true
`)
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error: %v", err)
	}

	if !cfg.EnabledMetrics.Interface {
		t.Error("EnabledMetrics.Interface should be true")
	}
	if cfg.EnabledMetrics.Conntrack {
		t.Error("EnabledMetrics.Conntrack should be false")
	}
	if !cfg.EnabledMetrics.SNMP {
		t.Error("EnabledMetrics.SNMP should be true")
	}
	if cfg.EnabledMetrics.Sockstat {
		t.Error("EnabledMetrics.Sockstat should be false")
	}
	if cfg.EnabledMetrics.Ping {
		t.Error("EnabledMetrics.Ping should be false")
	}
	if !cfg.EnabledMetrics.ARP {
		t.Error("EnabledMetrics.ARP should be true")
	}
}
