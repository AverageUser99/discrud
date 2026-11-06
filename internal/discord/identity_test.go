package discord

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestWebIdentityProfile(t *testing.T) {
	const expected = `{"os":"Windows","browser":"Firefox","device":"","device_model_name":"","system_locale":"en-US","has_client_mods":false,"browser_user_agent":"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:159.0) Gecko/20100101 Firefox/159.0","browser_version":"159.0","os_version":"10","referrer":"https://duckduckgo.com/","referring_domain":"duckduckgo.com","search_engine":"duckduckgo","referrer_current":"https://discord.com/","referring_domain_current":"discord.com","release_channel":"stable","client_build_number":663810,"client_event_source":null,"client_app_state":"unfocused"}`
	var want, got map[string]any
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	encoded := superProperties()
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatal(err)
	}
	if encoded != superProperties() {
		t.Fatal("launch properties changed between requests")
	}
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for _, key := range []string{"client_launch_id", "launch_signature", "client_heartbeat_session_id"} {
		id, ok := got[key].(string)
		if !ok || !uuid.MatchString(id) || seen[id] {
			t.Fatalf("invalid or duplicate %s: %v", key, got[key])
		}
		seen[id] = true
		delete(got, key)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("profile mismatch\nwant: %#v\ngot: %#v", want, got)
	}
	if got["browser_user_agent"] != UserAgent {
		t.Fatal("UA mismatch")
	}
	if newSessionID() == newSessionID() {
		t.Fatal("session IDs reused")
	}
}

func TestRequestIdentityHeaders(t *testing.T) {
	zone := systemTimezone()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			t.Error("request UA does not match profile")
		}
		if r.Header.Get("X-Super-Properties") != superProperties() {
			t.Error("request properties do not match Gateway profile")
		}
		if r.Header.Get("X-Discord-Timezone") != zone {
			t.Error("request timezone does not match system")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewClient("")
	client.base = server.URL
	if err := client.request(context.Background(), http.MethodGet, "/test", nil, "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTimezoneMappings(t *testing.T) {
	for key, want := range map[string]string{
		"Pacific Standard Time": "America/Los_Angeles",
		"GMT Standard Time":     "Europe/London",
		"India Standard Time":   "Asia/Calcutta",
		"UTC":                   "Etc/UTC",
	} {
		if got := windowsTimezoneIANA[key]; got != want {
			t.Errorf("%s: got %q, want %q", key, got, want)
		}
	}
	for key, zone := range windowsTimezoneIANA {
		if _, err := time.LoadLocation(zone); err != nil {
			t.Errorf("%s: invalid IANA zone %s: %v", key, zone, err)
		}
	}
}
