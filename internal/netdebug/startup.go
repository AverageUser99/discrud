package netdebug

import "time"

// Fixed stages only: no message text, identifiers, channel names or paths.
func Startup(stage string, started time.Time, count int) {
	switch stage {
	case "begin", "account_validated", "disk_cache_loaded", "shell_visible", "cached_messages", "fresh_messages", "cached_presented", "fresh_presented", "gateway_ready", "navigation_ready", "metadata_ready", "render", "render_full", "render_append", "render_patch", "startup_cache_miss":
	default:
		return
	}
	if m := active.Load(); m != nil {
		m.logger.Printf("startup stage=%s elapsed_ms=%d count=%d", stage, time.Since(started).Milliseconds(), count)
	}
}
func Thumbnail(hit bool, result string) {
	switch result {
	case "ok", "cancelled", "unsupported-format", "unavailable":
	default:
		result = "unavailable"
	}
	if m := active.Load(); m != nil {
		if hit {
			m.thumbnailHits.Add(1)
		} else {
			m.thumbnailMisses.Add(1)
		}
		m.logger.Printf("thumbnail hit=%t result=%s", hit, result)
	}
}
