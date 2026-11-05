package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Settings struct {
	CacheTTLHours float64 `json:"cache_ttl_hours"`
	CloseToTray   bool    `json:"close_to_tray"`
	LastChannel   string  `json:"last_channel,omitempty"`
	LastGuild     string  `json:"last_guild,omitempty"`
	LastOwner     string  `json:"last_owner,omitempty"`
}

func DefaultSettings() Settings { return Settings{CacheTTLHours: 24} }
func DataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Discrud"), nil
}
func LoadSettings(dir string) (Settings, error) {
	s := DefaultSettings()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return DefaultSettings(), err
	}
	if s.CacheTTLHours < 1.0/60 || s.CacheTTLHours > 720 {
		s.CacheTTLHours = 24
	}
	return s, nil
}
func SaveSettings(dir string, s Settings) error {
	if s.CacheTTLHours < 1.0/60 || s.CacheTTLHours > 720 {
		return errors.New("cache retention must be between 1 minute and 30 days")
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(dir, "settings.json"), b)
}
func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".discrud-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
