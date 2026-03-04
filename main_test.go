package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// TestSetupLogger_DefaultLevel tests logger with default info level
func TestSetupLogger_DefaultLevel(t *testing.T) {
	logger := setupLogger("info", "")

	if logger == nil {
		t.Fatal("setupLogger() returned nil")
	}

	if logger.GetLevel() != logrus.InfoLevel {
		t.Errorf("logger level = %v, want %v", logger.GetLevel(), logrus.InfoLevel)
	}
}

// TestSetupLogger_CustomLevel tests logger with custom level
func TestSetupLogger_CustomLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		wantLevel logrus.Level
	}{
		{
			name:     "debug_level",
			level:    "debug",
			wantLevel: logrus.DebugLevel,
		},
		{
			name:     "info_level",
			level:    "info",
			wantLevel: logrus.InfoLevel,
		},
		{
			name:     "warn_level",
			level:    "warn",
			wantLevel: logrus.WarnLevel,
		},
		{
			name:     "error_level",
			level:    "error",
			wantLevel: logrus.ErrorLevel,
		},
		{
			name:     "trace_level",
			level:    "trace",
			wantLevel: logrus.TraceLevel,
		},
		{
			name:     "fatal_level",
			level:    "fatal",
			wantLevel: logrus.FatalLevel,
		},
		{
			name:     "panic_level",
			level:    "panic",
			wantLevel: logrus.PanicLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := setupLogger(tt.level, "")

			if logger.GetLevel() != tt.wantLevel {
				t.Errorf("setupLogger(%q) level = %v, want %v", tt.level, logger.GetLevel(), tt.wantLevel)
			}
		})
	}
}

// TestSetupLogger_InvalidLevel tests fallback to info on invalid level
func TestSetupLogger_InvalidLevel(t *testing.T) {
	// Capture log output to verify warning is printed
	var buf bytes.Buffer

	logger := setupLogger("invalid-level", "")
	logger.SetOutput(&buf)

	// Trigger the warning by calling with invalid level again
	// (The warning is printed during setupLogger execution)

	if logger.GetLevel() != logrus.InfoLevel {
		t.Errorf("logger level = %v, want %v (should fallback to info)", logger.GetLevel(), logrus.InfoLevel)
	}
}

// TestSetupLogger_InvalidLevel_WarningMessage tests that warning message is printed
func TestSetupLogger_InvalidLevel_WarningMessage(t *testing.T) {
	logger := setupLogger("not-a-real-level", "")

	// Capture output
	var buf bytes.Buffer
	logger.SetOutput(&buf)

	// Log something to trigger output
	logger.Warn("test")

	// The warning about invalid level should have been printed during setup
	// We can't easily capture it since it's printed to the original logger
	// So we just verify the logger still works
	if logger.GetLevel() != logrus.InfoLevel {
		t.Errorf("logger should fallback to info level")
	}
}

// TestSetupLogger_FileOutput tests logging to file
func TestSetupLogger_FileOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := tmpDir + "/test.log"

	logger := setupLogger("info", logFile)

	if logger == nil {
		t.Fatal("setupLogger() returned nil")
	}

	// Verify file was created
	_, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("Log file should exist: %v", err)
	}

	// Write a log message
	logger.Info("test message")

	// Read file and verify content
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(data), "test message") {
		t.Errorf("Log file should contain 'test message', got: %s", string(data))
	}
}

// TestSetupLogger_FileOutput_Append tests that logging appends to existing file
func TestSetupLogger_FileOutput_Append(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := tmpDir + "/test.log"

	// Create file with initial content
	if err := os.WriteFile(logFile, []byte("initial content\n"), 0644); err != nil {
		t.Fatalf("Failed to create initial file: %v", err)
	}

	logger := setupLogger("info", logFile)

	// Write a log message
	logger.Info("new message")

	// Read file and verify both messages exist
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "initial content") {
		t.Errorf("Log file should contain 'initial content', got: %s", content)
	}
	if !strings.Contains(content, "new message") {
		t.Errorf("Log file should contain 'new message', got: %s", content)
	}
}

// TestSetupLogger_FileOutput_CreatesFileInExistingDir tests file creation in existing directory
func TestSetupLogger_FileOutput_CreatesFileInExistingDir(t *testing.T) {
	// Test that log file is created in an existing directory
	tmpDir := t.TempDir()
	logFile := tmpDir + "/test.log"

	logger := setupLogger("info", logFile)

	// Verify file was created
	_, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("Log file should exist: %v", err)
	}

	if logger == nil {
		t.Fatal("setupLogger() returned nil")
	}
}

// TestSetupLogger_EmptyLogFile tests with empty log file path (stdout)
func TestSetupLogger_EmptyLogFile(t *testing.T) {
	logger := setupLogger("info", "")

	if logger == nil {
		t.Fatal("setupLogger() returned nil")
	}

	// Should log to stdout by default
	// We can't easily verify this, but we can verify the logger works
	logger.Info("test")
}

// TestSetupLogger_CaseInsensitiveLevel tests that level parsing is case-insensitive
func TestSetupLogger_CaseInsensitiveLevel(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantLevel logrus.Level
	}{
		{
			name:      "uppercase_info",
			level:     "INFO",
			wantLevel: logrus.InfoLevel,
		},
		{
			name:      "uppercase_debug",
			level:     "DEBUG",
			wantLevel: logrus.DebugLevel,
		},
		{
			name:      "mixed_case_info",
			level:     "InFo",
			wantLevel: logrus.InfoLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := setupLogger(tt.level, "")

			if logger.GetLevel() != tt.wantLevel {
				t.Errorf("setupLogger(%q) level = %v, want %v", tt.level, logger.GetLevel(), tt.wantLevel)
			}
		})
	}
}

// TestSetupLogger_FilePermissions tests log file permissions
func TestSetupLogger_FilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := tmpDir + "/test.log"

	logger := setupLogger("info", logFile)

	// Check file permissions (should be 0644)
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("Failed to stat log file: %v", err)
	}

	// Check that file is readable and writable by owner
	mode := info.Mode()
	if mode&0600 != 0600 {
		t.Errorf("Log file permissions = %v, want at least 0600", mode&0777)
	}

	// Write a message to verify file is writable
	logger.Info("test message")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(data), "test message") {
		t.Error("Log file should contain test message")
	}
}

// TestSetupLogger_OutputType tests that logger output is correctly configured
func TestSetupLogger_OutputType(t *testing.T) {
	// Test stdout output (empty path)
	logger := setupLogger("info", "")
	if logger == nil {
		t.Fatal("setupLogger() returned nil")
	}

	// Verify we can write to it
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.Info("test")

	if buf.Len() == 0 {
		t.Error("Logger should have written output")
	}
}

// TestSetupLogger_Reconfigure tests reconfiguring logger
func TestSetupLogger_Reconfigure(t *testing.T) {
	tmpDir := t.TempDir()

	// First logger to stdout
	logger1 := setupLogger("debug", "")
	if logger1.GetLevel() != logrus.DebugLevel {
		t.Errorf("logger1 level = %v, want %v", logger1.GetLevel(), logrus.DebugLevel)
	}

	// Second logger to file
	logFile := tmpDir + "/test2.log"
	logger2 := setupLogger("error", logFile)
	if logger2.GetLevel() != logrus.ErrorLevel {
		t.Errorf("logger2 level = %v, want %v", logger2.GetLevel(), logrus.ErrorLevel)
	}

	// Verify they are independent
	if logger1 == logger2 {
		t.Error("Loggers should be different instances")
	}
}
