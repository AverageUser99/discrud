// Package demo supplies an entirely local backend for exercising the UI safely.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"discrud/internal/discord"
)

type Backend struct {
	mu       sync.Mutex
	messages map[string][]discord.Message
	events   chan discord.Event
	next     uint64
	nonces   map[string]discord.Message
}

var Me = discord.User{ID: "100", Username: "you", GlobalName: "You"}
var alex = discord.User{ID: "101", Username: "alex", GlobalName: "Alex"}
var sam = discord.User{ID: "102", Username: "sam", GlobalName: "Sam"}

func New() *Backend {
	b := &Backend{messages: map[string][]discord.Message{}, events: make(chan discord.Event, 64), next: 1000, nonces: map[string]discord.Message{}}
	for i := 0; i < 65; i++ {
		author := alex
		if i%3 == 0 {
			author = Me
		}
		text := fmt.Sprintf("Demo message %d. This conversation stays entirely on your PC.", i+1)
		if i == 62 {
			text = "Welcome to Discrud. Browse channels with Ctrl+K; Enter sends, Shift+Enter adds a line."
		}
		m := discord.Message{ID: strconv.Itoa(1000 + i), ChannelID: "general", GuildID: "demo-server", Author: author, Content: text, Timestamp: time.Now().Add(time.Duration(i-65) * time.Minute)}
		b.messages["general"] = append(b.messages["general"], m)
	}
	b.next = 1100
	b.messages["general"] = append(b.messages["general"], discord.Message{ID: "1100", ChannelID: "general", GuildID: "demo-server", Author: sam, Content: "An inline image, a link https://discord.com, and a mention: <@100>. Try Reply, Edit, or Delete on a message.", Timestamp: time.Now(), Mentions: []discord.User{Me}, Attachments: []discord.Attachment{{ID: "demo-image", Filename: "sample.png", URL: "demo://sample.png", ContentType: "image/png", Width: 360, Height: 160}}})
	b.messages["general"][len(b.messages["general"])-1].Content += "\nOffline Klipy link fixture (generated color bars, not live media): https://klipy.com/gifs/scared-63"
	b.messages["dm-alex"] = []discord.Message{{ID: "900", ChannelID: "dm-alex", Author: alex, Content: "This is a one-to-one direct message.", Timestamp: time.Now()}}
	b.messages["group"] = []discord.Message{{ID: "901", ChannelID: "group", Author: sam, Content: "An existing group DM. No calls, activity panels, or distractions.", Timestamp: time.Now()}}
	return b
}
func (b *Backend) Me(context.Context) (discord.User, error) { return Me, nil }
func (b *Backend) Guilds(context.Context) ([]discord.Guild, error) {
	return []discord.Guild{{ID: "demo-server", Name: "Discrud demo"}}, nil
}
func (b *Backend) Channels(context.Context, string) ([]discord.Channel, error) {
	return []discord.Channel{{ID: "category", GuildID: "demo-server", Name: "Text channels", Type: 4}, {ID: "general", GuildID: "demo-server", ParentID: "category", Name: "general", Type: 0}, {ID: "quiet", GuildID: "demo-server", ParentID: "category", Name: "quiet-room", Type: 0, Position: 1}}, nil
}
func (b *Backend) DMs(context.Context) ([]discord.Channel, error) {
	return []discord.Channel{{ID: "dm-alex", Type: 1, Recipients: []discord.User{alex}}, {ID: "group", Type: 3, Recipients: []discord.User{alex, sam}}}, nil
}
func (b *Backend) Roles(context.Context, string) ([]discord.Role, error) {
	return []discord.Role{{ID: "200", Name: "Friends"}}, nil
}
func (b *Backend) MyRoles(context.Context, string) ([]string, error) { return []string{"200"}, nil }
func (b *Backend) Messages(ctx context.Context, ch, before string) ([]discord.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []discord.Message
	for i := len(b.messages[ch]) - 1; i >= 0; i-- {
		m := b.messages[ch][i]
		if before != "" {
			a, _ := strconv.ParseUint(m.ID, 10, 64)
			n, _ := strconv.ParseUint(before, 10, 64)
			if a >= n {
				continue
			}
		}
		out = append(out, m)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}
func (b *Backend) Send(ctx context.Context, ch string, s discord.Send) (discord.Message, error) {
	if ctx.Err() != nil {
		return discord.Message{}, ctx.Err()
	}
	if strings.TrimSpace(s.Content) == "" && len(s.Files) == 0 {
		return discord.Message{}, errors.New("empty message")
	}
	b.mu.Lock()
	if m, ok := b.nonces[s.Nonce]; ok && s.Nonce != "" {
		b.mu.Unlock()
		return m, nil
	}
	b.next++
	m := discord.Message{ID: strconv.FormatUint(b.next, 10), ChannelID: ch, Author: Me, Content: s.Content, Timestamp: time.Now(), MessageReference: s.ReplyTo}
	if ch == "general" || ch == "quiet" {
		m.GuildID = "demo-server"
	}
	if s.ReplyTo != nil {
		for _, old := range b.messages[ch] {
			if old.ID == s.ReplyTo.MessageID {
				copy := old
				m.ReferencedMessage = &copy
				if s.ReplyPing {
					m.Mentions = append(m.Mentions, old.Author)
				}
				break
			}
		}
	}
	for i, f := range s.Files {
		m.Attachments = append(m.Attachments, discord.Attachment{ID: strconv.Itoa(i), Filename: f.Name, Size: int64(len(f.Data)), URL: "demo://uploaded"})
	}
	b.messages[ch] = append(b.messages[ch], m)
	if s.Nonce != "" {
		b.nonces[s.Nonce] = m
	}
	b.mu.Unlock()
	b.emit("MESSAGE_CREATE", m)
	return m, nil
}
func (b *Backend) Edit(ctx context.Context, ch, id, text string) (discord.Message, error) {
	b.mu.Lock()
	for i, m := range b.messages[ch] {
		if m.ID == id && m.Author.ID == Me.ID {
			m.Content = text
			now := time.Now()
			m.EditedTimestamp = &now
			b.messages[ch][i] = m
			b.mu.Unlock()
			b.emit("MESSAGE_UPDATE", m)
			return m, nil
		}
	}
	b.mu.Unlock()
	return discord.Message{}, errors.New("message not found or not yours")
}
func (b *Backend) Delete(ctx context.Context, ch, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, m := range b.messages[ch] {
		if m.ID == id && m.Author.ID == Me.ID {
			b.messages[ch] = append(b.messages[ch][:i], b.messages[ch][i+1:]...)
			b.emit("MESSAGE_DELETE", map[string]string{"id": id, "channel_id": ch})
			return nil
		}
	}
	return errors.New("message not found or not yours")
}
func (b *Backend) emit(kind string, v any) {
	data, _ := json.Marshal(v)
	select {
	case b.events <- discord.Event{Type: kind, Data: data}:
	default:
	}
}
func (b *Backend) Run(ctx context.Context, emit func(discord.Event)) {
	emit(discord.Event{Status: "Demo — offline, no Discord connection"})
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-b.events:
			emit(e)
		}
	}
}
func (b *Backend) Subscribe(string, string) {}

var _ discord.Backend = (*Backend)(nil)
