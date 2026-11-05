package presentation

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf16"

	"discrud/internal/discord"
)

func SortMessages(v []discord.Message) {
	sort.SliceStable(v, func(i, j int) bool {
		a, b := v[i], v[j]
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if len(a.ID) != len(b.ID) {
			return len(a.ID) < len(b.ID)
		}
		return a.ID < b.ID
	})
}
func PatchMessage(old discord.Message, patch json.RawMessage) (discord.Message, error) {
	b, err := json.Marshal(old)
	if err != nil {
		return old, err
	}
	var base, delta map[string]json.RawMessage
	if err = json.Unmarshal(b, &base); err != nil {
		return old, err
	}
	if err = json.Unmarshal(patch, &delta); err != nil {
		return old, err
	}
	for k, v := range delta {
		base[k] = v
	}
	b, err = json.Marshal(base)
	if err != nil {
		return old, err
	}
	var next discord.Message
	err = json.Unmarshal(b, &next)
	return next, err
}

// MentionQuery uses UTF-16 offsets, matching Windows edit controls (including emoji).
func MentionQuery(text string, caret int) (start int, query string, kind byte, ok bool) {
	units := utf16.Encode([]rune(text))
	if caret < 0 || caret > len(units) {
		return
	}
	prefix := string(utf16.Decode(units[:caret]))
	idx := strings.LastIndexAny(prefix, "@#")
	if idx < 0 {
		return
	}
	if idx > 0 {
		r := []rune(prefix[:idx])
		last := r[len(r)-1]
		if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
			return
		}
	}
	query = prefix[idx+1:]
	if strings.ContainsAny(query, " \r\n\t<>") {
		return
	}
	start = len(utf16.Encode([]rune(prefix[:idx])))
	return start, query, prefix[idx], true
}
