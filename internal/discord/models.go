package discord

import (
	"context"
	"encoding/json"
	"time"
)

type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot,omitempty"`
}

func (u User) Name() string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	if u.Username != "" {
		return u.Username
	}
	return "Unknown user"
}

type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (g *Guild) UnmarshalJSON(b []byte) error {
	type plain Guild
	var v struct {
		plain
		Properties struct {
			Name string `json:"name"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*g = Guild(v.plain)
	if g.Name == "" {
		g.Name = v.Properties.Name
	}
	return nil
}

type Role struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Channel struct {
	ID         string `json:"id"`
	GuildID    string `json:"guild_id"`
	ParentID   string `json:"parent_id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	Position   int    `json:"position"`
	Recipients []User `json:"recipients"`
}

func (c Channel) Label() string {
	if c.Name != "" {
		return c.Name
	}
	name := ""
	for _, u := range c.Recipients {
		if name != "" {
			name += ", "
		}
		name += u.Name()
	}
	if name == "" {
		return "Conversation " + c.ID
	}
	return name
}
func (c Channel) Supported() bool { return c.Type == 0 || c.Type == 5 || c.Type == 1 || c.Type == 3 }

type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ProxyURL    string `json:"proxy_url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

// Embed media URLs are supplied by Discord. Supported provider sharing links
// may also be resolved by the separate, bounded GIF source resolver.
type EmbedMedia struct {
	ContentType string `json:"content_type,omitempty"`
	URL         string `json:"url"`
	ProxyURL    string `json:"proxy_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}
type Embed struct {
	Type        string      `json:"type"`
	URL         string      `json:"url"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Image       *EmbedMedia `json:"image,omitempty"`
	Thumbnail   *EmbedMedia `json:"thumbnail,omitempty"`
	Video       *EmbedMedia `json:"video,omitempty"`
}

type Reference struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id,omitempty"`
}
type Member struct {
	Nick string `json:"nick,omitempty"`
}

type Message struct {
	ID                string       `json:"id"`
	ChannelID         string       `json:"channel_id"`
	GuildID           string       `json:"guild_id,omitempty"`
	Author            User         `json:"author"`
	Member            *Member      `json:"member,omitempty"`
	Content           string       `json:"content"`
	Timestamp         time.Time    `json:"timestamp"`
	EditedTimestamp   *time.Time   `json:"edited_timestamp,omitempty"`
	Attachments       []Attachment `json:"attachments,omitempty"`
	Embeds            []Embed      `json:"embeds,omitempty"`
	Mentions          []User       `json:"mentions,omitempty"`
	MentionRoles      []string     `json:"mention_roles,omitempty"`
	MentionEveryone   bool         `json:"mention_everyone,omitempty"`
	MessageReference  *Reference   `json:"message_reference,omitempty"`
	ReferencedMessage *Message     `json:"referenced_message,omitempty"`
	Type              int          `json:"type"`
}

// AuthorName prefers the server-specific nickname when Discord supplies it.
func (m Message) AuthorName() string {
	if m.Member != nil && m.Member.Nick != "" {
		return m.Member.Nick
	}
	return m.Author.Name()
}

func (m Message) MentionsUser(id string, roleIDs map[string]bool) bool {
	if m.Author.ID == id || id == "" {
		return false
	}
	if m.MentionEveryone {
		return true
	}
	for _, u := range m.Mentions {
		if u.ID == id {
			return true
		}
	}
	for _, r := range m.MentionRoles {
		if roleIDs[r] {
			return true
		}
	}
	return false
}

type Upload struct {
	Name string
	Data []byte
}
type Send struct {
	Content   string
	ReplyTo   *Reference
	ReplyPing bool // Opt-in notification of the replied-to message's author.
	Files     []Upload
	Nonce     string
}
type Event struct {
	Type   string
	Data   json.RawMessage
	Status string
}

// Backend deliberately exposes only user-driven chat operations, never automation.
type Backend interface {
	Me(context.Context) (User, error)
	Guilds(context.Context) ([]Guild, error)
	Channels(context.Context, string) ([]Channel, error)
	DMs(context.Context) ([]Channel, error)
	Roles(context.Context, string) ([]Role, error)
	MyRoles(context.Context, string) ([]string, error)
	Messages(context.Context, string, string) ([]Message, error)
	Send(context.Context, string, Send) (Message, error)
	Edit(context.Context, string, string, string) (Message, error)
	Delete(context.Context, string, string) error
	Run(context.Context, func(Event))
	Subscribe(string, string)
}

type APIError struct {
	Status             int
	RetryAfter         time.Duration
	Code               int
	CodePresent        bool
	Captcha            bool
	CaptchaService     string // Allowlisted service name only; never CAPTCHA tokens or raw response text.
	TicketRejected     bool
	UnexpectedResponse bool
}
