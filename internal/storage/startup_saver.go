package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"
)

type startupWrite struct {
	dir   string
	data  []byte
	epoch uint64
}

// StartupSaver serializes atomic disk writes and coalesces obsolete queued snapshots.
type StartupSaver struct {
	queue chan startupWrite
	done  chan struct{}
	mu    sync.Mutex
	epoch uint64
	once  sync.Once
}

func NewStartupSaver() *StartupSaver {
	s := &StartupSaver{queue: make(chan startupWrite, 1), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for w := range s.queue {
			s.mu.Lock()
			if w.epoch == s.epoch {
				_ = atomicWrite(filepath.Join(w.dir, "startup.json"), w.data)
			}
			s.mu.Unlock()
		}
	}()
	return s
}
func (s *StartupSaver) Save(dir string, snapshot StartupSnapshot) error {
	if snapshot.Owner == "" {
		return errors.New("startup cache requires an account")
	}
	snapshot.SavedAt = time.Now()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(data) > StartupLimit {
		return errors.New("startup cache exceeded its limit")
	}
	s.mu.Lock()
	epoch := s.epoch
	s.mu.Unlock()
	w := startupWrite{dir, data, epoch}
	select {
	case s.queue <- w:
	default:
		select {
		case <-s.queue:
		default:
		}
		s.queue <- w
	}
	return nil
}
func (s *StartupSaver) Clear(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	return ClearStartup(dir)
}
func (s *StartupSaver) Close() { s.once.Do(func() { close(s.queue); <-s.done }) }
