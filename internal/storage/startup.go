package storage

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"discrud/internal/discord"
)

const StartupLimit = 2 * 1024 * 1024
const MetadataTTL = 15 * time.Minute

type ReadingPosition struct {
	Channel      string `json:"channel"`
	Anchor       string `json:"anchor,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	FollowLatest bool   `json:"follow_latest"`
}

// Metadata is a presentation hint, never proof of identity or current access.
// Message contents stay in the separately bounded message cache.
type StartupSnapshot struct {
	Owner       string                       `json:"owner"`
	SavedAt     time.Time                    `json:"saved_at"`
	LastChannel discord.Channel              `json:"last_channel"`
	Position    ReadingPosition              `json:"position"`
	Guilds      []discord.Guild              `json:"guilds"`
	Channels    []discord.Channel            `json:"channels"`
	GuildsAt    time.Time                    `json:"guilds_at"`
	DMsAt       time.Time                    `json:"dms_at"`
	ChannelsAt  map[string]time.Time         `json:"channels_at"`
	RolesAt     map[string]time.Time         `json:"roles_at"`
	Roles       map[string]map[string]string `json:"roles"`
	MyRoles     map[string]map[string]bool   `json:"my_roles"`
}

func Fresh(at time.Time, ttl time.Duration) bool {
	now := time.Now()
	return ttl > 0 && !at.IsZero() && !at.After(now.Add(time.Minute)) && now.Sub(at) < ttl
}
func LoadStartup(dir, owner string, ttl time.Duration) (StartupSnapshot, error) {
	var s StartupSnapshot
	f, err := os.Open(filepath.Join(dir, "startup.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, StartupLimit+1))
	if err != nil {
		return s, err
	}
	if len(b) > StartupLimit {
		return s, errors.New("startup cache exceeded its limit")
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return StartupSnapshot{}, errors.New("startup cache is invalid")
	}
	if owner == "" || s.Owner != owner || !Fresh(s.SavedAt, ttl) {
		return StartupSnapshot{}, nil
	}
	if len(s.Channels) > 10000 || len(s.Guilds) > 5000 {
		return StartupSnapshot{}, errors.New("startup metadata exceeded its limit")
	}
	return s, nil
}
func SaveStartup(dir string, s StartupSnapshot) error {
	if s.Owner == "" {
		return errors.New("startup cache requires an account")
	}
	s.SavedAt = time.Now()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(b) > StartupLimit {
		return errors.New("startup cache exceeded its limit")
	}
	return atomicWrite(filepath.Join(dir, "startup.json"), b)
}
func ClearStartup(dir string) error {
	err := os.Remove(filepath.Join(dir, "startup.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
