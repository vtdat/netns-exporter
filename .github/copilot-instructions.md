# Netns-Exporter Copilot Instructions

## Project Overview
**netns-exporter** is a Prometheus exporter that collects network interface and kernel metrics from Linux network namespaces. It requires root/CAP_NET_ADMIN privileges and exports metrics for monitoring per-namespace network statistics.

## Architecture & Data Flow

### Core Components
1. **main.go** – Entry point; initializes logger, config, API server, and signal handling
2. **exporter.go** – HTTP server (APIServer) that registers metrics and serves `/metrics` endpoint
3. **collector.go** – Prometheus collector; implements the core Collect() loop with per-namespace metric gathering
4. **config.go** – YAML config parsing with regex filters and validation
5. **sync.go** – LimitedWaitGroup for concurrency control (limits goroutine count)

### Metric Collection Flow
```
Collector.Collect()
├─ enumerate /run/netns/* (Linux namespace files)
├─ filter by namespaces_filter (blacklist priority over whitelist)
├─ spawn worker goroutines (limited by config.Threads)
└─ for each namespace:
   ├─ lock OS thread + save original namespace
   ├─ switch to target namespace (netns.Set)
   ├─ create private mount namespace (unshare CLONE_NEWNS)
   ├─ mount /sys (thread-specific sysfs view)
   ├─ collect from multiple sources:
   │  ├─ /sys/class/net/<device>/statistics/* (interface metrics)
   │  ├─ Check if interface IP is external (not in internal_cidrs)
   │  │  └─ if external:
   │  │     ├─ Check /var/log/netns-exporter/<namespace>/ping_log
   │  │     ├─ If file not exist: spawn async ping process (ip netns exec)
   │  │     └─ If file exists: parse recent lines, calculate metrics, spawn new ping
   │  ├─ /proc/thread-self/net/snmp (TCP retrans, UDP errors)
   │  ├─ /proc/thread-self/net/sockstat (socket counts)
   │  └─ /proc/*/net/netfilter/nf_conntrack_* (conntrack)
   └─ restore original namespace + unlock thread
```

### Critical Design: OS Thread + Namespace Switching
- **Why per-thread isolation:** Linux namespace membership is per-thread. Multiple goroutines can't safely switch namespaces without race conditions
- **Implementation:** `runtime.LockOSThread()` binds a goroutine to an OS thread; `netns.Set()` changes that thread's network namespace; `LimitedWaitGroup` prevents goroutine explosion
- **Mount isolation:** `syscall.Unshare(CLONE_NEWNS)` creates a private mount namespace so sysfs mounts don't leak to host

## Configuration Patterns

### Ping Monitoring for External IPs (config.go, collector.go)
External IP detection and ICMP connectivity monitoring:
- **Trigger:** When interface IP is NOT in `internal_cidrs` CIDR ranges
- **Log file:** `/var/log/netns-exporter/<namespace_name>/ping_log` (one per namespace)
- **First run:** Creates log file and spawns async ping process (doesn't emit metrics yet)
- **Subsequent runs:** Reads recent lines (count = `scrape_interval`), calculates:
  - Success rate: `(successful_pings / total_pings) * 100`
  - Average latency: Sum of latencies / successful count
  - Spawns new ping process asynchronously in background
- **Configuration fields:**
  - `destination_host`: Target IP/hostname for ping (e.g., "8.8.8.8")
  - `scrape_interval`: Number of recent log lines to analyze (default 60)
  - `log_directory`: Directory for ping logs (default "/var/log/netns-exporter")
- **Log file format:** Plain text, newline-separated, each line: `<success|failure> <latency_ms>`
  - Example: `success 45.23` or `failure 0`
- **Async execution:** `spawnPingProcess()` runs via goroutine using `ip netns exec <namespace> ping`

### RegexFilter Logic (config.go)
- **Blacklist priority:** If name matches blacklist regex → DENY (even if whitelisted)
- **Whitelist enforcement:** If whitelist exists and name doesn't match → DENY
- **Default:** Allow by default if no patterns match
- **Applied to:** namespaces_filter and device_filter; empty patterns are skipped (prevent accidental "match all")

### Metric Naming Convention
All metrics follow: `netns_network_{metric_name}_total`
- Interface metrics labeled: namespace, device, type (qrouter/dhcp/other), host, deviceIP
- SNMP/conntrack metrics labeled: namespace, type, host
- **Ping metrics labeled:** namespace, type, host, destination (destination_host from config)
  - `netns_network_ping_success_rate` (0-100, percentage of successful pings)
  - `netns_network_ping_average_latency_ms` (milliseconds, averaged over recent attempts)

## Developer Workflows

### Build & Test
```bash
make build              # Linux native build
make build-linux       # Cross-compile for Linux (amd64)
make build-docker      # Build inside Docker container
make image             # Build Docker image
make test              # Run tests with race detector
make lint              # Run golangci-lint (v1.62.2+)
```

### Running
- **Host/privileged container:** `./netns-exporter --config /path/to/config.yaml --threads 8 --log-level debug`
- **Docker:** Requires `--privileged` and bind-mount of `/run/netns`
- Default config path: `/etc/netns-exporter/config.yaml`

### Debugging Tips
- Use `--log-level debug` to see namespace enumeration, filtering, and file I/O
- Check that namespaces exist: `ip netns list`
- Verify config parsing: Load config and check Threads, InterfaceMetrics, Filters
- Test filters locally: Apply regex against namespace names before deployment

## Key File Locations & Patterns

### Configuration (config.go, config.example.yaml)
- Validate CIDR notation in internal_cidrs before use
- Thread count defaults to `runtime.NumCPU()` if not specified
- Filter patterns compiled once at startup via UnmarshalYAML

### Logging (main.go)
- Uses sirupsen/logrus; configure level + optional file output
- Components log with field "component" (api-server, collector)
- Log level: debug (verbose), info (default), warn, error

### Dependencies
- **prometheus/client_golang** – metric registration and exposition
- **vishvananda/netns** – namespace switching (netns.Get, netns.Set, netns.GetFromName)
- **gopkg.in/yaml.v2** – config parsing
- **sirupsen/logrus** – structured logging

## Critical Patterns & Conventions

### Goroutine Concurrency
- Never use `sync.WaitGroup` directly; use `LimitedWaitGroup` (semaphore-based) to cap parallelism
- Spinoff worker: `wg.Add(1); go func() { defer wg.Done(); ... }(param)` (avoid closure loops)
- Thread limit: `runtime.NumCPU()` by default; tune via config/CLI flag

### Namespace-Safe Code
- Metrics read from `/proc/thread-self/` automatically see the switched namespace
- `/sys` requires explicit mount per goroutine (sysfs is not thread-aware)
- Always wrap namespace operations: Lock thread → Get original → Set target → Defer restore

### Metric Emission
- Use `prometheus.MustNewConstMetric` in Collect(); don't create metrics during Describe
- Labels must match descriptor order exactly (namespace, device, type, host, ip)
- Return early on file read failure; log but don't fatal

### Filter Application
- Apply `config.NamespacesFilter.IsAllowed(name)` before spawning worker
- Apply `config.DeviceFilter.IsAllowed(devName)` inside collectInterfaces; skip loopback unconditionally
- No filter = allow all; always check before heavy I/O

## Integration Points

### Prometheus Integration
- Single registry per APIServer (not global); isolates metrics
- `/metrics` handler uses dedicated registry to avoid collisions
- Process/Go runtime metrics registered automatically

### Docker
- Multi-stage build (alpine:latest for minimal footprint)
- CGO_ENABLED=0 ensures static binary (portable across libc versions)
- ENTRYPOINT/CMD pattern: allows `docker run ... -- --log-level debug`

### GitHub Actions (check.yml)
- Runs on push/PR; tests Go 1.15+ compatibility
- Steps: setup Go → cache go mod → lint → test
- Failing lint or test blocks merge

## Common Pitfalls

1. **Goroutine leak:** Forgetting `wg.Done()` in worker or not waiting before exit
2. **Namespace leak:** Not restoring original namespace or not unlocking OS thread (use defer)
3. **Empty filter match:** Empty regex pattern matches everything; skip compilation if empty (already done)
4. **Label mismatch:** Metric emit label count/order must match descriptor definition
5. **File not found:** Some namespace configs lack /proc/net/snmp; handle gracefully with -1 return
6. **/sys mount failure:** Proceed with warning if sysfs mount fails; other metrics may still collect

## Testing Strategy
- **Unit tests:** config parsing, regex filters, metric parsing
- **Integration tests:** Run against actual namespaces (requires root/container)
- **Linters:** govet, staticcheck, gosimple, unused, typecheck, revive, gocritic, gosec
- **Race detector:** Always pass `-race` flag to catch data race bugs in Collect loop
