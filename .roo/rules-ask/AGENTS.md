# Project Documentation Rules (Non-Obvious Only)

## Architecture Context

### Why Thread Locking is Required
- Linux namespace membership is per-OS-thread, not per-goroutine
- Go runtime can migrate goroutines between threads
- `runtime.LockOSThread()` pins goroutine to specific thread for namespace operations
- Without locking, namespace switches affect wrong threads (race conditions)

### Why /sys Requires Mounting
- `/proc/thread-self/` is thread-aware (automatically sees switched namespace)
- `/sys/class/net/` is NOT thread-aware (shows host's view)
- Must create private mount namespace and mount sysfs per goroutine
- `MS_SLAVE|MS_REC` prevents mount propagation back to host

### Ping Monitoring Design
- Only monitors external IPs (not in `internal_cidrs`)
- Asynchronous execution prevents blocking metric collection
- Log-based approach allows historical analysis
- Spawned from host context using `ip netns exec` (not from inside namespace)

## File Organization
- `collector.go` - Core metric collection (761 lines, most complex)
- `sync.go` - Custom semaphore-based WaitGroup (prevents goroutine explosion)
- `config.go` - YAML parsing with regex compilation in UnmarshalYAML
- `exporter.go` - HTTP server with dedicated Prometheus registry (not global)
- `main.go` - Entry point with graceful shutdown handling

## Non-Standard Patterns
- Metric descriptors created once in constructor, not per-collection
- Filter logic: blacklist priority over whitelist (counterintuitive)
- Thread count CLI flag overrides config file (main.go line 39)
- Empty regex patterns deliberately not compiled (would match everything)
- Return -1 convention for file read errors (caller decides to skip metric)

## Configuration Gotchas
- `destination_host` is required (validation fails without it)
- `internal_cidrs` must be valid CIDR notation (validated at load time)
- `scrape_interval` determines how many recent ping log lines to analyze
- Defaults applied AFTER validation (LoadConfig sequence matters)
