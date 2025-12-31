package main

import (
	"fmt"
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

	// Apply Defaults
	// Only override threads if not specified in config
	if cfg.Threads <= 0 {
		cfg.Threads = runtime.NumCPU()
	}

	return &cfg, nil
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
