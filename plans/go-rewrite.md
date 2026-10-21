# Plan: rewrite crew in Go

For an agent working through this alone. Before the first step, read this file, then `CLAUDE.md`, `ARCHITECTURE.md` and `tests/README.md`. Keep [Progress](#progress) current: it's how a fresh or compacted session knows where things stand. Steps marked **[user]** are the user's: stop, say what you need, and wait.

## Why

The user finds bash hard to read, hard to test, and not portable, and wants crew to run on a Mac as well as Linux. They chose a compiled language, and Go over Rust: Go needs no third-party modules here, cross-compiles to macOS from Linux, and reads plainly. The repo comes "batteries included": a clone runs without Go installed. Mac support itself comes after this plan, on its own branch.

## Decisions

Don't reopen these.

- **Go, standard library only.** No third-party modules. `gofmt` and `go vet` are the lint. Go is needed only to develop crew, never to run it.
- **Binaries are attached to GitHub Releases, never committed.** A GitHub Actions workflow builds them when a `v*` tag is pushed (see step 8).
  - Files are named after `uname -s` and `uname -m`: `crew-Linux-x86_64`, `crew-Linux-aarch64`. macOS binaries come with the Mac branch.
  - The version is stamped in at build time (`-ldflags "-X main.version=…"`). `crew doctor` checks that it matches `VERSION`.
- **`bin/crew` is a committed POSIX `sh` launcher, about 15 lines.**
  - It runs `bin/release/crew-<VERSION>` (`bin/release/` is gitignored), with `VERSION` read from the home. If that file is missing, it downloads it once with `curl` from the public repo's release `v<VERSION>`: to a temporary file, checked by running it with `version`, then renamed into place.
  - `CREW_VERSION` in the environment picks another version. `crew upgrade` uses it to fetch the next version's binary before it merges, so only the launcher knows how to download.
  - It finds itself through symlinks, as the bash did.
  - When it can't get a binary, it says why and exits 1, or 2 when called as `crew guard`, so a missing binary blocks edits.
  - The release URL is written into the launcher. A fork changes that one line.
  - So a fresh clone, or another machine taking over (`git clone <private repo> ~/crew && ~/crew/bin/crew host up --take`), needs no extra step.
- **`crew upgrade` moves to the newest release tag on `upstream`, not `upstream/main`.**
  - First `CREW_VERSION=<new> bin/crew version`, which downloads the binary. If that fails, it refuses: the release's binaries may not be up yet.
  - Then the merge, the new version's `install`, the push and the restart, as today.
  - Then it deletes the other versions in `bin/release/`.
  - The agent prompts and the binary then always come from the same release.
- **Runtime dependencies:** git, gh, claude, fish and curl. `jq` and `flock` drop out, since crew no longer uses them (the tests still do).
- **The guard is `crew guard <role>`. `bin/crew-guard` stays, as a two-line `sh` file that runs `crew guard "$@"`.** The hook command and its contract don't change.
  - Sessions started before an upgrade keep calling `bin/crew-guard`. If it disappeared, Claude Code would treat the hook's "not found" as a non-blocking error and allow their edits.
  - The guard blocks (exit 2) whenever it can't work out a path. The bash version allows every edit when `realpath` fails, which it always does on macOS.
- **Nothing anyone else sees changes.** The commands, flags, output text, exit codes (1 refused, 3 not the host, 4 `not_yet`), the ledger format and the `*.env` format all stay as they are. The agent prompts and the shell test suites depend on them.
- **The `*.env` files stay shell-style but get parsed, not sourced.**
  - Supported: `KEY=value`, single or double quotes, `$VAR` / `${VAR}`, and a trailing `# comment`.
  - Any other line is an error that names the file and line. This goes in the release notes.
- **Port one piece per commit, with every suite passing after each one.**
  - `git mv bin/crew libexec/crew.sh`, and `bin/crew` becomes the launcher. The Go binary runs a command itself if it has been ported, and otherwise hands it to `libexec/crew.sh`.
  - When a command moves to Go, delete its bash function and change the bash's internal calls to it into calls to `$CREW_HOME/bin/crew`, so each command exists once.
  - `libexec/crew.sh` and the hand-off are deleted in the last port step. They never reach users: the branch merges only once they're gone.
- **The shell suites are the oracle.** `tests/guard.sh`, `crew-e2e.sh` and `host.sh` test `crew` from outside, so they stay. Change them only where this plan changes a contract (the lock check, the launcher, upgrading to a tag).
  - Add Go unit tests (`go test ./...`) for the logic: front matter, sections, env parsing, `resolve`, stacks, dispatch checks, the guard's table.
  - Converting the shell suites to Go is not part of this plan.
- **Model the ledger instead of passing strings around.** `Task`, `Goal`, `Proposal` and `Project` read and write their own front matter and sections. Statuses are a type, and a test checks that every status value in `LEDGER.md` is handled.
  - Package layout is yours to choose. Keep it flat and small.
- **Linux is the reference, but the Mac build must compile.**
  - Put the Linux-only code (the `/proc` scan in cleanup) in `_linux.go` files, with a `_darwin.go` that returns "not supported on macOS yet".
  - Lint runs `GOOS=darwin go vet ./...` as well as the Linux vet.
  - systemd stays as it is: launchd belongs to the Mac branch.

## Things that are easy to get wrong

- **Locks.** Go's `syscall.Flock` on the same `.crew*.lock` files is the same lock that util-linux `flock` takes, so bash and Go share locks during the port.
  - Go doesn't pass open files to child processes, so the "close fds 7–9" rule goes away.
  - Change `tests/stubs/claude`'s check to "no open fd points at a `.crew*.lock` file", and update the Locks row in the Contracts table.
- **`push_soon`** pushes in the background so no command waits on the network. In Go, start the binary again, detached (its own session, output to `logs/push.log`), rather than a goroutine that dies when the command exits.
- **`crew tick`** must keep running each step as a separate `crew <cmd>` process, so a refusal skips only that step (see CLAUDE.md).
- **`fm_set`'s quoting rules** (`: `, ` #`, `"`, newlines) and **`append_log`** (`## Log` stays last) must match exactly. Port the bash's behaviour into unit tests first.
- **`cmd_install`** discovers its files, renames each new file into place, takes backups and keeps a manifest. Keep all of that: re-running it must stay safe.
- **Session JSON** (`claude agents --json`, transcripts, the `backgrounded · <job>` line) goes into Go structs. The contracts in ARCHITECTURE.md don't change.

## Steps

0. **[user]** Ask whether the move to the portable crew is finished (old plan's 10.3). If it is, delete `crew migrate-ledger`, its test scenario (tests/host.sh scenario 12) and its docs in one commit on `main` (old plan's 10.4). If not, stop here.
1. Branch `go-rewrite` from `main`.
   - Add `go.mod` (`github.com/isaacjwilliams/crew`) and `cmd/crew`, which hands every command to `libexec/crew.sh`.
   - Add the launcher as `bin/crew`, and gitignore `bin/release/`.
   - `tests/lib.sh` builds the binary once per run, stamped with `VERSION`, and `make_home` puts it in each home's `bin/release/`, so every suite runs through the launcher.
   - Add a `tests/stubs/curl` that serves files from the sandbox, and launcher tests:
     - a missing binary is downloaded and run;
     - a failed download exits 1, or 2 for `crew guard`;
     - `CREW_VERSION` fetches another version.
   - `tests/run.sh lint` adds `gofmt -l`, `go vet` (Linux and darwin), `go test ./...`, and shellcheck on the launcher.
2. **The guard.** Write `crew guard`, and make `bin/crew-guard` the two-line forwarder.
   - Add tests: a path that can't be resolved is blocked; a path into the home through a symlink is judged by where it points.
3. **Foundations and the ledger commands:** home and config, env parsing, the ledger model, locks, `commit` and `push_soon`, `resolve`. Then `help`, `config`, `list`, `show`, `path`, `find`, `get`, `set`, `log`, `new`, `ready`, `commit` (with `--hook`), `proposals`, `promote` and `status`. Split this into several commits.
4. **Projects:** `project`, `checkout`, `observe`.
5. **Sessions and work:** `dispatch`, `followup`, `restack`, `cleanup`, `feedback`, `reply`, `sync`, `goal`, `gardener`. One or two commands per commit.
6. **The machine:** `host`, `serve`, `setup`, `install` and `doctor` (dependencies, the binary's version), and `upgrade` (to the newest release tag, as above). `tests/host.sh` upgrades to a tag in its fake public repo, with the stub `curl` serving that tag's binary.
7. **`tick`.** Then delete `libexec/crew.sh` and the hand-off.
8. **The release workflow,** `.github/workflows/release.yml`.
   - It runs on a pushed `v*` tag, and only when `github.repository` is `isaacjwilliams/crew`, so a private home that pushes a tag never builds one.
   - It checks that `VERSION` matches the tag, installs what the suites need (fish, for one), runs `go test ./...` and `tests/run.sh guard crew-e2e host`, and cross-compiles the two Linux binaries with `CGO_ENABLED=0` and `-trimpath`.
   - It publishes the release with `gh release create <tag> --verify-tag --notes-from-tag`, using the workflow's `GITHUB_TOKEN` with `contents: write`.
   - Lint stays a local check, since it needs the `claude` CLI.
   - This can't be run before the user pushes a tag. Check it with `actionlint` if that's installed, otherwise by reading it closely.
9. **Docs.**
   - CLAUDE.md conventions: Go plus fish, and the two `sh` files in `bin/`; shellcheck only for those and `tests/`; drop the bash rules.
   - CLAUDE.md versions: a release is a pushed tag, and homes upgrade only to releases.
   - ARCHITECTURE.md: the `crew` internals section, the layout table, the Contracts table (upgrade, locks, the launcher) and "Assumptions to re-check".
   - SETUP.md: no Go needed, curl, how upgrades and releases work.
   - `tests/README.md`.
10. A commit bumping `VERSION` to `0.2.0`. Draft release notes for the user, including the stricter `*.env` parsing, and put them in your final report.
11. **[user]** Review the branch and merge it to `main`.
    - Release: `git tag -a v0.2.0` with the notes, then `git push origin main v0.2.0`. Check that the release shows both Linux binaries. If the workflow fails, delete the tag locally and on GitHub, have the agent fix it, then tag again.
    - Upgrade the real home with a plain `crew upgrade`. The old bash version merges `upstream/main`, which is `v0.2.0` now, then runs `bin/crew install`. That is the launcher by then: it downloads the binary and installs.
    - Smoke test: `crew doctor`; a tick from the timer (`journalctl --user -u crew-tick`); the lead on the phone; one tiny task end to end with one review round; and on another machine, `git clone` of the private home and `bin/crew status`, with no Go installed.
12. Once the user confirms the smoke test, delete this file in its own commit. Git history keeps it.

## Progress

- Step 0 done (2026-10-21): the user confirmed the move is finished; `crew migrate-ledger`, host.sh scenario 12 and their docs are removed on `main`. The v0.2.0 release notes should say so.
- Step 1 done: branch `go-rewrite`. `cmd/crew` (package `main`, the only package) does `crew version` itself and hands everything else to `libexec/crew.sh` with `syscall.Exec`. The launcher `bin/crew` takes `VERSION` and `bin/release/` from its own checkout (resolving symlinks), and sets `CREW_HOME` to that checkout unless it's set; the plan's "VERSION read from the home" is the same thing except in `tests/guard.sh`, which runs a launcher with `CREW_HOME` pointing at a bare ledger. `tests/lib.sh` builds with `release_binary VERSION` into `$CREW_TEST_RELEASES`, which the stub `curl` serves; the launcher tests are host.sh scenario 12. `tests/guard.sh` greps crew's source through `$SRC` (bash and Go together) while it moves. Docs wait for step 9.
- Step 2 done: `crew guard` (`guard.go`, unit tests in `guard_test.go`); `bin/crew-guard` forwards to it. `canonical` resolves paths like `realpath -m` and fails on a loop, which blocks. Hook input that isn't JSON now blocks too (the bash allowed it). `tests/guard.sh` runs the guard from a home made by `make_home`; its "state paths are crew's" check went, since there's one list now (`statePaths` in `home.go`; the bash `commit` keeps its own until step 3).
- Step 3, first commit: the foundations (`home.go` settings and projects, `env.go`, `ledger.go`, `lock.go`, `lease.go`, `push.go`, `resolve.go`) and `help`, `config`, `list`, `show`, `path`, `find`, `get`, `set`, `log`, `commit` (with `--hook`) and `proposals` in Go. Adds `crew push` (in help): `pushSoon` starts `crew push --soon` in its own session. Errors are returned, not `die`d: `fail` (exit 1), `notYet` (4), `exitQuietly(code)`. The bash calls Go commands through `crew_go`, which closes fds 7–9 (Go passes inherited fds on to what it starts). The stub `claude` now flags any fd open on a `.crew*.lock` file. **Never `git checkout` a file to undo a test mutation: copy it aside and back** (it once threw away uncommitted work).
