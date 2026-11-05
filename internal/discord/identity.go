package discord

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
)

const ClientVersion = "0.1.2"

// Client properties are protocol metadata, not a CAPTCHA solution. Identify the
// actual application instead of claiming to be Chrome or an official Discord build.
// https://docs.discord.food/reference#client-properties
func clientProperties() map[string]any {
	osName := runtime.GOOS
	switch osName {
	case "windows":
		osName = "Windows"
	case "darwin":
		osName = "Mac OS X"
	case "linux":
		osName = "Linux"
	}
	return map[string]any{
		"os":                 osName,
		"browser":            "Discrud",
		"device":             "Discrud",
		"system_locale":      "en-US",
		"browser_user_agent": UserAgent,
		"browser_version":    ClientVersion,
		"client_version":     ClientVersion,
	}
}
func superProperties() string {
	b, _ := json.Marshal(clientProperties())
	return base64.StdEncoding.EncodeToString(b)
}
