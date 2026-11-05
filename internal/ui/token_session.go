package ui

import (
	"context"

	"discrud/internal/discord"
)

type sessionSaver interface{ Save(string) error }

// Called only after Discord's successful identity lookup, on the active dialog's
// UI thread. Cancelled dialogs and invalid/bot accounts must not replace a session.
func rememberUserSession(ctx context.Context, user discord.User, token string, vault sessionSaver) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := discord.ValidateChatUser(user); err != nil {
		return err
	}
	normalized, err := discord.NormalizeUserToken(token)
	if err != nil {
		return err
	}
	return vault.Save(normalized)
}
