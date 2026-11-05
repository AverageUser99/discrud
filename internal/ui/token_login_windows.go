//go:build windows

package ui

import (
	"context"

	"discrud/internal/discord"
	"discrud/internal/storage"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *App) acceptAccountRisk() bool {
	if a.riskAccepted {
		return true
	}
	if walk.MsgBox(a.window, "Unofficial client — account risk", "Discord may restrict or suspend accounts used with unofficial clients. Continue only if you accept this risk.\n\nUse only your own account. Never share your token or login QR, and never use a token supplied by someone else.", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return false
	}
	a.riskAccepted = true
	return true
}

func (a *App) beginTokenLogin() {
	if a.opt.Demo || !a.acceptAccountRisk() {
		return
	}
	// Changing login methods invalidates the old QR and all its pending callbacks.
	a.newSession()
	a.scan.SetEnabled(true)
	a.qr.SetVisible(false)
	a.qrText.SetText("Manual token sign-in selected. No new session has been saved.")
	client, err := a.runTokenDialog()
	if err != nil {
		a.qrText.SetText(err.Error())
		return
	}
	if client != nil {
		a.connect(client)
	}
}

func (a *App) runTokenDialog() (*discord.Client, error) {
	ctx, cancelValidation := context.WithCancel(a.session)
	defer cancelValidation()
	generation := a.generation
	var dlg *walk.Dialog
	var entry *walk.LineEdit
	var status *walk.TextLabel
	var signIn, closeButton *walk.PushButton
	var authenticated *discord.Client
	submitting := false
	submit := func() {
		if a.opt.Demo {
			status.SetText("Live sign-in is disabled in demo mode.")
			return
		}
		if submitting {
			return
		}
		token, err := discord.NormalizeUserToken(entry.Text())
		entry.SetText("") // Do not leave a credential in the native control while checking it.
		if err != nil {
			status.SetText(err.Error())
			entry.SetFocus()
			return
		}
		client := discord.NewClient(token)
		submitting = true
		entry.SetEnabled(false)
		signIn.SetEnabled(false)
		status.SetText("Validating your account with Discord. Nothing has been saved yet...")
		go func() {
			user, err := client.ValidateUserSession(ctx)
			a.post(func() {
				if ctx.Err() != nil || a.generation != generation {
					return
				}
				if err == nil {
					err = rememberUserSession(ctx, user, token, storage.SessionVault())
				}
				if err != nil {
					submitting = false
					entry.SetEnabled(true)
					signIn.SetEnabled(true)
					status.SetText("Sign-in failed. Nothing was saved. " + err.Error())
					entry.SetFocus()
					return
				}
				authenticated = client
				dlg.Accept()
			})
		}()
	}
	err := (Dialog{
		AssignTo: &dlg, Title: "Discrud " + discord.ClientVersion + " — existing user token",
		Size: Size{Width: 640, Height: 360}, MinSize: Size{Width: 540, Height: 310},
		DefaultButton: &signIn, CancelButton: &closeButton, Layout: VBox{Spacing: 12},
		Children: []Widget{
			Label{Text: "Sign in with your own existing token", Font: Font{Family: "ComicShannsMono Nerd Font Mono", PointSize: 12, Bold: true}},
			TextLabel{Text: "A token grants access to your account. Enter it only in this local window — never in chat, screenshots, logs, or a support message. Bot and OAuth Bearer tokens are not accepted."},
			LineEdit{AssignTo: &entry, PasswordMode: true, MaxLength: discord.MaxTokenLength, ToolTipText: "Raw user-account token. Input is masked and is cleared when submitted or cancelled."},
			TextLabel{AssignTo: &status, Text: "The app validates the account before saving the session in Windows Credential Manager. It never extracts credentials from another application."},
			TextLabel{Text: "If you paste a token, clear your clipboard afterward. Discrud does not read or clear your clipboard automatically."},
			VSpacer{}, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				HSpacer{}, PushButton{AssignTo: &signIn, Text: "Validate and sign in", OnClicked: submit},
				PushButton{AssignTo: &closeButton, Text: "Cancel", OnClicked: func() { cancelValidation(); entry.SetText(""); dlg.Cancel() }},
			}},
		},
	}).Create(a.window)
	if err != nil {
		return nil, err
	}
	defer dlg.Dispose()
	dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) { cancelValidation(); entry.SetText("") })
	if a.opt.Smoke && a.opt.Demo {
		// A synthetic value only: test native masking/cancel without contacting Discord or the vault.
		a.post(func() {
			a.smokeTokenMasked = entry.PasswordMode()
			entry.SetText("synthetic-smoke-placeholder")
			dlg.Cancel()
		})
	}
	dlg.Run()
	entry.SetText("")
	if a.opt.Smoke && a.opt.Demo {
		a.smokeTokenCleared = entry.Text() == ""
	}
	return authenticated, nil
}
