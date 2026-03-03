# Project Coding Rules (Non-Obvious Only)

## Critical Patterns

### Thread Safety in Namespace Operations
- Always use `LimitedWaitGroup` from sync.go instead of `sync.WaitGroup` (prevents goroutine explosion)
- Pattern: `wg.Add(1); go func(param) { defer wg.Done(); ... }(value)` - pass params to avoid closure variable capture

### Namespace Switching Sequence (collector.go)
1. `runtime.LockOSThread()` - MUST be first
2. `netns.Get()` - save original namespace
3. `defer netns.Set(originalNS)` - restore before unlock
4. `defer runtime.UnlockOSThread()` - MUST be after restore
5. `syscall.Unshare(CLONE_NEWNS)` - create private mount namespace
6. Mount /sys with `MS_SLAVE|MS_REC` flags

### Metric Creation
- Create all `prometheus.Desc` in `NewCollector()`, never in `Collect()`
- Label order in `MustNewConstMetric()` must exactly match descriptor definition
- Use `prometheus.MustNewConstMetric()` in Collect(), not persistent metrics

### Ping Process Implementation
- Continuous ping runs for full `scrape_interval` duration (not single ping)
- Use `bufio.Scanner` to read stdout line-by-line in real-time
- Track expected vs received pings to detect timeouts
- Write results to log file as they arrive (not buffered)
- Timeout pings logged as failures after process completes

### Error Handling
- `readFloatFromFile()` returns -1 on error (caller skips metric emission)
- Missing SNMP/sockstat files are warnings, not fatal errors
- Failed namespace switch logs error and returns (doesn't crash collector)

## Configuration Behavior
- CLI `--threads` flag overrides config file value (applied in main.go line 39)
- Empty regex patterns are NOT compiled (would match everything - see config.go UnmarshalYAML)
- Config validation runs before defaults applied (LoadConfig sequence matters)
