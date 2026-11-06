//go:build !windows

package discord

import (
	"strings"
	"time"
)

func systemTimezone() string {
	name := time.Local.String()
	if name == "UTC" || strings.Contains(name, "/") {
		return name
	}
	// Never invent a timezone when the OS does not expose an IANA identifier.
	return ""
}
