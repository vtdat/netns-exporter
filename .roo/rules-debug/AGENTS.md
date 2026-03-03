# Project Debug Rules (Non-Obvious Only)

## Debugging Namespace Issues
- Use `--log-level debug` to see namespace enumeration and filtering decisions
- Check `/run/netns/` directory exists and contains namespace files
- Verify with `ip netns list` before running exporter
- Component logs tagged with `.WithField("component", "collector")` or `"api-server"`

## Race Condition Detection
- `make test` always runs with `-race` flag (mandatory for this project)
- Race detector critical for namespace switching code (per-thread operations)
- Goroutine leaks manifest as increasing memory usage over time

## Namespace Switching Failures
- Thread must be locked before namespace operations (silent failures otherwise)
- Original namespace must be restored in defer (prevents thread contamination)
- `/sys` mount failures logged as warnings (other metrics may still work)
- Private mount namespace prevents host contamination (check with `findmnt` in namespace)

## Ping Monitoring Debug
- First scrape creates log file at `/var/log/netns-exporter/<namespace>/ping_log`
- No metrics emitted on first run (need data accumulation)
- Log format: `success <latency_ms>` or `failure 0` per line
- Ping spawned via `ip netns exec <namespace> ping` (runs from host context)
- Check log file permissions if ping process fails silently

## Filter Debugging
- Empty regex patterns are skipped (not compiled)
- Blacklist checked before whitelist (blacklist always wins)
- Use debug logs to see "Skipping namespace X (filtered)" messages
- Test regex patterns locally before deployment

## Common Silent Failures
- Missing SNMP/sockstat files in namespace (logged as errors, not fatal)
- Interface without IPv4 address (logged as debug, skips ping monitoring)
- Conntrack files missing (returns -1, metric not emitted)
