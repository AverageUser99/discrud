package storage

import (
	"sort"
	"time"

	"discrud/internal/discord"
)

const HistoryPageSize = 50

// Page membership is proof of continuity. A sorted bag of cached messages is
// not: it may contain separate, non-adjacent visits to a conversation.
type historyPage struct {
	Channel   string    `json:"channel"`
	Before    string    `json:"before,omitempty"`
	IDs       []string  `json:"ids"`
	FetchedAt time.Time `json:"fetched_at"`
	End       bool      `json:"end"`
}

// RememberPage records an authoritative REST page after Put/ReconcileLatest.
// Reads never renew its age. Gateway patches may update entries independently.
func (c *Cache) RememberPage(ch, before string, messages []discord.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ordered := append([]discord.Message(nil), messages...)
	sort.Slice(ordered, func(i, j int) bool { return messageLess(ordered[i], ordered[j]) })
	p := historyPage{Channel: ch, Before: before, FetchedAt: c.now(), End: len(messages) < HistoryPageSize}
	for _, m := range ordered {
		if m.ChannelID != ch || m.ID == "" {
			return
		}
		p.IDs = append(p.IDs, m.ID)
	}
	c.pages[cacheKey(ch, before)] = p
	c.changedLocked()
	c.prunePagesLocked()
}

func (c *Cache) prunePagesLocked() {
	now := c.now()
	byChannel := map[string][]string{}
	for key, p := range c.pages {
		valid := p.Channel != "" && !p.FetchedAt.IsZero() && now.Sub(p.FetchedAt) < c.ttl && !p.FetchedAt.After(now.Add(time.Minute))
		for _, id := range p.IDs {
			if _, ok := c.entries[cacheKey(p.Channel, id)]; !ok {
				valid = false
				break
			}
		}
		if !valid {
			delete(c.pages, key)
			c.changedLocked()
			continue
		}
		byChannel[p.Channel] = append(byChannel[p.Channel], key)
	}
	// Includes empty end-of-history pages; metadata cannot grow without bound.
	for _, keys := range byChannel {
		if len(keys) <= 64 {
			continue
		}
		sort.Slice(keys, func(i, j int) bool { return c.pages[keys[i]].FetchedAt.Before(c.pages[keys[j]].FetchedAt) })
		for _, key := range keys[:len(keys)-64] {
			delete(c.pages, key)
			c.changedLocked()
		}
	}
}

// CachedPage selects a contiguous first-paint page, never a sorted union of
// unrelated visits. The caller can apply its native-control window limit.
func (c *Cache) CachedPage(ch, anchor string) ([]discord.Message, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	p, found := c.pages[cacheKey(ch, "")]
	if anchor != "" {
		found = false
		for _, candidate := range c.pages {
			if candidate.Channel != ch || found && !candidate.FetchedAt.After(p.FetchedAt) {
				continue
			}
			for _, id := range candidate.IDs {
				if id == anchor {
					p, found = candidate, true
					break
				}
			}
		}
	}
	if !found {
		return nil, false
	}
	var messages []discord.Message
	for _, id := range p.IDs {
		messages = append(messages, c.entries[cacheKey(ch, id)].Message)
	}
	return messages, true
}

// Before returns only a proven contiguous page (or its hidden prefix). Missing,
// expired, deleted or size-evicted members force a REST fetch, never a silent gap.
func (c *Cache) Before(ch, before string) (messages []discord.Message, hit, end bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked()
	p, exact := c.pages[cacheKey(ch, before)]
	count := len(p.IDs)
	if !exact {
		var newest time.Time
		for _, candidate := range c.pages {
			if candidate.Channel != ch || !candidate.FetchedAt.After(newest) {
				continue
			}
			for i, id := range candidate.IDs {
				if id == before && i > 0 {
					p, count, newest = candidate, i, candidate.FetchedAt
					break
				}
			}
		}
		if newest.IsZero() {
			return nil, false, false
		}
	}
	for _, id := range p.IDs[:count] {
		messages = append(messages, c.entries[cacheKey(ch, id)].Message)
	}
	return messages, true, p.End
}
