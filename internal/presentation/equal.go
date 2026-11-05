package presentation

import (
	"reflect"

	"discrud/internal/discord"
)

// SameMessage compares renderable message data without treating equivalent
// RFC3339 time zones or JSON's nil/empty slices as a visible message change.
// Cache JSON can normalize +00:00 to Z even though both denote the same instant.
func SameMessage(a, b discord.Message) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return false
	}
	b.Timestamp = a.Timestamp
	if a.EditedTimestamp != nil && b.EditedTimestamp != nil {
		if !a.EditedTimestamp.Equal(*b.EditedTimestamp) {
			return false
		}
		b.EditedTimestamp = a.EditedTimestamp
	}
	if a.ReferencedMessage != nil && b.ReferencedMessage != nil {
		if !SameMessage(*a.ReferencedMessage, *b.ReferencedMessage) {
			return false
		}
		b.ReferencedMessage = a.ReferencedMessage
	}
	if len(a.Attachments) == 0 && len(b.Attachments) == 0 {
		b.Attachments = a.Attachments
	}
	if len(a.Embeds) == 0 && len(b.Embeds) == 0 {
		b.Embeds = a.Embeds
	}
	if len(a.Mentions) == 0 && len(b.Mentions) == 0 {
		b.Mentions = a.Mentions
	}
	if len(a.MentionRoles) == 0 && len(b.MentionRoles) == 0 {
		b.MentionRoles = a.MentionRoles
	}
	return reflect.DeepEqual(a, b)
}
