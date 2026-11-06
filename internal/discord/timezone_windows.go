package discord

import "golang.org/x/sys/windows/registry"

// Use the configured Windows zone, not its current abbreviation/UTC offset:
// those are ambiguous and change with daylight saving time.
func systemTimezone() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	name, _, err := key.GetStringValue("TimeZoneKeyName")
	if err != nil {
		return ""
	}
	return windowsTimezoneIANA[name]
}
