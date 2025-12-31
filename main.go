package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

const defaultConfigFile = "/etc/netns-exporter/config.yaml"

func main() {
	var (
		cfgPath     string
		logFilePath string
		logLevel    string
		threads     int
	)

	flag.StringVar(&cfgPath, "config", defaultConfigFile, "Path to config file")
	flag.StringVar(&logFilePath, "log-file", "", "Write logs to file (default: stdout)")
	flag.StringVar(&logLevel, "log-level", "info", "Logging level")
	flag.IntVar(&threads, "threads", runtime.NumCPU(), "Number of threads for collecting data")
	flag.Parse()

	// 1. Initialize Logger
	logger := setupLogger(logLevel, logFilePath)

	// 2. Load Config
	config, err := LoadConfig(cfgPath)
	if err != nil {
		logger.Fatalf("Failed to load config from %s: %v", cfgPath, err)
	}
	config.Threads = threads

	// 3. Setup Context for Graceful Shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 4. Initialize API Server
	apiServer, err := NewAPIServer(config, logger)
	if err != nil {
		logger.WithError(err).Fatal("Creating API server failed")
	}

	// 5. Run Server in a Goroutine
	go func() {
		if err := apiServer.Start(); err != nil {
			logger.Error("Server stopped unexpectedly")
		}
	}()

	// Wait for signal...
	<-ctx.Done()

	// Create a timeout context for shutdown (e.g. 5 seconds)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("Server forced to shutdown: %v", err)
	}
}

func setupLogger(levelStr, path string) *logrus.Logger {
	logger := logrus.New()

	level, err := logrus.ParseLevel(levelStr)
	if err != nil {
		logger.Warnf("Invalid log level '%s', defaulting to info", levelStr)
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	if path != "" {
		logFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			logger.WithError(err).Fatalf("Failed to open log file: %s", path)
		}
		logger.SetOutput(logFile)
	}

	return logger
}
