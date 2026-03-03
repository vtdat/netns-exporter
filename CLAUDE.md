# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make lint          # Install golangci-lint v1.62.2+ if needed, run linters
make test          # Run tests with -race flag (mandatory for race detection)
make build         # Build for host OS
make build-linux   # Cross-compile for Linux (amd64) with static binary
make image         # Build Docker image
make clean         # Remove build artifacts
```

## Architecture Overview

**Netns-exporter** is a Prometheus exporter that collects network interface and kernel metrics from Linux network namespaces. It requires root/CAP_NET_ADMIN privileges.

### Core Components

| File | Purpose |
|------|---------|
| `main.go` | Entry point; CLI flags, logger, config, signal handling |
| `exporter.go` | HTTP server with dedicated Prometheus registry; middleware logging |
| `collector.go` | Namespace metric collection; `/sys`, `/proc` parsing, ping monitoring |
| `cache.go` | Thread-safe metric cache with periodic background updates |
| `config.go` | YAML config parsing with regex filters and CIDR validation |
| `sync.go` | `LimitedWaitGroup` for concurrency control (semaphore-based) |

### Data Flow

```
APIServer.New()
├─ Create prometheus.Registry (dedicated, not global)
├─ Create MetricCache
├─ Register Collector
└─ Start periodic cache updates (every scrape_interval/2)

Collector.Collect()  [called by Prometheus scrape]
└─ Return cached metrics immediately

Cache.backgroundUpdate()  [runs every scrape_interval/2 seconds]
└─ collector.collectMetrics()
   ├─ Read /run/netns/* namespace files
   ├─ Filter by namespaces_filter (blacklist > whitelist)
   ├─ Spawn workers via LimitedWaitGroup (config.Threads)
   └─ Per namespace:
      ├─ runtime.LockOSThread()  [CRITICAL: namespace is per-thread]
      ├─ netns.Set() to target namespace
      ├─ syscall.Unshare(CLONE_NEWNS) for private mounts
      ├─ Mount /sys (read-only sysfs)
      ├─ Collect: interfaces, conntrack, SNMP, sockstat, ARP, ping
      └─ Restore namespace + UnlockOSThread()
```

### Critical Architecture Patterns

**1. OS Thread Locking** (`collector.go:358-372`)
- Linux namespace membership is per-thread, not per-goroutine
- MUST use `runtime.LockOSThread()` before `netns.Set()`
- MUST restore original namespace in defer before `UnlockOSThread()`
- `/proc/thread-self/` automatically sees switched namespace; `/sys` needs explicit mount

**2. LimitedWaitGroup** (`sync.go`)
- NEVER use `sync.WaitGroup` directly - use `LimitedWaitGroup` (semaphore-based)
- Prevents goroutine explosion when processing many namespaces
- Pattern: `wg.Add(1); go func() { defer wg.Done(); ... }()`

**3. Filter Logic** (`config.go:149-163`)
- Blacklist has priority: match = always DENY
- Whitelist: if defined and no match = DENY
- Default: ALLOW
- Empty regex patterns are NOT compiled (would match everything)

**4. Ping Monitoring** (`collector.go:484-529`)
- Triggered when interface IP NOT in `internal_cidrs` (and namespace type = "qrouter")
- First run: creates `/var/log/netns-exporter/<ns>/ping_log`, spawns ping, no metrics
- Subsequent runs: parse recent lines (count=`scrape_interval`), emit metrics, respawn ping
- Ping format: `success <latency_ms>` or `failure 0`
- Runs via `ip netns exec <ns> ping -c <interval> -i 1 -W 2 <dest>`

**5. Metric Caching** (`cache.go`)
- Cache updates every `scrape_interval / 2` seconds
- Prometheus scrapes return cached data instantly (~1ms vs 2-5s collection time)
- Thread-safe via `sync.RWMutex`
- See `CACHE.md` for full details

### Configuration (`config.example.yaml`)

```yaml
api_server:
  server_address: 0.0.0.0
  server_port: 9101
  telemetry_path: /metrics

interface_metrics: ["rx_bytes", "tx_bytes", "rx_packets", "tx_packets", ...]
internal_cidrs: ["10.0.0.0/8"]  # Skip ping for these IPs

namespaces_filter:
  whitelist_pattern: "^qrouter-\\d$"  # Optional

destination_host: 8.8.8.8  # For ping monitoring
scrape_interval: 60       # Cache updates every 30s
log_directory: /var/log/netns-exporter

# Enable/disable metric categories (all true by default)
enabled_metrics:
  interface: true
  conntrack: true
  snmp: true
  sockstat: true
  ping: true
  arp: true
```

### Metric Naming

All metrics: `netns_network_{metric_name}_total`

Labels:
- Interface: `netns`, `device`, `type` (qrouter/qdhcp/other), `host`, `deviceIP`
- SNMP/Conntrack/Sockstat: `netns`, `type`, `host`
- Ping: `netns`, `device`, `type`, `host`, `destination`
- ARP: `netns`, `type`, `host`, `ip_address`, `hw_address`, `device`, `state`

### Common Pitfalls

1. **Goroutine leak**: Forgetting `wg.Done()` or not waiting before exit
2. **Namespace leak**: Not restoring namespace or not unlocking thread (always use defer)
3. **Label mismatch**: `MustNewConstMetric` label order must match descriptor definition
4. **Empty filter match**: Empty regex matches everything - skip compilation if empty
5. **Missing /proc files**: Some namespaces lack SNMP/sockstat - handle gracefully, return -1
6. **/sys mount failure**: Proceed with warning; other metrics may still work

### Docker Usage

```bash
docker run --privileged \
  --mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=slave \
  -p 9101:9101 \
  netns-exporter
```

Or use `docker-compose.yml` (requires `/var/log/netns-exporter` directory).
