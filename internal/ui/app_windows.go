//go:build windows

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"discrud/internal/demo"
	"discrud/internal/discord"
	"discrud/internal/gifplayer"
	"discrud/internal/gifsource"
	"discrud/internal/netdebug"
	"discrud/internal/storage"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var appBackgroundColor = walk.RGB(164, 164, 164)

type Options struct {
	Demo     bool
	Smoke    bool
	DataDir  string
	Shutdown <-chan struct{} // Optional process interruption; quits even when close-to-tray is enabled.
}
type navRow struct {
	label   string
	channel discord.Channel
}
type suggestion struct{ label, insert string }
type attempt struct {
	channel string
	send    discord.Send
}
type App struct {
	opt                                           Options
	window                                        *walk.MainWindow
	login, shell, drawer, editHost                *walk.Composite
	qr                                            *walk.ImageView
	qrBitmap                                      *walk.Bitmap
	qrText                                        *walk.TextLabel
	scan                                          *walk.PushButton
	servers                                       *walk.ComboBox
	filter                                        *walk.LineEdit
	conversations                                 *walk.ListBox
	header                                        *walk.Label
	history                                       *walk.ScrollView
	editor                                        *composer
	replyPing                                     *walk.CheckBox
	replyText, filesText                          *walk.Label
	suggestions                                   *walk.ListBox
	errorText                                     *walk.Label
	retry                                         *walk.PushButton
	status                                        *walk.StatusBarItem
	sendButton, attachButton, pasteButton         *walk.PushButton
	tray                                          *walk.NotifyIcon
	closed                                        atomic.Bool
	quitting                                      bool
	ctx                                           context.Context
	cancel                                        context.CancelFunc
	session                                       context.Context
	cancelSession                                 context.CancelFunc
	cancelLoad                                    context.CancelFunc
	cancelMedia                                   context.CancelFunc
	generation, loadID                            int
	backend                                       discord.Backend
	gatewayFailure                                error
	cache                                         *storage.Cache
	thumbnails                                    *storage.ThumbnailCache
	startupSaver                                  *storage.StartupSaver
	startedAt                                     time.Time
	guildsAt, dmsAt                               time.Time
	channelsAt, rolesAt                           map[string]time.Time
	guildLoading, rolesLoading                    map[string]bool
	dmsDone, startupDirty                         bool
	suppressStartupSave                           bool
	gatewayEpoch, loadedEpoch                     int
	readyRefreshPending                           bool
	cachedShown, freshShown                       bool
	cachedPresented, freshPresented               bool
	forceLatest                                   bool
	historySettled, showingCached                 bool
	channelViews                                  map[string]*channelView
	historyScrollID, historyInputID, renderSerial int
	historyPaintLock                              win.HWND
	historyPaintEpoch                             uint64
	olderDone                                     bool
	historyRetryAt                                time.Time
	restorePosition                               *storage.ReadingPosition
	savedPosition                                 storage.ReadingPosition
	previews                                      []*pendingPreview
	previewTimer                                  *time.Timer
	animationTimer                                *time.Timer
	animationEpoch                                uint64
	animations                                    map[*animatedGIF]bool
	gifBudget                                     gifplayer.Budget
	gifResolver                                   *gifsource.Resolver
	settings                                      storage.Settings
	dir                                           string
	me                                            discord.User
	guilds                                        []discord.Guild
	channels                                      map[string]discord.Channel
	guildLoaded                                   map[string]bool
	users                                         map[string]discord.User
	roles                                         map[string]map[string]string
	myRoles                                       map[string]map[string]bool
	mentions                                      map[string]int
	seenMentions                                  map[string]bool
	rows                                          []navRow
	current                                       discord.Channel
	messages                                      []discord.Message
	revisions                                     map[string]int
	revision                                      int
	atLatest, loading                             bool
	reply                                         *discord.Reference
	editing                                       string
	uploads                                       []discord.Upload
	completing                                    bool
	choices                                       []suggestion
	mentionStart, mentionEnd                      int
	sending                                       bool
	failed                                        *attempt
	cardImages                                    []*walk.Bitmap
	riskAccepted                                  bool
	smokeStarted                                  bool
	smokeTokenMasked, smokeTokenCleared           bool
	smokePosition                                 storage.ReadingPosition
	navUpdating                                   bool
	cards                                         map[string]*walk.Composite
	renderedIDs                                   []string
	renderedMessages                              map[string]discord.Message
	previewCount                                  int
	renderID                                      int
	mediaSem                                      chan struct{}
	mediaCtx                                      context.Context
	background                                    *walk.SolidColorBrush
	bold                                          *walk.Font
}

func Run(opt Options) error {
	// Resolve legacy lxn/win lazy procedures with real (nil) pointers first.
	// Walk later passes MAKEINTRESOURCE integer IDs as *uint16; resolving a
	// lazy symbol there can grow the Go stack and make the GC reject ID 7 as
	// an invalid pointer. These nil-name calls allocate no Windows resources.
	win.LoadIcon(0, nil)
	win.LoadCursor(0, nil)
	started := time.Now()
	dir := opt.DataDir
	var err error
	if dir == "" {
		if opt.Demo {
			dir = filepath.Join(os.TempDir(), "Discrud-demo")
		} else {
			dir, err = storage.DataDir()
			if err != nil {
				return err
			}
		}
	}
	settings, settingErr := storage.LoadSettings(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{opt: opt, dir: dir, settings: settings, ctx: ctx, cancel: cancel, channels: map[string]discord.Channel{}, guildLoaded: map[string]bool{}, users: map[string]discord.User{}, roles: map[string]map[string]string{}, myRoles: map[string]map[string]bool{}, mentions: map[string]int{}, seenMentions: map[string]bool{}, revisions: map[string]int{}, atLatest: true}
	a.startedAt = started
	a.startupSaver = storage.NewStartupSaver()
	defer a.startupSaver.Close()
	if err = a.create(); err != nil {
		return err
	}
	defer func() { a.disposeImages(); a.window.Dispose(); a.bold.Dispose(); a.background.Dispose() }()
	a.setupTray()
	a.window.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if a.settings.CloseToTray && !a.quitting && a.tray != nil {
			if e := a.tray.SetVisible(true); e == nil {
				*canceled = true
				a.window.Hide()
				return
			}
		}
		a.shutdown()
	})
	if settingErr != nil {
		a.status.SetText("Settings could not be read; defaults restored.")
	}
	if opt.Demo {
		a.connect(demo.New())
	} else {
		token, e := storage.SessionVault().Load()
		if e != nil {
			a.qrText.SetText(e.Error())
		} else if token != "" {
			a.connect(discord.NewClient(token))
		}
	}
	if opt.Shutdown != nil {
		go func() {
			select {
			case <-opt.Shutdown:
				a.post(a.quit)
			case <-ctx.Done():
			}
		}()
	}
	a.window.Run()
	a.shutdown()
	return nil
}
func (a *App) create() error {
	var err error
	a.background, err = walk.NewSolidColorBrush(appBackgroundColor)
	if err != nil {
		return err
	}
	a.bold, err = walk.NewFont("ComicShannsMono Nerd Font Mono", 10, walk.FontBold)
	if err != nil {
		return err
	}
	a.mediaSem = make(chan struct{}, 2)
	a.gifBudget.Limit = 128 << 20 // Shared by active decoders; off-screen players are released.
	var gifTransport http.RoundTripper = netdebug.Transport(gifsource.NewTransport())
	if a.opt.Demo {
		gifTransport = demo.GIFTransport{}
	}
	a.gifResolver = gifsource.New(&http.Client{Transport: gifTransport, Timeout: 20 * time.Second}, discord.UserAgent)
	a.animations = make(map[*animatedGIF]bool)
	err = (MainWindow{
		AssignTo: &a.window, Title: "Discrud " + discord.ClientVersion, Size: Size{Width: 900, Height: 760}, MinSize: Size{Width: 620, Height: 480}, Font: Font{Family: "ComicShannsMono Nerd Font Mono", PointSize: 10}, Icon: walk.IconApplication(), Layout: VBox{Margins: Margins{Left: 12, Top: 8, Right: 12, Bottom: 8}, Spacing: 8},
		MenuItems: []MenuItem{Menu{Text: "&Discrud", Items: []MenuItem{
			Action{Text: "Browse / quick switcher", Shortcut: Shortcut{Modifiers: walk.ModControl, Key: walk.KeyK}, OnTriggered: a.quickSwitch},
			Action{Text: "Search messages", Shortcut: Shortcut{Modifiers: walk.ModControl, Key: walk.KeyF}, OnTriggered: a.searchMessages},
			Action{Text: "Focus message", Shortcut: Shortcut{Modifiers: walk.ModControl, Key: walk.KeyL}, OnTriggered: func() {
				if a.editor != nil {
					a.editor.SetFocus()
				}
			}},
			Action{Text: "Settings", OnTriggered: a.showSettings}, Action{Text: "Log out", OnTriggered: a.logout}, Separator{}, Action{Text: "Quit", OnTriggered: a.quit},
		}}}, StatusBarItems: []StatusBarItem{{AssignTo: &a.status, Text: "Not connected"}},
		Children: []Widget{
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: "Browse", MaxSize: Size{Width: 90, Height: 0}, OnClicked: func() {
					if a.backend != nil {
						a.drawer.SetVisible(!a.drawer.Visible())
						if a.drawer.Visible() {
							a.filter.SetFocus()
						}
					}
				}},
				Label{AssignTo: &a.header, Text: "Discrud " + discord.ClientVersion, Font: Font{Family: "ComicShannsMono Nerd Font Mono", PointSize: 12, Bold: true}}, HSpacer{}, PushButton{Text: "Settings", OnClicked: a.showSettings},
			}},
			Composite{AssignTo: &a.login, Layout: VBox{Margins: Margins{Left: 20, Top: 20, Right: 20, Bottom: 20}, Spacing: 14}, Children: []Widget{
				VSpacer{}, Label{Text: "Just your conversations.", Font: Font{Family: "ComicShannsMono Nerd Font Mono", PointSize: 20, Bold: true}},
				TextLabel{Text: "An unofficial Discord user-account client. Discord does not support this application; using it may put your account at risk. No bot account, password entry, telemetry, or automatic messaging."},
				ImageView{AssignTo: &a.qr, MinSize: Size{Width: 280, Height: 280}, MaxSize: Size{Width: 280, Height: 280}, Mode: ImageViewModeShrink, Visible: false},
				TextLabel{AssignTo: &a.qrText, Text: "Sign in by scanning a QR code with the official Discord mobile app. Only approve login requests you initiated here."},
				PushButton{AssignTo: &a.scan, Text: "Scan QR", OnClicked: a.beginLogin},
				PushButton{Text: "Use existing user token", OnClicked: a.beginTokenLogin},
				PushButton{Text: "Forget saved session", OnClicked: a.logout}, VSpacer{},
			}},
			Composite{AssignTo: &a.shell, Visible: false, StretchFactor: 1, Layout: HBox{MarginsZero: true, Spacing: 12}, Children: []Widget{
				Composite{AssignTo: &a.drawer, Visible: false, MinSize: Size{Width: 230, Height: 0}, MaxSize: Size{Width: 260, Height: 0}, Layout: VBox{MarginsZero: true}, Children: []Widget{
					ComboBox{AssignTo: &a.servers, OnCurrentIndexChanged: a.serverChanged},
					LineEdit{AssignTo: &a.filter, CueBanner: "Filter channels / DMs", OnTextChanged: a.updateNav},
					ListBox{AssignTo: &a.conversations, StretchFactor: 1, OnItemActivated: a.openSelected, OnCurrentIndexChanged: a.openSelected},
					Label{Text: "Ctrl+K to switch · Ctrl+L to type"},
				}},
				Composite{StretchFactor: 1, Layout: VBox{MarginsZero: true, Spacing: 6}, Children: []Widget{
					Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{PushButton{Text: "Older messages", OnClicked: func() { a.loadMessages(true) }}, PushButton{Text: "Latest", OnClicked: a.latestMessages}, PushButton{Text: "Search", ToolTipText: "Search this conversation (Ctrl+F)", OnClicked: a.searchMessages}, HSpacer{}, Label{Text: "No distractions."}}},
					ScrollView{AssignTo: &a.history, MinSize: Size{Width: 200, Height: 250}, StretchFactor: 1, Layout: VBox{Margins: Margins{Left: 8, Top: 8, Right: 8, Bottom: 8}, Spacing: 8}},
					Label{AssignTo: &a.errorText, Visible: false, TextColor: walk.RGB(160, 40, 40)},
					PushButton{AssignTo: &a.retry, Text: "Retry send (check the conversation first)", Visible: false, OnClicked: a.sendMessage},
					Label{AssignTo: &a.replyText, Visible: false},
					CheckBox{AssignTo: &a.replyPing, Text: "Ping author", Visible: false, ToolTipText: "Notify the author of the message you are replying to. Off by default."},
					Label{AssignTo: &a.filesText, Visible: false},
					ListBox{AssignTo: &a.suggestions, Visible: false, MinSize: Size{Width: 0, Height: 64}, MaxSize: Size{Width: 0, Height: 96}, OnItemActivated: func() { a.completeMention() }},
					Composite{AssignTo: &a.editHost, MinSize: Size{Width: 320, Height: 32}, MaxSize: Size{Width: 0, Height: 160}, Layout: VBox{MarginsZero: true}},
					Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
						PushButton{AssignTo: &a.attachButton, Text: "Attach file", OnClicked: a.attachFile}, PushButton{AssignTo: &a.pasteButton, Text: "Paste image", OnClicked: func() { a.pasteImage() }}, PushButton{Text: "Clear / cancel", OnClicked: a.clearComposer}, HSpacer{}, Label{Text: "Enter ↵", ToolTipText: "Enter sends · Shift+Enter adds a new line"}, PushButton{AssignTo: &a.sendButton, Text: "Send", OnClicked: a.sendMessage},
					}},
				}},
			}},
		},
	}).Create()
	if err != nil {
		return err
	}
	a.window.SetBackground(a.background)
	a.editor, err = newComposer(a.editHost)
	if err != nil {
		return err
	}
	a.editor.send = a.sendMessage
	a.editor.paste = a.pasteImage
	a.editor.complete = a.completeMention
	a.editor.changed.Event().Attach(a.updateSuggestions)
	a.observeHistory()
	a.window.SizeChanged().Attach(a.refreshAnimations)
	a.window.VisibleChanged().Attach(func() { a.loadVisiblePreviews() })
	return nil
}
func (a *App) post(fn func()) {
	if a.closed.Load() {
		return
	}
	a.window.Synchronize(func() {
		if !a.closed.Load() {
			fn()
		}
	})
}
func (a *App) task(work func(context.Context) func()) {
	gen, ctx := a.generation, a.session
	if ctx == nil {
		ctx = a.ctx
	}
	go func() {
		apply := work(ctx)
		if apply != nil {
			a.post(func() {
				if a.generation == gen {
					apply()
				}
			})
		}
	}()
}
func (a *App) showError(err error) {
	if err == nil {
		return
	}
	a.errorText.SetText(err.Error())
	a.errorText.SetToolTipText(err.Error())
	a.errorText.SetVisible(true)
	a.status.SetText(err.Error())
}
func (a *App) clearError() {
	a.retry.SetVisible(false)
	if a.gatewayFailure != nil {
		a.showError(a.gatewayFailure)
	} else {
		a.errorText.SetVisible(false)
	}
}
func (a *App) newSession() {
	a.clearChannelViews()
	a.historyScrollID++
	a.olderDone = false
	a.gatewayEpoch, a.loadedEpoch = 0, 0
	a.readyRefreshPending = false
	a.suppressStartupSave = false
	a.dmsDone, a.forceLatest = false, false
	a.cachedShown, a.freshShown = false, false
	a.cachedPresented, a.freshPresented = false, false
	a.restorePosition = nil
	a.gatewayFailure = nil
	if a.cancelSession != nil {
		a.cancelSession()
	}
	if a.cancelLoad != nil {
		a.cancelLoad()
	}
	if a.cancelMedia != nil {
		a.cancelMedia()
	}
	a.generation++
	a.session, a.cancelSession = context.WithCancel(a.ctx)
}
func (a *App) beginLogin() {
	if a.opt.Demo {
		return
	}
	if !a.acceptAccountRisk() {
		return
	}
	a.newSession()
	a.scan.SetEnabled(false)
	a.qrText.SetText("Connecting to QR authentication...")
	a.qr.SetVisible(false)
	gen, ctx := a.generation, a.session
	go func() {
		token, err := discord.QRLogin(ctx, func(e discord.QREvent) {
			a.post(func() {
				if a.generation != gen {
					return
				}
				a.qrText.SetText(e.Status)
				if e.Image != nil {
					bmp, e2 := walk.NewBitmapFromImage(e.Image)
					if e2 != nil {
						a.qrText.SetText("Could not render QR code")
						return
					}
					a.qr.SetImage(bmp)
					if a.qrBitmap != nil {
						a.qrBitmap.Dispose()
					}
					a.qrBitmap = bmp
					a.qr.SetVisible(true)
				}
			})
		})
		a.post(func() {
			if a.generation != gen {
				return
			}
			a.scan.SetEnabled(true)
			if err != nil {
				a.qr.SetVisible(false)
				a.qrText.SetText(err.Error())
				return
			}
			if err = storage.SessionVault().Save(token); err != nil {
				a.qr.SetVisible(false)
				a.qrText.SetText(err.Error())
				return
			}
			a.connect(discord.NewClient(token))
		})
	}()
}
func (a *App) connect(backend discord.Backend) {
	a.newSession()
	a.backend = backend
	a.status.SetText("Validating saved account...")
	a.scan.SetEnabled(false)
	a.qr.SetVisible(false)
	a.qrText.SetText("Restoring your session...")
	a.clearError()
	ttl, dir := time.Duration(a.settings.CacheTTLHours*float64(time.Hour)), a.dir
	netdebug.Startup("begin", a.startedAt, 0)
	a.task(func(ctx context.Context) func() {
		me, err := backend.Me(ctx)
		if err == nil {
			err = discord.ValidateChatUser(me)
		}
		if err != nil {
			return func() {
				a.scan.SetEnabled(true)
				a.qrText.SetText(err.Error())
				a.status.SetText("Not connected — check your connection or choose another sign-in method")
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		netdebug.Startup("account_validated", a.startedAt, 1)
		// No account-owned disk data is displayed before identity validation.
		cache, cacheErr := storage.OpenCache(dir, me.ID, ttl, storage.CacheLimit)
		snapshot, snapshotErr := storage.LoadStartup(dir, me.ID, ttl)
		thumbnails, _ := storage.OpenThumbnails(dir, me.ID, ttl)
		netdebug.Startup("disk_cache_loaded", a.startedAt, cache.Count())
		return func() {
			a.me, a.cache, a.thumbnails = me, cache, thumbnails
			a.current = discord.Channel{}
			a.channels = map[string]discord.Channel{}
			a.guildLoaded = map[string]bool{}
			a.guildLoading = map[string]bool{}
			a.rolesLoading = map[string]bool{}
			a.users = map[string]discord.User{me.ID: me}
			a.roles = map[string]map[string]string{}
			a.myRoles = map[string]map[string]bool{}
			a.login.SetVisible(false)
			a.shell.SetVisible(true)
			a.scan.SetEnabled(true)
			a.header.SetText("Choose a conversation · " + me.Name())
			a.drawer.SetVisible(true)
			netdebug.Startup("shell_visible", a.startedAt, 0)
			a.startEvents()
			a.restoreStartup(snapshot)
			a.startCacheTimer()
			a.refreshNavigation()
			if cacheErr != nil {
				a.showError(cacheErr)
			}
			if snapshotErr != nil {
				netdebug.Startup("startup_cache_miss", a.startedAt, 0)
			}
			if a.opt.Smoke && !a.smokeStarted {
				a.smokeStarted = true
				go a.smoke()
			}
		}
	})
}
func (a *App) rememberChannel(c discord.Channel) {
	if c.ID == "" {
		return
	}
	a.channels[c.ID] = c
	for _, u := range c.Recipients {
		a.users[u.ID] = u
	}
}
func (a *App) startEvents() {
	backend, ctx, gen := a.backend, a.session, a.generation
	go backend.Run(ctx, func(e discord.Event) {
		a.post(func() {
			if a.generation == gen {
				a.onEvent(e)
			}
		})
	})
}
func (a *App) startCacheTimer() {
	cache, ctx := a.cache, a.session
	if cache == nil {
		return
	}
	gen := a.generation
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.post(func() {
					if a.generation == gen && (a.startupDirty || a.captureReadingPosition() != a.savedPosition) {
						a.saveStartup()
					}
				})
				if err := cache.Flush(); err != nil {
					a.post(func() {
						if a.generation == gen {
							a.status.SetText("Message cache could not be saved")
						}
					})
				}
			}
		}
	}()
}
func (a *App) saveSettings() {
	if err := storage.SaveSettings(a.dir, a.settings); err != nil {
		a.showError(err)
	}
}
func (a *App) setupTray() {
	ni, err := walk.NewNotifyIcon(a.window)
	if err != nil {
		return
	}
	a.tray = ni
	ni.SetIcon(walk.IconApplication())
	ni.SetToolTip("Discrud — chat client")
	show := walk.NewAction()
	show.SetText("Open Discrud")
	show.Triggered().Attach(a.restoreWindow)
	ni.ContextMenu().Actions().Add(show)
	quit := walk.NewAction()
	quit.SetText("Quit")
	quit.Triggered().Attach(a.quit)
	ni.ContextMenu().Actions().Add(quit)
	ni.MouseDown().Attach(func(x, y int, b walk.MouseButton) {
		if b == walk.LeftButton {
			a.restoreWindow()
		}
	})
}
func (a *App) restoreWindow() {
	a.window.Show()
	a.window.Activate()
	if a.tray != nil {
		a.tray.SetVisible(false)
	}
}
func (a *App) quit() { a.quitting = true; a.window.Close() }
func (a *App) shutdown() {
	if a.closed.Swap(true) {
		return
	}
	a.endHistoryPaint()
	a.animationEpoch++
	if a.animationTimer != nil {
		a.animationTimer.Stop()
	}
	a.saveStartup()
	a.startupSaver.Close()
	a.cancel()
	if a.cancelSession != nil {
		a.cancelSession()
	}
	if a.cancelLoad != nil {
		a.cancelLoad()
	}
	if a.cancelMedia != nil {
		a.cancelMedia()
	}
	if a.cache != nil {
		_ = a.cache.Flush()
	}
	if a.tray != nil {
		a.tray.Dispose()
	}
	if a.qrBitmap != nil {
		a.qrBitmap.Dispose()
		a.qrBitmap = nil
	}
}
func (a *App) logout() {
	if a.sending {
		a.showError(errors.New("wait for the current send to finish before logging out"))
		return
	}
	if !a.opt.Demo {
		if walk.MsgBox(a.window, "Log out?", "Remove the saved session and local message cache? This forgets the credential on this PC; it does not revoke the session on Discord.", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			return
		}
		if err := storage.SessionVault().Delete(); err != nil {
			a.showError(err)
			return
		}
	}
	a.newSession()
	var cacheErr error
	if a.cache != nil {
		cacheErr = a.cache.Clear()
	} else {
		cacheErr = os.Remove(filepath.Join(a.dir, "messages.json"))
		if errors.Is(cacheErr, os.ErrNotExist) {
			cacheErr = nil
		}
	}
	if a.thumbnails != nil {
		cacheErr = errors.Join(cacheErr, a.thumbnails.Clear())
	}
	cacheErr = errors.Join(cacheErr, a.startupSaver.Clear(a.dir))
	a.thumbnails = nil
	a.backend = nil
	a.cache = nil
	a.me = discord.User{}
	a.current = discord.Channel{}
	a.messages = nil
	a.channels = map[string]discord.Channel{}
	a.guildLoaded = map[string]bool{}
	a.users = map[string]discord.User{}
	a.roles = map[string]map[string]string{}
	a.myRoles = map[string]map[string]bool{}
	a.mentions = map[string]int{}
	a.seenMentions = map[string]bool{}
	a.settings.LastOwner = ""
	a.settings.LastChannel = ""
	a.settings.LastGuild = ""
	a.saveSettings()
	a.clearComposer()
	a.clearHistory()
	a.login.SetVisible(true)
	a.shell.SetVisible(false)
	a.scan.SetEnabled(!a.opt.Demo)
	a.qr.SetVisible(false)
	a.qrText.SetText("Signed out. Scan a new QR code to reconnect.")
	if cacheErr != nil {
		a.qrText.SetText("Signed out, but some local caches could not be removed. Delete the local Discrud cache files before sharing this PC.")
	}
	a.header.SetText("Discrud " + discord.ClientVersion)
	a.window.SetTitle("Discrud " + discord.ClientVersion)
	a.status.SetText("Not connected")
}
func (a *App) openExternal(raw string) {
	if strings.HasPrefix(raw, "demo:") {
		walk.MsgBox(a.window, "Demo attachment", "Demo attachments are placeholders. No network request or external file will be opened.", walk.MsgBoxIconInformation)
		return
	}
	if !safeWebURL(raw) {
		a.showError(errors.New("only HTTP(S) links can be opened"))
		return
	}
	p, _ := windows.UTF16PtrFromString(raw)
	verb, _ := windows.UTF16PtrFromString("open")
	if err := windows.ShellExecute(windows.Handle(a.window.Handle()), verb, p, nil, nil, win.SW_SHOWNORMAL); err != nil {
		a.showError(errors.New("Windows could not open the link"))
	}
}
func (a *App) disposeImages() {
	for _, bmp := range a.cardImages {
		bmp.Dispose()
	}
	a.cardImages = nil
}
func (a *App) clearHistory() {
	a.previews = nil
	a.history.SetSuspended(true)
	for a.history.Children().Len() > 0 {
		a.history.Children().At(0).Dispose()
	}
	a.cards = nil
	a.renderedIDs = nil
	a.renderedMessages = nil
	a.previewCount = 0
	a.disposeImages()
	a.history.SetSuspended(false)
}

// smoke exercises actual native controls with the offline backend, never account credentials.
func (a *App) smoke() {
	time.Sleep(1200 * time.Millisecond)
	a.post(func() { a.servers.SetCurrentIndex(1) })
	time.Sleep(1200 * time.Millisecond)
	a.post(func() {
		if c, ok := a.channels["general"]; ok {
			if a.current.ID == c.ID {
				a.latestMessages()
			} else {
				a.openChannel(c)
			}
		}
	})
	time.Sleep(1200 * time.Millisecond)
	a.post(func() {
		a.editor.SetText("**Native MVP smoke test**\r\nMultiline, Unicode: hello 👋")
		a.editor.SendMessage(win.WM_CHAR, 13, 0)
	})
	time.Sleep(1600 * time.Millisecond)
	gifReport := a.smokeGIFs()
	videoReport := a.smokeVideo()
	probe := &feedSmokeProbe{}
	a.post(func() { a.smokeScrollHistory(probe) })
	time.Sleep(1000 * time.Millisecond)
	a.post(func() {
		a.finishSmokeScroll(probe)
		a.editor.SetText("@al")
		a.editor.Select(3, 3)
		a.updateSuggestions()
		if len(a.messages) > 4 {
			id := a.messages[len(a.messages)/2].ID
			a.smokePosition = storage.ReadingPosition{Channel: a.current.ID, Anchor: id, Offset: 5, FollowLatest: false}
			position := a.smokePosition
			a.restorePosition = &position
			a.renderMessages(false, id)
		}
	})
	time.Sleep(500 * time.Millisecond)
	feedReport := a.smokeChannelSwitches(probe)
	a.post(func() {
		_ = os.MkdirAll("dist", 0700)
		report := map[string]any{"demo": a.opt.Demo, "conversation": a.current.ID, "messages": len(a.messages), "native_controls": a.history.Children().Len(), "cache_entries": 0, "mention_suggestions": len(a.choices), "error_visible": a.errorText.Visible(), "error": a.errorText.Text(), "window_visible": a.window.Visible(), "shell_visible": a.shell.Visible(), "login_visible": a.login.Visible(), "history_visible": a.history.Visible(), "history_bounds": a.history.Bounds(), "shell_bounds": a.shell.Bounds(), "login_bounds": a.login.Bounds(), "editor_bounds": a.editor.Bounds(), "editor_client_bounds": a.editor.ClientBounds(), "edit_host_bounds": a.editHost.Bounds(), "content_bounds": a.history.Parent().Bounds(), "drawer_bounds": a.drawer.Bounds()}
		for key, value := range videoReport {
			report[key] = value
		}
		for key, value := range gifReport {
			report[key] = value
		}
		for key, value := range feedReport {
			report[key] = value
		}
		report["startup_cached_restore"] = a.cachedShown
		report["reading_position_restored"] = a.captureReadingPosition() == a.smokePosition
		report["history_cards_have_layout"] = true
		for _, card := range a.cards {
			b := card.BoundsPixels()
			if !card.Visible() || b.Width <= 0 || b.Height <= 0 {
				report["history_cards_have_layout"] = false
			}
		}
		for key, value := range a.editor.smokeWrapping() {
			report[key] = value
		}
		if a.history.Children().Len() > 0 {
			card := a.history.Children().At(0).(*walk.Composite)
			report["first_card_bounds"] = card.Bounds()
			report["first_card_visible"] = card.Visible()
			for i := 0; i < card.Children().Len(); i++ {
				if rich, ok := card.Children().At(i).(*richText); ok {
					report["native_rich_text_characters"] = rich.SendMessage(win.WM_GETTEXTLENGTH, 0, 0)
				}
			}
		}
		a.updateSuggestions()
		a.editor.SendMessage(win.WM_KEYDOWN, win.VK_TAB, 0)
		report["mention_inserted"] = strings.Contains(a.editor.Text(), "<@101>")
		if len(a.messages) > 0 {
			a.replyTo(a.messages[0])
			report["reply_ping_default_off"] = a.replyPing.Visible() && !a.replyPing.Checked()
			a.replyPing.SetChecked(true)
			report["reply_ping_toggle_on"] = a.replyPing.Checked()
			a.replyPing.SetChecked(false)
			report["reply_ping_toggle_off"] = !a.replyPing.Checked()
			a.clearComposer()
			report["reply_ping_cleared"] = !a.replyPing.Visible() && !a.replyPing.Checked()
		}
		_, dialogErr := a.runTokenDialog()
		report["token_dialog_masked"] = a.smokeTokenMasked
		report["token_dialog_cleared"] = a.smokeTokenCleared
		report["token_dialog_ok"] = dialogErr == nil
		mention, _ := json.Marshal(discord.Message{ID: "99999", ChannelID: "quiet", GuildID: "demo-server", Author: discord.User{ID: "101", Username: "alex"}, Mentions: []discord.User{a.me}, Content: "Demo mention", Timestamp: time.Now()})
		cacheCount, messageCount := 0, len(a.messages)
		if a.cache != nil {
			cacheCount = a.cache.Count()
		}
		a.onEvent(discord.Event{Type: "MESSAGE_CREATE", Data: mention})
		report["background_channel_ignored"] = a.mentions["quiet"] == 0 && len(a.messages) == messageCount && (a.cache == nil || a.cache.Count() == cacheCount)
		if len(a.messages) > 0 {
			firstID := a.messages[0].ID
			firstCard := a.cards[firstID]
			var firstHandle uintptr
			if firstCard != nil {
				firstHandle = uintptr(firstCard.Handle())
				report["message_context_menu"] = firstCard.ContextMenu() != nil
			}
			a.atLatest = true
			incoming, _ := json.Marshal(discord.Message{ID: "smoke-incremental", ChannelID: a.current.ID, Author: discord.User{ID: "smoke-user", Username: "smoke"}, Content: "Incremental render check", Timestamp: time.Now()})
			a.onEvent(discord.Event{Type: "MESSAGE_CREATE", Data: incoming})
			report["incremental_append_preserved_existing_card"] = firstCard != nil && a.cards[firstID] != nil && uintptr(a.cards[firstID].Handle()) == firstHandle
			if len(a.messages) > 2 {
				patched, _ := json.Marshal(map[string]string{"id": a.messages[1].ID, "channel_id": a.current.ID, "content": "Synthetic edit reconciliation"})
				a.onEvent(discord.Event{Type: "MESSAGE_UPDATE", Data: patched})
				report["incremental_edit_preserved_other_cards"] = a.cards[firstID] != nil && uintptr(a.cards[firstID].Handle()) == firstHandle
			}
		}
		if a.tray != nil {
			a.settings.CloseToTray = true
			_ = a.window.Close()
			report["tray_close_hides"] = !a.window.Visible() && !a.closed.Load()
			a.restoreWindow()
			report["tray_restore_visible"] = a.window.Visible()
			a.settings.CloseToTray = false
		}
		if a.cache != nil {
			report["cache_entries"] = a.cache.Count()
		}
		a.onEvent(discord.Event{Type: "GATEWAY_FAILED", Status: "Synthetic smoke connection failure"})
		a.clearError()
		report["gateway_failure_stays_visible"] = a.gatewayFailure != nil && a.errorText.Visible() && a.errorText.Text() == "Synthetic smoke connection failure"
		a.gatewayFailure = nil
		a.clearError()
		b, _ := json.MarshalIndent(report, "", "  ")
		_ = os.WriteFile("dist/smoke.json", b, 0600)
		a.quit()
	})
}
