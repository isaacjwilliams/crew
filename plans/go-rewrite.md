# Plan: rewrite crew in Go

For an agent working through this alone. Before the first step, read this file, then `CLAUDE.md`, `ARCHITECTURE.md` and `tests/README.md`. Keep [Progress](#progress) current: it's how a fresh or compacted session knows where things stand. Steps marked **[user]** are the user's: stop, say what you need, and wait.

## Why

The user finds bash hard to read, hard to test, and not portable, and wants crew to run on a Mac as well as Linux. They chose a compiled language, and Go over Rust: Go needs no third-party modules here, cross-compiles to macOS from Linux, and reads plainly. Mac support itself comes after this plan, on its own branch.

## Decisions

Don't reopen these.

- **Go, standard library only.** No third-party modules. `gofmt` and `go vet` are the lint.
- **One binary, built on each machine.** `bin/crew` becomes the build output, gitignored. There's no binary in git and no release download.
  - After cloning: `go build -o bin/crew ./cmd/crew`, then `bin/crew setup` (or `host up`).
  - `crew upgrade` merges as today, builds `bin/crew.new`, renames it over `bin/crew`, then runs the new binary's `install`. A failed build leaves the old binary in place.
  - Go joins the runtime dependencies. `jq` and `flock` leave them, since crew no longer needs them (the tests still use `jq`).
  - `crew doctor` checks that the binary was built from the home's `HEAD`, using the VCS stamp `go build` embeds (`debug.ReadBuildInfo`).
- **`crew-guard` becomes `crew guard <role>`.** The hook command is `"${CREW_HOME:-$HOME/crew}/bin/crew" guard <role>`.
  - It blocks (exit 2) whenever it can't work out a path. The bash version allows every edit when `realpath` fails, which it always does on macOS.
- **Nothing anyone else sees changes.** The commands, flags, output text, exit codes (1 refused, 3 not the host, 4 `not_yet`), the ledger format and the `*.env` format all stay as they are. The agent prompts and the shell test suites depend on them.
- **The `*.env` files stay shell-style but get parsed, not sourced.**
  - Supported: `KEY=value`, single or double quotes, `$VAR` / `${VAR}`, and a trailing `# comment`.
  - Any other line is an error that names the file and line. This goes in the release notes.
- **Port one piece per commit, with every suite passing after each one.**
  - `git mv bin/crew libexec/crew.sh`. The Go binary runs a command itself if it has been ported, and otherwise hands it to `libexec/crew.sh`.
  - When a command moves to Go, delete its bash function and change the bash's internal calls to it into calls to `$CREW_HOME/bin/crew`, so each command exists once.
  - `libexec/crew.sh` and the hand-off are deleted in the last port step. They never reach users: the branch merges only once they're gone.
- **The shell suites are the oracle.** `tests/guard.sh`, `crew-e2e.sh` and `host.sh` test `crew` from outside, so they stay. Change them only where this plan changes a contract (the guard command, the lock check, the build).
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
   - Gitignore `bin/crew`.
   - `tests/lib.sh` builds the binary once per run, and `make_home` copies it into each home's `bin/`.
   - `tests/run.sh lint` adds `gofmt -l`, `go vet` (Linux and darwin) and `go test ./...`.
2. **The guard.** Write `crew guard`, update the hook command everywhere it appears (agent frontmatter, `lead/.claude/settings.json`, `config/worker-settings.json`, `cmd_doctor`, `tests/guard.sh`) and the Contracts table, then delete `bin/crew-guard`.
   - Add tests: a path that can't be resolved is blocked; a path into the home through a symlink is judged by where it points.
3. **Foundations and the ledger commands:** home and config, env parsing, the ledger model, locks, `commit` and `push_soon`, `resolve`. Then `help`, `config`, `list`, `show`, `path`, `find`, `get`, `set`, `log`, `new`, `ready`, `commit` (with `--hook`), `proposals`, `promote` and `status`. Split this into several commits.
4. **Projects:** `project`, `checkout`, `observe`.
5. **Sessions and work:** `dispatch`, `followup`, `restack`, `cleanup`, `feedback`, `reply`, `sync`, `goal`, `gardener`. One or two commands per commit.
6. **The machine:** `host`, `serve`, `setup`, `install`, `doctor`, and `upgrade` (with the build-and-rename above).
7. **`tick`.** Then delete `libexec/crew.sh` and the hand-off.
8. **Docs.**
   - CLAUDE.md conventions: Go plus fish; shellcheck only for `tests/`; drop the bash rules.
   - ARCHITECTURE.md: the `crew` internals section, the layout table, the Contracts table, and "Assumptions to re-check".
   - SETUP.md: Go as a dependency, building after a clone, and the upgrade window below.
   - `tests/README.md`.
9. A commit bumping `VERSION` to `0.2.0`.
10. **[user]** Review the branch, merge it to `main` and push. Then upgrade the real home:
    - Do it while no worker is mid-turn: workers call `crew`, which is missing until the build.
    - The old bash `crew upgrade` merges, then fails because `bin/crew` isn't built yet. Then run:
      ```
      cd ~/crew && go build -o bin/crew ./cmd/crew && bin/crew install && git push
      systemctl --user restart crew-lead
      ```
    - Smoke test: `crew doctor`; a tick from the timer (`journalctl --user -u crew-tick`); the lead on the phone; one tiny task end to end with one review round.
11. Once the user confirms the smoke test, delete this file in its own commit. Git history keeps it.

## Progress

- (nothing yet)
