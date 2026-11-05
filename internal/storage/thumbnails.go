package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const ThumbnailLimit = 32 * 1024 * 1024
const thumbnailEntryLimit = 1024 * 1024
const thumbnailMemoryLimit = 16

type thumbnailMemory struct {
	img image.Image
	at  time.Time
}
type thumbnailFlight struct {
	done chan struct{}
	img  image.Image
	err  error
}
type ThumbnailCache struct {
	mu      sync.Mutex
	dir     string
	ttl     time.Duration
	limit   int64
	memory  map[string]thumbnailMemory
	flights map[string]*thumbnailFlight
	epoch   uint64
}

func hashed(value string) string { b := sha256.Sum256([]byte(value)); return hex.EncodeToString(b[:]) }
func OpenThumbnails(dir, owner string, ttl time.Duration) (*ThumbnailCache, error) {
	if owner == "" || ttl <= 0 {
		return nil, errors.New("invalid thumbnail cache owner/TTL")
	}
	c := &ThumbnailCache{dir: filepath.Join(dir, "thumbnails", hashed(owner)), ttl: ttl, limit: ThumbnailLimit, memory: map[string]thumbnailMemory{}, flights: map[string]*thumbnailFlight{}}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return nil, err
	}
	c.pruneLocked()
	return c, nil
}
func (c *ThumbnailCache) readLocked(key string) image.Image {
	now := time.Now()
	if m, ok := c.memory[key]; ok && now.Sub(m.at) < c.ttl {
		return m.img
	}
	delete(c.memory, key)
	path := filepath.Join(c.dir, key+".png")
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !Fresh(info.ModTime(), c.ttl) || info.Size() > thumbnailEntryLimit {
		os.Remove(path)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width > 360 || cfg.Height > 240 || cfg.Width <= 0 || cfg.Height <= 0 {
		os.Remove(path)
		return nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		os.Remove(path)
		return nil
	}
	c.rememberLocked(key, img, info.ModTime())
	return img
}
func (c *ThumbnailCache) rememberLocked(key string, img image.Image, at time.Time) {
	if len(c.memory) >= thumbnailMemoryLimit {
		oldest := ""
		var date time.Time
		for k, v := range c.memory {
			if oldest == "" || v.at.Before(date) {
				oldest, date = k, v.at
			}
		}
		delete(c.memory, oldest)
	}
	c.memory[key] = thumbnailMemory{img, at}
}

// Get coalesces concurrent loads of the same attachment. The fetch callback is
// cancellation-aware; Clear invalidates in-flight writes as well as existing files.
func (c *ThumbnailCache) Get(ctx context.Context, identity string, fetch func(context.Context) (image.Image, error)) (image.Image, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := hashed(identity)
	c.mu.Lock()
	if img := c.readLocked(key); img != nil {
		c.mu.Unlock()
		return img, true, nil
	}
	if flight := c.flights[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-flight.done:
			return flight.img, true, flight.err
		}
	}
	flight := &thumbnailFlight{done: make(chan struct{})}
	c.flights[key] = flight
	epoch := c.epoch
	c.mu.Unlock()
	img, err := fetch(ctx)
	if err == nil {
		err = ctx.Err()
	}
	var data []byte
	if err == nil && img != nil {
		bounds := img.Bounds()
		if bounds.Dx() > 360 || bounds.Dy() > 240 {
			err = errors.New("thumbnail dimensions exceeded cache limit")
		} else {
			var out bytes.Buffer
			if encodeErr := png.Encode(&out, img); encodeErr == nil && out.Len() <= thumbnailEntryLimit {
				data = out.Bytes()
			}
		}
	}
	c.mu.Lock()
	if err == nil && img != nil && c.epoch == epoch {
		c.rememberLocked(key, img, time.Now())
		if len(data) > 0 {
			if atomicWrite(filepath.Join(c.dir, key+".png"), data) == nil {
				c.pruneLocked()
			}
		}
	}
	flight.img, flight.err = img, err
	delete(c.flights, key)
	close(flight.done)
	c.mu.Unlock()
	return img, false, err
}
func (c *ThumbnailCache) pruneLocked() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		size int64
		at   time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".png" || !Fresh(info.ModTime(), c.ttl) || info.Size() > thumbnailEntryLimit {
			os.Remove(filepath.Join(c.dir, e.Name()))
			continue
		}
		files = append(files, file{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if total <= c.limit {
			break
		}
		os.Remove(filepath.Join(c.dir, f.name))
		total -= f.size
		delete(c.memory, f.name[:len(f.name)-4])
	}
}
func (c *ThumbnailCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	c.memory = map[string]thumbnailMemory{}
	return os.RemoveAll(c.dir)
}
func (c *ThumbnailCache) SetTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ttl = ttl
	c.pruneLocked()
	for k, m := range c.memory {
		if !Fresh(m.at, ttl) {
			delete(c.memory, k)
		}
	}
}
