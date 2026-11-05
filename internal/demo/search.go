package demo

import (
	"context"
	"strings"

	"discrud/internal/discord"
)

func (b *Backend) SearchMessages(ctx context.Context, q discord.SearchQuery) (discord.SearchResults, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var matches []discord.Message
	messages := b.messages[q.ChannelID]
	for i := len(messages) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return discord.SearchResults{}, err
		}
		m := messages[i]
		if q.Content != "" && !strings.Contains(strings.ToLower(m.Content), strings.ToLower(q.Content)) {
			continue
		}
		if q.AuthorID != "" && m.Author.ID != q.AuthorID {
			continue
		}
		if q.MentionID != "" {
			found := false
			for _, u := range m.Mentions {
				if u.ID == q.MentionID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		matches = append(matches, m)
	}
	result := discord.SearchResults{Total: len(matches)}
	if q.Offset < 0 || q.Offset >= len(matches) {
		return result, nil
	}
	end := q.Offset + 25
	if end > len(matches) {
		end = len(matches)
	}
	result.Messages = matches[q.Offset:end]
	return result, nil
}
