package main

import (
	"context"
	"sync"
	"sync/atomic"
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
// Uses atomic.Value for lock-free reads of immutable data
type MetricCache struct {
	mu             sync.Mutex // Protects writes only
	data           atomic.Value
	scrapeInterval time.Duration
	updateInterval time.Duration
	logger         logrus.FieldLogger
}

// cacheData holds the cached data and timestamp
type cacheData struct {
	metrics []CachedMetricData
	ts      time.Time
}

// NewMetricCache creates a new metric cache
// Cache is updated every half of scrape_interval to ensure fresh data
func NewMetricCache(scrapeIntervalSeconds int, logger *logrus.Logger) *MetricCache {
	scrapeInterval := time.Duration(scrapeIntervalSeconds) * time.Second
	updateInterval := scrapeInterval / 2

	cache := &MetricCache{
		scrapeInterval: scrapeInterval,
		updateInterval: updateInterval,
		logger:         logger.WithField("component", "cache"),
	}
	// Initialize with empty data
	cache.data.Store(cacheData{
		metrics: make([]CachedMetricData, 0),
		ts:      time.Time{},
	})
	return cache
}

// GetMetricData returns cached metric data and the timestamp when they were collected
// Uses lock-free atomic load for better performance under concurrent access
// Returns the slice directly without copying - the slice is immutable after storage
func (mc *MetricCache) GetMetricData() ([]CachedMetricData, time.Time) {
	stored := mc.data.Load().(cacheData)
	return stored.metrics, stored.ts
}

// UpdateCache stores new metric data and updates the timestamp
// Uses atomic Store to publish new data atomically to all readers
func (mc *MetricCache) UpdateCache(data []CachedMetricData) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.data.Store(cacheData{
		metrics: data,
		ts:      time.Now(),
	})

	mc.logger.Debugf("Cache updated with %d metrics at %s",
		len(data), time.Now().Format(time.RFC3339))
}

// GetCacheAge returns how old the cache is
func (mc *MetricCache) GetCacheAge() time.Duration {
	stored := mc.data.Load().(cacheData)
	if stored.ts.IsZero() {
		return 0
	}
	return time.Since(stored.ts)
}

// GetLastUpdateTime returns the timestamp of the last cache update
func (mc *MetricCache) GetLastUpdateTime() time.Time {
	stored := mc.data.Load().(cacheData)
	return stored.ts
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
