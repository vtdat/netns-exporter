package main

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"

	yaml "gopkg.in/yaml.v2"
)

type NetnsExporterConfig struct {
	APIServer        APIServerConfig `yaml:"api_server"`
	InterfaceMetrics []string        `yaml:"interface_metrics"`
	Threads          int             `yaml:"threads"`
	NamespacesFilter RegexFilter     `yaml:"namespaces_filter"`
	DeviceFilter     RegexFilter     `yaml:"device_filter"`
	InternalCIDRs    []string        `yaml:"internal_cidrs"`
	DestinationHost  string          `yaml:"destination_host"`
	ScrapeInterval   int             `yaml:"scrape_interval"`
	LogDirectory     string          `yaml:"log_directory"`
	EnabledMetrics   MetricsConfig   `yaml:"enabled_metrics"`
}

type MetricsConfig struct {
	Interface bool `yaml:"interface"`
	Conntrack bool `yaml:"conntrack"`
	SNMP      bool `yaml:"snmp"`
	Sockstat  bool `yaml:"sockstat"`
	Ping      bool `yaml:"ping"`
	ARP       bool `yaml:"arp"`
}

type APIServerConfig struct {
	ServerAddress  string `yaml:"server_address"`
	ServerPort     int    `yaml:"server_port"`
	RequestTimeout int    `yaml:"request_timeout"`
	TelemetryPath  string `yaml:"telemetry_path"`
}

// RegexFilter consolidates the logic for both Namespace and Device filters.
type RegexFilter struct {
	BlacklistPattern string `yaml:"blacklist_pattern"`
	WhitelistPattern string `yaml:"whitelist_pattern"`

	// yaml:"-" prevents these fields from being read/written to YAML directly
	BlacklistRegexp *regexp.Regexp `yaml:"-"`
	WhitelistRegexp *regexp.Regexp `yaml:"-"`
}

func LoadConfig(path string) (*NetnsExporterConfig, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Unmarshal
	var cfg NetnsExporterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate Config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation error: %w", err)
	}

	// Apply Defaults
	// Only override threads if not specified in config
	if cfg.Threads <= 0 {
		cfg.Threads = runtime.NumCPU()
	}

	// Set default log directory if not specified
	if cfg.LogDirectory == "" {
		cfg.LogDirectory = "/var/log/netns-exporter"
	}

	// Default scrape interval to 60 seconds if not specified
	if cfg.ScrapeInterval <= 0 {
		cfg.ScrapeInterval = 60
	}

	// Apply metric defaults - enable all by default if not specified
	cfg.applyMetricDefaults()

	return &cfg, nil
}

// applyMetricDefaults sets default values for metric collection flags
// All metrics are enabled by default if not explicitly configured
func (cfg *NetnsExporterConfig) applyMetricDefaults() {
	// If no metrics are explicitly configured, enable all by default
	// This maintains backward compatibility
	allDisabled := !cfg.EnabledMetrics.Interface &&
		!cfg.EnabledMetrics.Conntrack &&
		!cfg.EnabledMetrics.SNMP &&
		!cfg.EnabledMetrics.Sockstat &&
		!cfg.EnabledMetrics.Ping &&
		!cfg.EnabledMetrics.ARP

	if allDisabled {
		cfg.EnabledMetrics.Interface = true
		cfg.EnabledMetrics.Conntrack = true
		cfg.EnabledMetrics.SNMP = true
		cfg.EnabledMetrics.Sockstat = true
		cfg.EnabledMetrics.Ping = true
		cfg.EnabledMetrics.ARP = true
	}
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
// It compiles the regex patterns after loading the strings.
func (rf *RegexFilter) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Define a type alias to prevent recursive calls to UnmarshalYAML
	type plain RegexFilter
	if err := unmarshal((*plain)(rf)); err != nil {
		return err
	}

	var err error

	// Only compile if the pattern is not empty.
	// Compiling an empty string creates a Regex that matches everything!
	if rf.BlacklistPattern != "" {
		rf.BlacklistRegexp, err = regexp.Compile(rf.BlacklistPattern)
		if err != nil {
			return fmt.Errorf("invalid blacklist pattern '%s': %w", rf.BlacklistPattern, err)
		}
	}

	if rf.WhitelistPattern != "" {
		rf.WhitelistRegexp, err = regexp.Compile(rf.WhitelistPattern)
		if err != nil {
			return fmt.Errorf("invalid whitelist pattern '%s': %w", rf.WhitelistPattern, err)
		}
	}

	return nil
}

// IsAllowed checks if a given name (namespace or device) is permitted.
// Logic:
// 1. If it matches the Blacklist, it is DENIED.
// 2. If a Whitelist is defined and it does NOT match, it is DENIED.
// 3. Otherwise, it is ALLOWED.
func (rf *RegexFilter) IsAllowed(name string) bool {
	// Check Blacklist first (Highest Priority)
	if rf.BlacklistRegexp != nil && rf.BlacklistRegexp.MatchString(name) {
		return false
	}

	// Check Whitelist (Restrictive Mode)
	// If a whitelist exists, the name MUST match it.
	if rf.WhitelistRegexp != nil && !rf.WhitelistRegexp.MatchString(name) {
		return false
	}

	// Default Allow
	return true
}

// isValidCIDR checks if InternalCIDRs are valid CIDR notation
func isValidCIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	return err == nil
}
func (cfg *NetnsExporterConfig) Validate() error {
	// Validate InternalCIDRs
	for _, cidr := range cfg.InternalCIDRs {
		if !isValidCIDR(cidr) {
			return fmt.Errorf("invalid CIDR notation in internal_cidrs: %s", cidr)
		}
	}

	// Validate that destination_host is set if ping monitoring is enabled
	if cfg.EnabledMetrics.Ping && cfg.DestinationHost == "" {
		return fmt.Errorf("destination_host must be configured when ping monitoring is enabled")
	}

	return nil
}
