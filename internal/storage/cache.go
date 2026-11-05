package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"discrud/internal/discord"
)

const CacheLimit = 50 * 1024 * 1024
const channelLimit = 2000

type cacheEntry struct {
	Message   discord.Message `json:"message"`
	FetchedAt time.Time       `json:"fetched_at"`
}
type cacheFile struct {
	Owner   string                 `json:"owner"`
	Entries map[string]cacheEntry  `json:"entries"`
	Pages   map[string]historyPage `json:"pages,omitempty"`
}
type Cache struct {
	mu          sync.Mutex
	flushMu     sync.Mutex
	version     uint64
	writeFile   func(string, []byte) error
	path, owner string
	ttl         time.Duration
	limit       int
	entries     map[string]cacheEntry
	pages       map[string]historyPage
	now         func() time.Time
	dirty       bool
}

func OpenCache(dir, owner string, ttl time.Duration, limit int) (*Cache, error) {
	if limit < 128 {
		return nil, errors.New("cache limit is too small")
	}
	if ttl <= 0 {
		return nil, errors.New("invalid cache TTL")
	}
	c := &Cache{path: filepath.Join(dir, "messages.json"), owner: owner, ttl: ttl, limit: limit, entries: map[string]cacheEntry{}, pages: map[string]historyPage{}, now: time.Now}
	f, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if info.Size() > int64(limit) {
		_ = f.Close() // Windows cannot replace a destination that is still open.
		c.changedLocked()
		return c, c.Flush()
	}
	var data cacheFile
	if err = json.NewDecoder(f).Decode(&data); err != nil {
		_ = f.Close()
		c.changedLocked()
		_ = c.Flush()
		return c, errors.New("message cache was corrupt; it was reset")
	}
	if err = f.Close(); err != nil {
		return c, err
	}
	if data.Owner == owner && data.Entries != nil {
		c.entries = data.Entries
		if data.Pages != nil {
			c.pages = data.Pages
		}
	} else {
		c.changedLocked()
	}
	c.pruneLocked()
	return c, c.Flush()
}
func (c *Cache) pruneLocked() {
	now := c.now()
	for id, e := range c.entries {
		if e.Message.ID == "" || e.Message.ChannelID == "" || e.FetchedAt.IsZero() || now.Sub(e.FetchedAt) >= c.ttl || e.FetchedAt.After(now.Add(time.Minute)) {
			delete(c.entries, id)
			c.changedLocked()
		}
	}
	c.prunePagesLocked()
}
func cacheKey(ch, id string) string { return ch + ":" + id }
func (c *Cache) Put(messages []discord.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	now := c.now()
	for _, m := range messages {
		if m.ID != "" && m.ChannelID != "" {
			c.entries[cacheKey(m.ChannelID, m.ID)] = cacheEntry{m, now}
			c.changedLocked()
		}
	}
	// Keep recently fetched history, not just recently sent messages. Otherwise
	// fetching deep history immediately evicts the very page we just loaded.
	byChannel := map[string][]cacheEntry{}
	for _, e := range c.entries {
		byChannel[e.Message.ChannelID] = append(byChannel[e.Message.ChannelID], e)
	}
	for _, list := range byChannel {
		if len(list) <= channelLimit {
			continue
		}
		sort.Slice(list, func(i, j int) bool {
			if !list[i].FetchedAt.Equal(list[j].FetchedAt) {
				return list[i].FetchedAt.Before(list[j].FetchedAt)
			}
			return messageLess(list[i].Message, list[j].Message)
		})
		for _, e := range list[:len(list)-channelLimit] {
			delete(c.entries, cacheKey(e.Message.ChannelID, e.Message.ID))
			c.changedLocked()
		}
	}
	c.prunePagesLocked()
}
func messageLess(a, b discord.Message) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	if len(a.ID) != len(b.ID) {
		return len(a.ID) < len(b.ID)
	}
	return a.ID < b.ID
}
func (c *Cache) Get(ch string) []discord.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	var out []discord.Message
	for _, e := range c.entries {
		if e.Message.ChannelID == ch {
			out = append(out, e.Message)
		}
	}
	sort.Slice(out, func(i, j int) bool { return messageLess(out[i], out[j]) })
	return out
}

// ReconcileLatest removes messages missing from the fetched recent page, while
// protecting newer Gateway events observed during the request. Older history is
// left TTL-bound rather than falsely marked freshly fetched.
func (c *Cache) ReconcileLatest(ch string, page []discord.Message, protected map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := map[string]bool{}
	var cutoff discord.Message
	for _, m := range page {
		ids[m.ID] = true
		if cutoff.ID == "" || messageLess(m, cutoff) {
			cutoff = m
		}
	}
	for key, e := range c.entries {
		m := e.Message
		if m.ChannelID == ch && !ids[m.ID] && !protected[m.ID] && (cutoff.ID == "" || !messageLess(m, cutoff)) {
			delete(c.entries, key)
			c.changedLocked()
		}
	}
}

func (c *Cache) Delete(ch, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, cacheKey(ch, id))
	c.changedLocked()
}
func (c *Cache) SetTTL(ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ttl = ttl
	c.pruneLocked()
	c.changedLocked()
}
func (c *Cache) Clear() error {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
	c.pages = map[string]historyPage{}
	c.version++
	c.dirty = false
	if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.changedLocked()
		return err
	}
	return nil
}
func (c *Cache) changedLocked() { c.dirty, c.version = true, c.version+1 }

func (c *Cache) Flush() error {
	// Serialize file replacement/clear, but do not hold the read lock during
	// large JSON encodes, size eviction, or disk I/O. Channel switches stay hot.
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	c.mu.Lock()
	c.pruneLocked()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	version := c.version
	snapshot := &Cache{owner: c.owner, limit: c.limit, ttl: c.ttl, now: c.now, entries: make(map[string]cacheEntry, len(c.entries)), pages: make(map[string]historyPage, len(c.pages))}
	for key, e := range c.entries {
		snapshot.entries[key] = e
	}
	for key, p := range c.pages {
		snapshot.pages[key] = p
	}
	writeFile := c.writeFile
	if writeFile == nil {
		writeFile = atomicWrite
	}
	c.mu.Unlock()
	b, err := snapshot.encodeBounded()
	if err != nil {
		return err
	}
	if err = writeFile(c.path, b); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version == version {
		c.entries, c.pages = snapshot.entries, snapshot.pages
		c.dirty = false
	}
	// A concurrent Put/Delete stays dirty for the next flush; never replace its
	// newer data with the older disk snapshot.
	return nil
}

func (c *Cache) encodeBounded() ([]byte, error) {
	data := cacheFile{Owner: c.owner, Entries: c.entries, Pages: c.pages}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(b) > c.limit {
		keys := make([]string, 0, len(c.entries))
		for id := range c.entries {
			keys = append(keys, id)
		}
		sort.Slice(keys, func(i, j int) bool { return c.entries[keys[i]].FetchedAt.Before(c.entries[keys[j]].FetchedAt) })
		// Evict in batches so a full cache does not require one complete marshal per entry.
		for len(b) > c.limit && len(keys) > 0 {
			count := len(keys) / 8
			if count < 1 {
				count = 1
			}
			for _, k := range keys[:count] {
				delete(c.entries, k)
			}
			keys = keys[count:]
			c.prunePagesLocked()
			b, err = json.Marshal(data)
			if err != nil {
				return nil, err
			}
		}
	}
	// Empty end markers have no message entries to evict. They still count
	// against the same hard file budget.
	if len(b) > c.limit {
		keys := make([]string, 0, len(c.pages))
		for key := range c.pages {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return c.pages[keys[i]].FetchedAt.Before(c.pages[keys[j]].FetchedAt) })
		for _, key := range keys {
			delete(c.pages, key)
			b, err = json.Marshal(data)
			if err != nil {
				return nil, err
			}
			if len(b) <= c.limit {
				break
			}
		}
	}
	return b, nil
}
func (c *Cache) Count() int { c.mu.Lock(); defer c.mu.Unlock(); c.pruneLocked(); return len(c.entries) }
