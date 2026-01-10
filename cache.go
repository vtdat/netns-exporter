package main

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// CachedMetricData stores the raw data needed to recreate a Prometheus metric
type CachedMetricData struct {
	Desc        string
	ValueType   string
	Value       float64
	LabelNames  []string
	LabelValues []string
}

// MetricCache stores cached metric data with timestamp
type MetricCache struct {
	mu             sync.RWMutex
	metricData     []CachedMetricData
	lastUpdate     time.Time
	scrapeInterval time.Duration
	updateInterval time.Duration
	logger         logrus.FieldLogger
}

// NewMetricCache creates a new metric cache
// Cache is updated every half of scrape_interval to ensure fresh data
func NewMetricCache(scrapeIntervalSeconds int, logger *logrus.Logger) *MetricCache {
	scrapeInterval := time.Duration(scrapeIntervalSeconds) * time.Second
	updateInterval := scrapeInterval / 2

	return &MetricCache{
		metricData:     make([]CachedMetricData, 0),
		scrapeInterval: scrapeInterval,
		updateInterval: updateInterval,
		logger:         logger.WithField("component", "cache"),
	}
}

// GetMetricData returns cached metric data and the timestamp when they were collected
func (mc *MetricCache) GetMetricData() ([]CachedMetricData, time.Time) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Return a copy of the metric data slice to avoid race conditions
	dataCopy := make([]CachedMetricData, len(mc.metricData))
	copy(dataCopy, mc.metricData)

	return dataCopy, mc.lastUpdate
}

// UpdateCache stores new metric data and updates the timestamp
func (mc *MetricCache) UpdateCache(data []CachedMetricData) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.metricData = data
	mc.lastUpdate = time.Now()

	mc.logger.Debugf("Cache updated with %d metrics at %s",
		len(data), mc.lastUpdate.Format(time.RFC3339))
}

// GetCacheAge returns how old the cache is
func (mc *MetricCache) GetCacheAge() time.Duration {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if mc.lastUpdate.IsZero() {
		return 0
	}

	return time.Since(mc.lastUpdate)
}

// GetLastUpdateTime returns the timestamp of the last cache update
func (mc *MetricCache) GetLastUpdateTime() time.Time {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	return mc.lastUpdate
}

// StartPeriodicUpdate starts a background goroutine that periodically updates the cache
// Updates occur every half of scrape_interval to ensure fresh metrics
func (mc *MetricCache) StartPeriodicUpdate(ctx context.Context, collector *Collector) {
	go func() {
		mc.logger.Infof("Starting periodic cache updates every %s (scrape_interval: %s)",
			mc.updateInterval, mc.scrapeInterval)

		// Perform initial collection immediately
		mc.performCollection(collector)

		ticker := time.NewTicker(mc.updateInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				mc.logger.Info("Stopping periodic cache updates")
				return
			case <-ticker.C:
				mc.performCollection(collector)
			}
		}
	}()
}

// performCollection collects metrics and updates the cache
func (mc *MetricCache) performCollection(collector *Collector) {
	startTime := time.Now()
	mc.logger.Debug("Starting periodic metric collection")

	// Collect metric data
	metricData := collector.collectMetrics()

	// Update cache with collected metric data
	mc.UpdateCache(metricData)

	mc.logger.Infof("Periodic collection completed in %s, cached %d metrics",
		time.Since(startTime), len(metricData))
}
