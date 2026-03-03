# Caching Mechanism

## Overview

The netns-exporter implements a memory caching mechanism to optimize performance when multiple Prometheus instances scrape the same exporter. Instead of querying all network namespaces on every scrape request, the exporter maintains a cache that is updated periodically in the background.

## How It Works

### Architecture

1. **MetricCache** (`cache.go`): A thread-safe cache that stores collected metrics with timestamps
2. **Periodic Updates**: A background goroutine updates the cache every **half of `scrape_interval`** to ensure fresh data
3. **Instant Response**: When Prometheus scrapes the `/metrics` endpoint, the exporter immediately returns cached metrics without querying namespaces

### Components

#### MetricCache
- Stores metrics in memory with a timestamp
- Thread-safe using `sync.RWMutex`
- Automatically updates every `scrape_interval` seconds
- Runs independently of Prometheus scrape requests

#### Collector
- `Collect()`: Returns cached metrics to Prometheus (fast, no namespace queries)
- `collectMetrics()`: Performs actual namespace collection (called by cache background goroutine)

#### APIServer
- Creates and manages the cache lifecycle
- Starts periodic cache updates on initialization
- Stops cache updates on shutdown

## Benefits

1. **Performance**: Multiple Prometheus instances can scrape without causing redundant namespace queries
2. **Consistency**: All scrapers within the same interval get the same metrics snapshot
3. **Reduced Load**: Namespace switching and metric collection happen at a controlled rate
4. **Predictable Timing**: Metrics are collected at regular intervals regardless of scrape frequency

## Configuration

The cache update interval is controlled by the `scrape_interval` setting in your configuration file:

```yaml
scrape_interval: 60  # Cache updates every 30 seconds (half of 60)
```

**Important**: The cache automatically updates every **half of `scrape_interval`** to ensure metrics are always fresh. For example:
- `scrape_interval: 60` → cache updates every 30 seconds
- `scrape_interval: 120` → cache updates every 60 seconds

This ensures that when Prometheus scrapes, the cached data is never older than half the scrape interval.

## Behavior

### On Startup
1. Exporter starts and creates the cache
2. Performs an immediate initial collection
3. Starts periodic updates in the background (every half of `scrape_interval`)
4. Ready to serve metrics from cache

### During Operation
1. Background goroutine collects metrics every `scrape_interval / 2` seconds
2. Prometheus scrapes return cached metrics immediately
3. Cache timestamp is included in logs for debugging

### On Shutdown
1. Periodic cache updates are stopped
2. HTTP server shuts down gracefully
3. No data loss - last cached metrics remain available until shutdown completes

## Monitoring Cache Behavior

Check the logs for cache-related messages:

```
INFO[0000] Starting periodic cache updates every 30s (scrape_interval: 1m0s)  component=cache
DEBUG[0000] Starting periodic metric collection           component=cache
INFO[0003] Periodic collection completed in 2.5s, cached 1234 metrics  component=cache
DEBUG[0010] Serving 1234 cached metrics from 2026-01-09T08:30:00Z (age: 7s)  component=collector
```

## Thread Safety

The cache implementation is fully thread-safe:
- Uses `sync.RWMutex` for concurrent read/write access
- Multiple Prometheus scrapers can read simultaneously
- Background updates acquire write lock only during cache updates
- No race conditions (verified with `go test -race`)

## Performance Characteristics

- **Cache Hit**: ~1ms (returns pre-collected metrics)
- **Cache Update**: Depends on namespace count and enabled metrics (typically 1-5 seconds)
- **Update Frequency**: Every `scrape_interval / 2` seconds
- **Cache Freshness**: Maximum age is half of `scrape_interval`
- **Memory Usage**: Proportional to number of metrics (typically <10MB for 100 namespaces)
- **CPU Usage**: Spike during collection, idle between updates

## Comparison: Before vs After

### Before (No Cache)
- Every Prometheus scrape triggers full namespace collection
- 3 Prometheus instances = 3x the work
- Collection time: 2-5 seconds per scrape
- High CPU usage during scrapes

### After (With Cache)
- Namespace collection happens every `scrape_interval / 2` seconds
- 3 Prometheus instances = same work as 1 instance
- Response time: <1ms per scrape
- Predictable CPU usage pattern
- Metrics are always fresh (max age: half of scrape interval)
