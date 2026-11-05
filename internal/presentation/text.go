package presentation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"

	"discrud/internal/discord"
)

var mention = regexp.MustCompile(`<(@!?|@&|#)([0-9]+)>`)

func ResolveMentions(text string, users map[string]discord.User, roles map[string]string, channels map[string]string) string {
	return mention.ReplaceAllStringFunc(text, func(token string) string {
		v := mention.FindStringSubmatch(token)
		switch v[1] {
		case "@", "@!":
			if u, ok := users[v[2]]; ok {
				return "@" + u.Name()
			}
			return "@user-" + v[2]
		case "@&":
			if name := roles[v[2]]; name != "" {
				return "@" + name
			}
			return "@role-" + v[2]
		case "#":
			if name := channels[v[2]]; name != "" {
				return "#" + name
			}
			return "#channel-" + v[2]
		}
		return token
	})
}
func escapeRTF(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '\\', '{', '}':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\line `)
		case '\r':
		case '\t':
			b.WriteString(`\tab `)
		default:
			if r >= 32 && r < 127 {
				b.WriteRune(r)
			} else if r >= 32 {
				for _, u := range utf16.Encode([]rune{r}) {
					fmt.Fprintf(&b, `\u%d?`, int16(u))
				}
			}
		}
	}
	return b.String()
}

// RTF produces a restricted document, never accepting RTF/HTML from message text.
func RTF(text string) string {
	return `{\rtf1\ansi\deff0{\fonttbl{\f0 ComicShannsMono Nerd Font Mono;}{\f1 ComicShannsMono Nerd Font Mono;}}\viewkind4\uc1\pard\f0\fs20 ` + format(text, 0) + `}`
}
func format(text string, depth int) string {
	if depth > 5 {
		return escapeRTF(text)
	}
	type style struct {
		mark, open, close string
		literal           bool
	}
	styles := []style{{"```", `{\f1 `, "}", true}, {"`", `{\f1 `, "}", true}, {"**", `{\b `, "}", false}, {"__", `{\ul `, "}", false}, {"~~", `{\strike `, "}", false}, {"*", `{\i `, "}", false}}
	var out strings.Builder
	for len(text) > 0 {
		first := -1
		which := -1
		end := 0
		for i, s := range styles {
			start := strings.Index(text, s.mark)
			if start < 0 {
				continue
			}
			finish := strings.Index(text[start+len(s.mark):], s.mark)
			if finish < 0 {
				continue
			}
			if first < 0 || start < first {
				first = start
				which = i
				end = start + len(s.mark) + finish
			}
		}
		if first < 0 {
			out.WriteString(escapeRTF(text))
			break
		}
		s := styles[which]
		out.WriteString(escapeRTF(text[:first]))
		inner := text[first+len(s.mark) : end]
		out.WriteString(s.open)
		if s.literal {
			out.WriteString(escapeRTF(inner))
		} else {
			out.WriteString(format(inner, depth+1))
		}
		out.WriteString(s.close)
		text = text[end+len(s.mark):]
	}
	return out.String()
}

// Merge preserves newer revisions already received while an HTTP history request was in flight.
func Merge(existing, incoming []discord.Message, preferIncoming bool) []discord.Message {
	index := map[string]int{}
	out := append([]discord.Message(nil), existing...)
	for i, m := range out {
		index[m.ID] = i
	}
	for _, m := range incoming {
		if i, ok := index[m.ID]; ok {
			if preferIncoming {
				out[i] = m
			}
		} else {
			index[m.ID] = len(out)
			out = append(out, m)
		}
	}
	SortMessages(out)
	return out
}
