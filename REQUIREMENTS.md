# Scope and acceptance baseline

## Decisions approved during planning

- Personal daily-use Windows client, for this machine only.
- Go; explore native UI instead of the initially proposed Wails.
- Prefer low idle CPU/RAM, small installation, few dependencies.
- Chat-first interface with drawer and keyboard quick switcher.
- One Discord **user** account, securely remembered session. Originally QR-only; after a confirmed hCaptcha block, the user explicitly approved optional manual entry of their own existing token. No browser login dependency or automatic extraction.
- Unofficial-account-access risks accepted; no CAPTCHA/MFA bypass.
- Ordinary server text channels, existing DMs, existing group DMs.
- Text, images/files, replies, user/role/channel mentions, own-message editing/deletion.
- Enter sends; Shift+Enter inserts a newline.
- No draft preservation, including when switching conversations.
- Mention indicators only; no general unread indicators, alerts, or sounds.
- Recent text/metadata cache: configurable TTL, default 24 hours; 50 MiB cap; Clear Cache.
- Configurable close behavior: exit or minimize to tray.

## Completion gates

| ID | Acceptance condition | MVP status |
|---|---|---|
| AUTH-1 | QR approval; clearly report additional challenges without bypass | Phone approval confirmed; final exchange blocked by Discord hCaptcha in live testing |
| AUTH-2 | Restart with saved session; logout removes local credential | Credential Manager round-trip tested; real-account restart pending |
| AUTH-3 | Optional masked manual token entry; validate user, reject bots, save only on success, cancel safely | Unit and native mask/cancel tests passed; real-token sign-in pending |
| NAV-1 | Browse server/category/text-channel, existing DM/group DM | Native demo tested; live permissions/API behavior pending |
| NAV-2 | Drawer, keyboard switcher, restore last conversation | Implemented; switcher currently covers loaded channels |
| CHAT-1 | Fetch history, paginate, receive create/edit/delete live | Local protocol/backend tests; live end-to-end pending |
| CHAT-2 | Send multiline text, replies, mentions | Offline/native tests; live delivery pending |
| CHAT-3 | Edit/delete own messages, explicit delete confirmation | Backend tested; manual native/live actions pending |
| FILE-1 | Pick files/paste images and send; thumbnails; external open | Implemented with conservative caps; manual clipboard/upload testing pending |
| MENTION-1 | Mention indicators without ordinary notifications | Native smoke tested; live events/role membership pending |
| CACHE-1 | Persist recent messages; expire without extending TTL on reads | Automated tests passed |
| CACHE-2 | Enforce cap; clear cache; prevent cross-account reuse | Automated tests passed; filesystem failures remain visible |
| WINDOW-1 | Close exits or hides to recoverable tray icon | Native tray hide/restore smoke passed |
| REL-1 | Connection status, reconnect/resume, manual retry, no silent send queue | Local gateway/rate-limit/nonce tests passed; real network-loss test pending |
| PERF-1 | Record binary size, idle CPU, working set, private bytes | Offline benchmark recorded; live workload still needs measurement |

“Implemented” is not the same as “accepted on a real account.” This first build is ready for testing, not the final daily-driver acceptance milestone.

## Excluded

Voice/video, reactions, typing indicators, presence controls, message search, emoji/sticker pickers, threads/forums, friend management, new DM/group creation, server administration, bots, plugins, and public-release packaging.

## Remaining before full scope acceptance

1. Complete the manual real-account checklist in TESTING.md.
2. Decide whether loaded-participant autocomplete and loaded-channel switching are sufficient or need expansion.
3. Validate native layout, hyperlinks, clipboard paste, thumbnails, and scrolling by direct use.
4. Scroll-triggered pagination and bounded native channel reuse are implemented and executable-smoke-tested; validate visual smoothness on representative live channels before considering full history virtualization.
5. Decide whether cold-start offline browsing or encrypted message cache is necessary.
6. Measure a representative live account before setting hard performance budgets.
