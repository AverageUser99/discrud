package discord

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SearchQuery is scoped to one channel; user filters contain Discord user IDs.
type SearchQuery struct {
	GuildID, ChannelID, Content, AuthorID, MentionID string
	Offset                                           int
}

type SearchResults struct {
	Messages []Message
	Total    int
}

type SearchBackend interface {
	SearchMessages(context.Context, SearchQuery) (SearchResults, error)
}

func (c *Client) SearchMessages(ctx context.Context, q SearchQuery) (SearchResults, error) {
	if q.ChannelID == "" || q.Offset < 0 || q.Offset > 9975 {
		return SearchResults{}, errors.New("invalid search scope or page")
	}
	values := url.Values{}
	if q.Content != "" {
		values.Set("content", q.Content)
	}
	if q.AuthorID != "" {
		values.Set("author_id", q.AuthorID)
	}
	if q.MentionID != "" {
		values.Set("mentions", q.MentionID)
	}
	values.Set("offset", strconv.Itoa(q.Offset))
	path := "/channels/" + url.PathEscape(q.ChannelID) + "/messages/search"
	if q.GuildID != "" {
		path = "/guilds/" + url.PathEscape(q.GuildID) + "/messages/search"
		values.Set("channel_id", q.ChannelID)
	}
	var response struct {
		Messages [][]struct {
			Message
			Hit bool `json:"hit"`
		} `json:"messages"`
		Total      int     `json:"total_results"`
		RetryAfter float64 `json:"retry_after"`
	}
	if err := c.json(ctx, "GET", path+"?"+values.Encode(), nil, &response); err != nil {
		return SearchResults{}, err
	}
	if response.RetryAfter > 0 {
		return SearchResults{}, fmt.Errorf("Discord is indexing this conversation; try again in a few seconds")
	}
	out := SearchResults{Total: response.Total}
	for _, group := range response.Messages {
		if len(group) == 0 {
			continue
		}
		item := group[0]
		for _, candidate := range group {
			if candidate.Hit {
				item = candidate
				break
			}
		}
		if item.Message.GuildID == "" {
			item.Message.GuildID = q.GuildID
		}
		out.Messages = append(out.Messages, item.Message)
	}
	return out, nil
}

// ResolveSearchUser accepts a numeric ID, a mention, or an exact known username/display name.
func ResolveSearchUser(input string, users map[string]User) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if strings.HasPrefix(input, "<@") && strings.HasSuffix(input, ">") {
		input = strings.TrimSuffix(strings.TrimPrefix(input, "<@"), ">")
	}
	input = strings.TrimPrefix(strings.TrimPrefix(input, "!"), "@")
	if input != "" && strings.Trim(input, "0123456789") == "" {
		return input, nil
	}
	id := ""
	for _, u := range users {
		if strings.EqualFold(input, u.Username) || strings.EqualFold(input, u.GlobalName) {
			if id != "" && id != u.ID {
				return "", errors.New("ambiguous user name; use a user ID")
			}
			id = u.ID
		}
	}
	if id == "" {
		return "", errors.New("user not found among loaded users; use a user ID")
	}
	return id, nil
}
