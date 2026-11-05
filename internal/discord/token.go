package discord

import (
	"context"
	"errors"
	"strings"
)

const MaxTokenLength = 2048

// NormalizeUserToken accepts only a deliberately supplied raw credential. It does
// not access browsers, files, the clipboard, or environment variables.
func NormalizeUserToken(input string) (string, error) {
	token := strings.TrimSpace(input)
	if token == "" {
		return "", errors.New("enter your own Discord user-account token")
	}
	lower := strings.ToLower(token)
	if strings.HasPrefix(lower, "bot ") {
		return "", errors.New("bot tokens are not supported; use your own user-account token")
	}
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "authorization:") {
		return "", errors.New("enter only the raw user-account token, without an Authorization or Bearer prefix")
	}
	if len(token) > MaxTokenLength {
		return "", errors.New("the entered token exceeds the supported credential length")
	}
	for _, b := range []byte(token) {
		if b <= 32 || b >= 127 || b == '"' || b == '\'' || b == '`' {
			return "", errors.New("enter a raw token without quotes, whitespace, or control characters")
		}
	}
	return token, nil
}

func ValidateChatUser(user User) error {
	if user.Bot {
		return errors.New("bot accounts are not supported; sign in with your own Discord user account")
	}
	if user.ID == "" {
		return errors.New("Discord did not return a valid user account; nothing was saved")
	}
	return nil
}

// ValidateUserSession performs a read-only identity lookup. It never stores a
// credential, opens a gateway, or sends a message.
func (c *Client) ValidateUserSession(ctx context.Context) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	user, err := c.Me(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return User{}, ctx.Err()
		}
		return User{}, err
	}
	if err = ValidateChatUser(user); err != nil {
		return User{}, err
	}
	return user, nil
}
