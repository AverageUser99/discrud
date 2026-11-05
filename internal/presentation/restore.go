package presentation

import "discrud/internal/discord"

// CachedWindow limits native controls at first paint without changing disk retention.
func CachedWindow(messages []discord.Message, anchor string, limit int) []discord.Message {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	start := len(messages) - limit
	if anchor != "" {
		for i, m := range messages {
			if m.ID == anchor {
				start = i - limit/3
				if start < 0 {
					start = 0
				}
				if start+limit > len(messages) {
					start = len(messages) - limit
				}
				break
			}
		}
	}
	return messages[start : start+limit]
}

// ReconcileRecent removes cached entries missing from the authoritative latest
// page. Older cached history can remain to preserve a reading anchor; it is not
// falsely treated as validated by this bounded refresh.
func ReconcileRecent(cached, fetched, during []discord.Message, keepHistory bool) []discord.Message {
	SortMessages(fetched)
	var older []discord.Message
	if keepHistory && len(fetched) > 0 {
		cutoff := fetched[0]
		for _, m := range cached {
			before := m.Timestamp.Before(cutoff.Timestamp)
			if m.Timestamp.Equal(cutoff.Timestamp) {
				before = len(m.ID) < len(cutoff.ID) || len(m.ID) == len(cutoff.ID) && m.ID < cutoff.ID
			}
			if before {
				older = append(older, m)
			}
		}
	}
	return Merge(Merge(older, fetched, true), during, true)
}

func NearestMessage(messages []discord.Message, anchor string) string {
	if len(messages) == 0 {
		return ""
	}
	for _, m := range messages {
		if m.ID == anchor {
			return anchor
		}
	}
	for _, m := range messages {
		if len(m.ID) > len(anchor) || len(m.ID) == len(anchor) && m.ID >= anchor {
			return m.ID
		}
	}
	return messages[len(messages)-1].ID
}
