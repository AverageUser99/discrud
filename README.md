# Discrud — first native MVP

A small, Windows-native Go client for your own Discord conversations. No Wails, WebView2, Electron, JavaScript, voice, activity panels, or plugins.

**Unofficial and unsupported by Discord. User-account access may cause verification challenges, restrictions, or account suspension. Use at your own risk.** This is a first testing build, not a claim that the entire daily-driver scope is finished.

## Try it

- **Offline demo:** double-click `dist/Demo.cmd`.
- **Real account:** double-click `dist/discrud.exe` (v0.1.2). Choose **Scan QR**, or **Use existing user token** to enter your own existing session token in a masked local field.
- For QR login, scan using the official Discord mobile app and explicitly approve your own login. Never approve a QR someone else initiated.
- For manual sign-in, paste only the raw token (no quotes, Bot/Bearer prefix, or Authorization header), then choose **Validate and sign in**. Clear your clipboard afterward; never send a token in chat or a bug report.
- The app performs a read-only account lookup, rejects bots, and saves a manually entered token only after successful validation. Cancelling or failed validation does not replace saved credentials. CAPTCHA/verification challenges are reported, not bypassed.
- Phone approval and desktop sign-in are separate steps: the desktop still has to exchange the approval ticket. Version **0.1.1** distinguishes explicit CAPTCHA responses, rejected tickets, and unknown HTTP failures without exposing credentials. A phone success message alone does not establish a desktop session. Discord has explicitly required hCaptcha during testing; the native QR flow cannot display that challenge. Manual token entry was approved as an alternative, without adding a webview.
- REST and gateway requests include consistent Discrud client metadata; no Chrome identity or official Discord build number is claimed.
- The executable is unsigned; Windows may show a SmartScreen warning. Only run builds you trust.

The executable is self-contained apart from built-in Windows components. The development toolchain is **not** needed to run it.

## Included

- Server/category/channel browsing; existing DMs and group DMs.
- Collapsible navigation and **Ctrl+K** quick switcher; **Ctrl+L** focuses the composer.
- Live gateway updates with reconnect/resume, automatic scroll-up history loading, **Older messages** fallback, and **Latest**.
- Selectable native RichEdit message text, basic bold/italic/underline/strike/code, links, reply previews.
- Reply, edit/delete your own messages, file uploads, clipboard image paste.
- **Enter** sends; **Shift+Enter** adds a newline. Use **Tab** or double-click to insert a mention suggestion.
- Image thumbnails, native animated GIF attachments, automatic Klipy sharing-link previews, and basic Discord-supplied embed previews; attachments/full media open externally when explicitly clicked.
- Selected-chat-only message processing; no background mention badges, sounds, or desktop alerts.
- Configurable close-to-tray, cache TTL, and Clear Cache.
- Windows Credential Manager for one saved session, from QR or explicitly chosen manual token entry. No password login or automatic credential extraction.
- No drafts: leaving a conversation discards unsent text and attachments.

Uploads are conservatively capped at **10 MiB combined / 10 files** and message text at **2,000 UTF-16 characters**. These are MVP limits, not claims about your Discord upload entitlement.

## Deliberate MVP limitations

- The quick switcher searches servers, DMs, and **channels already loaded by visiting their server**. Choose a server first to load more channels.
- User autocomplete uses participants encountered in messages/DMs/READY, not a full server-member directory. Role/channel completion uses loaded server metadata.
- History loads automatically within two viewports of the top (minimum 800 pixels), using cached contiguous pages before REST. Startup initially renders 30 messages; older pages reveal at most 20 controls at a time. Each feed has a 200-message sliding native window; the TTL/50 MiB-bounded disk cache retains up to 2,000 messages per conversation. Use **Latest** to return from deep history.
- PNG/JPEG/WebP thumbnails and bounded GIF animation are supported, with 24 preview slots per rendered page and two download workers. Basic embeds use Discord's supplied title/description/image URLs. Klipy sharing links additionally have a bounded native metadata resolver and do not require a Discord embed. GIFs use the Go standard decoder and our compositor/native widget, one reusable bitmap per player and one shared deadline timer. GIFs animate only while hovered and pause on the current frame when the pointer leaves. Offscreen, inactive-channel, hidden and minimized players also pause; at most four animate simultaneously. Limits: 8 MiB download, 1 megapixel canvas, 256 frames, 24 million total frame pixels, 32 MiB estimated memory per animation / 64 MiB shared. GIF frames stay in memory only; static thumbnails have a local cache. Attachment media fetches allow only the exact Discord CDN/media and images-ext-1/2 proxy hosts. Klipy link resolution additionally allows Klipy's HTTPS pages/CDNs, bounded validated redirects and public-IP-only connections. No account authorization or cookies are sent. MP4/WebM (including video-backed “GIFs”), animated WebP, stickers and full Discord markdown are not supported; use the browser link. Klipy links resolve actual GIF originals/variants from Discord hints or provider metadata and use the same hover player as attachments. Other sharing providers still require Discord-supplied GIF media.
- Sending and changing attachments while editing is not supported. Reply pings and automatic `@everyone/@here` parsing are disabled.
- Cold-start offline browsing is not implemented: startup needs a successful account lookup. Cached messages can be shown when a later channel fetch fails.
- History is not fully virtualized. Existing controls survive history prepends, appends and edits. The active feed plus two most recently visited native feeds stay resident, preserving their loaded window and reading offset without rebuilding on a warm switch. Older/expired views fall back to the message cache. Busy live channels still need manual visual testing.
- Background message events are discarded, not cached or counted. Opening a conversation fetches its latest history.
- Multiple simultaneous app instances are not supported.
- No installer, auto-updater, cross-machine compatibility matrix, or signed release.

See [REQUIREMENTS.md](REQUIREMENTS.md) and [TESTING.md](TESTING.md) for scope and acceptance checks, and [BUILD_REPORT.md](BUILD_REPORT.md) for measured build/test results.

## Klipy GIF links

Posting `https://klipy.com/gifs/scared-63` (or another `/gifs/<slug>` link) creates an inline hover-to-play GIF slot even without a Discord embed. The app prefers available Discord GIF/proxy URLs, then reads Klipy's Open Graph/JSON-LD/embedded JSON or advertised oEmbed metadata. It may try a GIF sibling of a provider video/poster asset, but accepts only actual GIF bytes under the same decoder limits. Canonical duplicate links/embeds share one slot per message.

Resolution is lazy, cancellable and bounded: four links per message, existing two media workers, 1 MiB metadata, 8 MiB GIF, 12 HTTP attempts / 25 seconds per load, and a 64-entry short-lived URL/failure cache. No JavaScript, browser, external decoder or new dependency. Failed previews can be clicked to retry; there is no retry polling. Direct provider requests disclose your IP to Klipy/CDN servers. Server challenges, removed content or video-only sources can still prevent resolution; the exact live Klipy examples have not been verified by the offline tests.

## Update efficiency

- One persistent Discord Gateway WebSocket pushes new messages, edits, and deletions; there is no periodic message-history polling.
- Only the selected conversation’s message events reach the UI/cache. Selection is checked again at the UI boundary to discard stale queued events.
- Guild changes trigger coalesced subscription updates using opcode 37, clearing optional subscriptions on the previous guild. Switching chats within one guild changes only the local filter—no redundant subscription packet.
- The protocol’s `typing` flag also gates guild message delivery, so it is enabled for the active guild; typing events themselves are discarded. Activity/thread/member-update subscriptions and all member-list ranges remain disabled. The `channels` subscription field is a **member-list** selector, not a per-channel message filter.
- Discord may still push account-wide/background events; these are discarded locally. This is not a guarantee of zero incoming background bytes. A read-only live startup/heartbeat check passed; that is not a guarantee for every account/server.
- REST history requests fetch 50 messages on opening a chat, explicit Latest, or a scroll/Older cache miss. Cached page membership prevents skipping unloaded ranges; end-of-history detection and single-flight loading avoid repeated empty requests. A cache-first startup normally waits for READY before its single authoritative history refresh, closing the initial event gap without duplicate fetches. If Gateway is unavailable, a three-second fallback permits REST-only use; a late READY may then need another catch-up refresh. Successful RESUMED connections replay missed events without another history request.
- Gateway heartbeats run at Discord’s requested interval. The five-second cache maintenance timer is local disk work, not an API call.

## Fast startup / resume

- A single account lookup validates the saved credential **before** any account-owned cached view is displayed. No cold-start offline access or authentication bypass.
- Restore the saved conversation, first visible message ID/pixel offset (or follow-latest), and 30 cached messages without waiting for server lists, roles, or the Gateway. The UI marks this data **cached · syncing**.
- Reconcile one fresh recent page in place, including edits/deletions; retain older cached history when needed for the reading anchor. If the anchor is gone, use the nearest available message. HTTP 403/404 removes the selected cached view.
- Navigation/role metadata is cached for up to 15 minutes, bounded by the configured retention. On a miss, independent requests run asynchronously; optional metadata never gates message rendering. Only the active/browsed server gets lazy metadata requests—no background-chat history prefetch.
- Thumbnails are cached as small PNGs under an account-specific hashed directory, with 32 MiB disk / 16 decoded-image limits, configured TTL, and coalesced concurrent loads. CDN URL query rotation does not invalidate an immutable attachment-ID cache key.
- Startup metadata writes are atomic, coalesced, and serialized on one disk worker. Reading position is captured on shutdown and the local maintenance tick. No drafts or automatic sends are restored.
- See [STARTUP_REPORT.md](STARTUP_REPORT.md) for measured live startup results and [HISTORY_REPORT.md](HISTORY_REPORT.md) for automatic history/channel-cache implementation, executable checks and limitations.

## Data and privacy

Live settings/cache: `%APPDATA%\Discrud`. Default demo data: `%TEMP%\Discrud-demo`.

- `settings.json`: retention, close behavior, last selected conversation/account ID.
- `messages.json`: **plaintext** message text/metadata, scoped to one account.
- `startup.json`: account-scoped conversation/navigation/role metadata and reading position, capped at **2 MiB**; not an authentication or permission source.
- `thumbnails/<account-hash>/*.png`: plaintext resized thumbnails, maximum **32 MiB per account** and **16** decoded images in memory; not full media or credentials.
- Credential Manager target: `Discrud/session`. Credentials never enter the JSON cache/settings.
- Default TTL: **24 hours from fetch**. Reading does not extend TTL; refetching does.
- Maximum message cache file: **50 MiB**, oldest fetched entries evicted first. Atomic replacement may temporarily use an additional file.
- Message expiry is checked at startup and on the five-second local maintenance tick. Thumbnail expiry is checked on cache open/read/write and retention changes. Reading cached data does not refresh its fetch age. TTL is not forensic erasure.
- Clear local caches removes message/startup/thumbnail files and inactive native feeds; new network activity can refill them. The current in-memory chat view remains.
- Log out removes the saved local credential and cache. It does **not** revoke the remote Discord session; use Discord's device/session settings for that.
- Full media and unsent drafts are not saved. Only the bounded thumbnails described above persist. No telemetry, analytics, or remote logging.
- The token-entry field is masked and cleared on submission/cancel. This is not a guarantee of memory erasure; the active session necessarily holds its credential in process memory. The app never reads or clears your clipboard automatically.

`--data-dir PATH` relocates settings/cache, **not** the live Credential Manager entry. `--demo` never reads or writes live credentials and never connects to Discord.

## Network debugging

Run the GUI executable with diagnostics enabled:

```powershell
.\dist\discrud.exe --debug
# Optional custom log destination:
.\dist\discrud.exe --debug --debug-log C:\Temp\discrud-network.log
```

A console is attached/opened and the same output is written to `discrud-network-debug.log` in the working directory (overwritten each run). Without `--debug`, no network log or debug console is created.

- Timestamped HTTP attempts show method, fixed category (messages/media/account/etc.), status, failure flags, and duration. `failed` includes HTTP errors (such as 401/415); `transport_failed` distinguishes connection failures from HTTP error responses. Failures/cancellations count as attempts; bodies are not consumed by diagnostics.
- Separate WebSocket handshake and inbound/outbound JSON message counters distinguish heartbeats, subscriptions, and message events. Background events received and discarded locally still count as incoming traffic.
- WebSocket failures show a numeric close code and fixed cause label, never the server’s free-form close reason. Gateway logs include retry delay/lifetime, invalid-session resumability, and heartbeat ACK timeouts. Short-lived READY sessions do not reset exponential backoff; protocol rejection codes stop reconnecting rather than repeating invalid packets.
- Startup phase timings distinguish account validation, disk loading, cached/fresh controls ready, and cached/fresh layout-and-scroll completion (`*_presented`). Render strategy/duration and thumbnail hit/miss/result diagnostics help identify local work versus network latency. These are application milestones, not OS compositor timestamps.
- Ctrl+C in the attached debug console requests application shutdown, including when close-to-tray is enabled.
- Cumulative totals print every five seconds and on exit. `web_requests` = HTTP attempts + WebSocket handshake attempts; each WebSocket message is **not** another HTTP request.
- WebSocket byte totals measure JSON payload bytes only, not TCP/TLS/framing overhead. HTTP body sizes, OS/browser traffic, DNS and lower-level automatic transport retries are not measured.
- Logs omit credentials, all headers/bodies, full URLs, query strings, channel/user/message IDs, and raw error text. Fixed protocol labels and payload lengths are recorded; logs still reveal usage timing/volume. Debug logging adds disk/console overhead and is not a packet capture. External attachment links opened in a browser are outside this counter.

### HTTP identity

API requests use `User-Agent: Discrud/<version> (personal unofficial Windows chat client)`. `X-Super-Properties` is base64-encoded Discrud/OS/version metadata, not a Chrome or official Discord build identity. JSON Accept/Content-Type and English locale headers describe the request; Origin/Referer use Discord URLs for protocol compatibility. WebSocket handshakes use the Discrud User-Agent and Discord Origin; media downloads also identify as Discrud and never receive the account Authorization header.

These headers are valid HTTP metadata, **not** evidence that Discord supports this client or grants official user-account API access. The user-token and QR flows remain unofficial; verification/challenges and account restrictions are possible.

## Build

Windows amd64, Go 1.26+, Python 3 for the convenience scripts. No Node or C compiler is required for a normal build.

```powershell
# Optional if Go is not installed: checksum-verified official, project-local toolchain.
python scripts/bootstrap.py

python scripts/build.py --test
python scripts/smoke.py
python scripts/benchmark.py
```

Output: `dist/discrud.exe`, `dist/Demo.cmd`, and third-party license notices.

### Releases

Merges to `master` that change Go source publish a GitHub release with a Windows amd64 ZIP containing the executable, demo launcher, and license notices. Before merging Go changes, set `ClientVersion` in `internal/discord/identity.go` to a new stable SemVer version (`MAJOR.MINOR.PATCH`); CI rejects a version whose `v`-prefixed Git tag already exists. Choose major, minor, and patch changes according to the release's compatibility and scope. Documentation-only and Python-only changes do not trigger a release. The release is tagged at the merge commit.

CI runs Go tests on pull requests that change Go source, and Python lint checks when Python files change. Feature-branch pushes run the applicable lint and compilation checks. After a merge, CI builds a release only for Go source changes and does not rerun the PR test suite.

```powershell
# Optional race detection requires a compatible C compiler.
.\.tools\go\bin\go.exe test -race ./...

# Opt-in live QR handshake only; no scan or account token exchange.
$env:DISCRUD_LIVE_QR_CHECK = "1"
.\.tools\go\bin\go.exe test ./internal/discord -run TestLiveQRHandshake -v
Remove-Item Env:DISCRUD_LIVE_QR_CHECK
```

The native RichEdit boundary receives OS-owned pointers in Windows callbacks. The build runs normal `go vet` on backend/storage/presentation/demo, and disables only the `unsafeptr` analyzer for the UI/entry-point packages.

## Structure

- `cmd/discrud`: Windows entry point and embedded manifest.
- `internal/discord`: remote-auth v2, REST operations, gateway/resume.
- `internal/storage`: Credential Manager, atomic settings, bounded TTL cache.
- `internal/presentation`: safe restricted RTF, mention resolution, message merging.
- `internal/gifplayer`: standard-library-only bounded GIF decoder wrapper and allocation-free palette compositor.
- `internal/gifsource`: bounded Klipy link discovery, metadata resolution, verified GIF fetching and public-network transport.
- `internal/ui`: Walk/Win32/RichEdit controls.
- `internal/demo`: local backend; no real account needed.

Protocol references: [Oxidcord](https://github.com/Master0fFate/oxidcord) and [Discord Userdoccers](https://docs.discord.food/remote-authentication/desktop). This is an independent Go implementation, not a Rust port or an official Discord product.
