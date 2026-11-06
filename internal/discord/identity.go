package discord

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const ClientVersion = "0.1.2"

// Browser compatibility profile supplied from the web app. These versions are
// pinned metadata, not the application's runtime or an automatically current build.
const UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0"

// Launch-scoped identifiers must not replay another browser session's IDs.
var launchID = newSessionID()
var launchSignature = newSessionID()
var heartbeatSessionID = newSessionID()

func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func clientProperties() map[string]any {
	return map[string]any{
		"os":                          "Windows",
		"browser":                     "Firefox",
		"device":                      "",
		"device_model_name":           "",
		"system_locale":               "en-US",
		"has_client_mods":             false,
		"browser_user_agent":          UserAgent,
		"browser_version":             "159.0",
		"os_version":                  "10",
		"referrer":                    "https://duckduckgo.com/",
		"referring_domain":            "duckduckgo.com",
		"search_engine":               "duckduckgo",
		"referrer_current":            "https://discord.com/",
		"referring_domain_current":    "discord.com",
		"release_channel":             "stable",
		"client_build_number":         663810,
		"client_event_source":         nil,
		"client_launch_id":            launchID,
		"launch_signature":            launchSignature,
		"client_heartbeat_session_id": heartbeatSessionID,
		"client_app_state":            "unfocused",
	}
}

func superProperties() string {
	b, _ := json.Marshal(clientProperties())
	return base64.StdEncoding.EncodeToString(b)
}
