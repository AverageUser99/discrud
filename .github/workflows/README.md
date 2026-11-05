# CI workflow overview

The workflow runs on branch pushes and pull requests. It detects changed files first, then uses those results and the event type to select checks and release steps. Go and Python changes are identified by `**/*.go` and `**/*.py`; other files do not trigger language-specific checks. Dependency manifests and build configuration are not included in these filters.

## Change detection and concurrency

Pull requests are compared with their base branch. Pushes to `master` are compared with the previous commit; other branch pushes are compared with the default branch. The resulting flags control downstream jobs and steps, avoiding language setup and checks when they are not needed.

Runs are grouped by pull request or branch. A newer pull request or non-`master` branch run cancels an older run in the same group; `master` runs are not cancelled in progress.

## Checks

- **Non-`master` pushes:** If Go or Python files changed, a Windows job runs only the checks for the affected languages. Go checks verify formatting, compile packages and tests without running tests, and run `go vet`. The native-code vet invocation disables `unsafeptr`. Python checks use Ruff's selected syntax and correctness rules.
- **Pull requests:** The required test job remains present even when neither language changed. Go changes trigger version validation and the Go test and vet suite; Python changes trigger Ruff. Both sets run when both languages change.
- **`master` pushes:** The required test job validates the version when Go files changed but does not repeat the pull request test suite. This assumes branch protection requires successful, up-to-date pull request checks and is not bypassed.

Windows Go jobs target amd64 with CGO disabled. The branch formatting check preserves LF checkout line endings because `gofmt` expects them. Go module caching reduces repeated dependency downloads.

## Build and release

A `master` push builds and publishes only when Go source changed and the preceding jobs succeeded. Version validation requires a stable `MAJOR.MINOR.PATCH` value and rejects an existing matching `v`-prefixed tag. The build produces a Windows amd64 executable and packages it with a launcher and license notices. The ZIP is uploaded as a workflow artifact and attached to a GitHub release tagged at the pushed commit.

The workflow grants read access by default; only the release job receives write access to repository contents. Documentation-only and Python-only changes do not produce a release.
