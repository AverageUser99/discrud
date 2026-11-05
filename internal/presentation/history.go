package presentation

import "discrud/internal/discord"

// OlderPageWindow selects the messages nearest the current before cursor.
// REST pages arrive newest-first; slicing them before sorting skips a gap.
func OlderPageWindow(page []discord.Message, limit int) []discord.Message {
	ordered := append([]discord.Message(nil), page...)
	SortMessages(ordered)
	return CachedWindow(ordered, "", limit)
}

func MessagesOverlap(a, b []discord.Message) bool {
	ids := make(map[string]bool, len(a))
	for _, m := range a {
		ids[m.ID] = true
	}
	for _, m := range b {
		if ids[m.ID] {
			return true
		}
	}
	return false
}

// ReconcileHistoryWindow validates the recent portion without joining disjoint
// history windows. Deep readers must not see a fabricated gap to today's page.
func ReconcileHistoryWindow(cached, fetched, during []discord.Message) []discord.Message {
	if MessagesOverlap(cached, fetched) {
		return ReconcileRecent(cached, fetched, during, true)
	}
	if len(fetched) == 0 {
		return Merge(nil, during, true)
	}
	SortMessages(fetched)
	cutoff := fetched[0]
	var retained []discord.Message
	for _, m := range cached {
		if m.Timestamp.Before(cutoff.Timestamp) || m.Timestamp.Equal(cutoff.Timestamp) && (len(m.ID) < len(cutoff.ID) || len(m.ID) == len(cutoff.ID) && m.ID < cutoff.ID) {
			retained = append(retained, m)
		}
	}
	return Merge(retained, during, true)
}
