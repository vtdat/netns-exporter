# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Build & Test Commands
- `make lint` - Installs golangci-lint v1.62.2 if needed (required for Go 1.23+)
- `make test` - Always runs with `-race` flag (mandatory for detecting data races in namespace switching)
- `make build-linux` - Uses `CGO_ENABLED=0` for static binary (required for Alpine Docker image)

## Critical Architecture Patterns

### OS Thread Locking (collector.go)
- **MUST** use `runtime.LockOSThread()` before namespace switching - Linux namespace membership is per-thread
- **MUST** save original namespace with `netns.Get()` and restore in defer before `UnlockOSThread()`
- **MUST** create private mount namespace with `syscall.Unshare(CLONE_NEWNS)` before mounting /sys
- `/proc/thread-self/` paths automatically see switched namespace; `/sys` requires explicit mount per goroutine

### Concurrency Control (sync.go)
- **NEVER** use `sync.WaitGroup` directly - always use `LimitedWaitGroup` (semaphore-based)
- Prevents goroutine explosion when processing many namespaces
- Pattern: `wg.Add(1); go func(param) { defer wg.Done(); ... }(value)` (avoid closure loops)

### Filter Logic (config.go)
- Blacklist has priority over whitelist (blacklist match = always deny)
- Empty regex pattern is NOT compiled (would match everything)
- Applied in `IsAllowed()`: blacklist check → whitelist check → default allow

### Ping Monitoring (collector.go)
- Only triggered when interface IP is NOT in `internal_cidrs`
- First run: creates log file, spawns continuous ping process, returns without metrics
- Subsequent runs: parses recent lines (count = `scrape_interval`), emits metrics, spawns new continuous ping
- **Continuous ping**: runs for `scrape_interval` seconds with `-i 1` (one ping per second)
- Log format: `success <latency_ms>` or `failure 0` (one per line, written in real-time)
- Ping runs via `ip netns exec <namespace> ping -c <scrape_interval> -i 1` (from host, not inside namespace)
- Timeout handling: missing pings (expected vs received) are logged as failures

## Code Conventions
- Metric descriptors created in `NewCollector()`, not in `Collect()` (Prometheus requirement)
- Label order in `MustNewConstMetric()` must match descriptor definition exactly
- Return -1 from `readFloatFromFile()` on error; caller skips metric emission
- Component logging uses `.WithField("component", "name")` pattern
- Config validation happens in `LoadConfig()` before defaults applied

## Non-Standard Behaviors
- Thread count defaults to `runtime.NumCPU()` if not in config or CLI (CLI flag overrides config)
- `/sys` mount uses `MS_SLAVE|MS_REC` to prevent propagation back to host
- SNMP/sockstat files may not exist in some namespaces - handle gracefully, don't fatal
- Namespace type detection: prefix `qrouter-` = "qrouter", `dhcp-` = "dhcp", else "other"
