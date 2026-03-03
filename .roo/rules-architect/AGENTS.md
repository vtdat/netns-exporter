# Project Architecture Rules (Non-Obvious Only)

## Critical Design Constraints

### Concurrency Model
- Must use `LimitedWaitGroup` (not `sync.WaitGroup`) to cap goroutine count
- One goroutine per namespace during collection (prevents thread pool exhaustion)
- Each goroutine locks to OS thread (Linux namespace operations are per-thread)
- Semaphore-based limiting prevents memory exhaustion with many namespaces

### Namespace Isolation Requirements
- Thread locking is mandatory (namespace membership is per-thread, not per-process)
- Private mount namespace required for /sys access (prevents host contamination)
- Original namespace must be restored before thread unlock (prevents thread pollution)
- Mount propagation must be MS_SLAVE (prevents leaking mounts to host)

### Metric Collection Architecture
- Descriptors created once at startup (Prometheus requirement, not per-collection)
- Dedicated registry per APIServer (avoids global state pollution)
- Label order is immutable (must match descriptor definition exactly)
- Missing metrics return -1 and are skipped (not emitted as zero)

### Filter Architecture
- Blacklist evaluated before whitelist (blacklist always wins)
- Empty patterns deliberately not compiled (would create match-all regex)
- Applied at two levels: namespace enumeration and device iteration
- Default behavior is allow-all (restrictive only when patterns defined)

## Performance Considerations
- Thread count defaults to `runtime.NumCPU()` (tune for namespace count vs CPU)
- Ping monitoring is asynchronous (doesn't block metric collection)
- File reads are synchronous per namespace (I/O bound, not CPU bound)
- Collection cycle time logged at debug level (monitor for performance issues)

## State Management
- Ping logs are persistent across scrapes (accumulate over time)
- No in-memory state between collections (stateless collector)
- Configuration loaded once at startup (no hot reload)
- Registry is per-server instance (supports multiple servers if needed)

## Integration Constraints
- Requires root/CAP_NET_ADMIN privileges (namespace operations)
- Must run on Linux (uses Linux-specific syscalls)
- Docker requires `--privileged` and `/run/netns` bind mount
- Static binary required for Alpine (CGO_ENABLED=0 in Makefile)
