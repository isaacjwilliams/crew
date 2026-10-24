# agent-crew

Source for a personal Claude Code agent setup: six agent profiles (lead, goal-lead, implementer, casual-observer, plan-reviewer, gardener), the `crew` CLI (Go, in `cmd/crew/`) with its write guard, fish integration, systemd units and their launchd twins for a Mac, and the lead's settings in `lead/`. This checkout becomes the public repo once the user publishes it. A person's *crew home* (`~/crew`) is a private copy of it, made by `crew setup`, which adds their ledger and pulls new versions from here with `crew upgrade`. Development happens here; a home only takes changes from upstream.

Read `ARCHITECTURE.md` before any structural change. It has the design principles, the flows, and the **Contracts** table of cross-file dependencies. `SETUP.md` is the user-facing guide; `tests/README.md` explains the test harness.

## Safety rules for working in this repo

- **Edit sources here, never the installed copies or a live home**: `~/.claude/agents/`, `~/crew/`, `~/agent-ledger/` (the ledger of the old installer, kept as an archive), `~/.config/fish/`, `~/.config/systemd/user/`, `~/Library/LaunchAgents/`. The user's home takes changes from this repo with `crew upgrade`.
- **Don't run `crew setup`, `crew install`, `crew host …`, `crew serve` or `crew upgrade` against the real HOME unless the user asks.** They change the live setup on this machine, its GitHub repos or its lease. The tests run them under a fake HOME, which is fine.
- **Never run `claude remote-control`**, not even with `--help`: it may start a server. Read https://code.claude.com/docs/en/remote-control.md instead.
- **Never push this repo, or create GitHub repos,** unless the user asks: nothing here is public until they publish it.
- **Don't touch `~/crew`, `~/agent-ledger`** or `~/agent-work`. They hold live task state and unmerged work.
- **Don't run this checkout's `bin/crew` outside the tests.** It would take this checkout as its home (or the real `~/crew`, when `CREW_HOME` is set) and write a ledger there. Exercise the Go code through the tests (`tests/run.sh`, `go test ./...`).
- **Never push a tag or create a release** unless the user asks: a pushed `v*` tag publishes a release that every home upgrades to.
- **Never start real model sessions** (`claude -p`, `claude --bg`, `claude --agent …`) to test changes. The tests use stubs in `tests/stubs/`. `claude plugin validate` is fine: it's static.

## Verify every change

```bash
tests/run.sh            # must pass before a change is done
```

Developing crew needs Go (see `go.mod`); running it doesn't. `tests/run.sh lint` runs `gofmt`, `go vet` (for Linux and macOS) and `go test ./...`.

- Add or update a test for every bug fix and behaviour change, in `guard.sh`, `crew-e2e.sh` or `host.sh`, or as a Go unit test for logic (front matter, settings files, the guard's table, stacks). Make sure the new test fails without the fix.
- After changing an agent file, `claude plugin validate agents` must pass. It runs as part of `tests/run.sh lint`.
- For changes that depend on live Claude Code behaviour (background sessions, resume, hooks firing, AGENTS.md loading), tell the user a live smoke test is needed. List exactly what to try.

## Conventions

- **Shipped code** is Go, standard library only, plus fish, and two `sh` files in `bin/`: the launcher `crew` and the hook forwarder `crew-guard`.
  - Runtime dependencies are git, gh, claude, fish and curl (the launcher fetches the binary with it). Go is needed only to develop crew, never to run it. Don't add third-party Go modules, Python or Node.
  - `gofmt` and `go vet` are the Go lint. Keep shellcheck clean at `-S warning` for `bin/` and `tests/`.
  - Linux is the reference, and crew runs on macOS too. Code that runs a system's own tools goes in a `_linux.go` file with a `_darwin.go` beside it; the parsing and choices around it go in a neutral file, so `go test` on Linux covers them. The shell suites are Linux-only; the release workflow runs `go test` on macOS.
  - **Services** are `systemd/` units on Linux and `launchd/` plists on a Mac, one plist per `.service`. `services_linux.go` and `services_darwin.go` define the same functions and are the only places crew runs `systemctl` or `launchctl`; callers never branch on the system. A change to a unit needs the same change to its plist: `services_test.go` checks that they agree.
  - Binaries are never committed. A release's workflow builds them (`.github/workflows/release.yml`), and `bin/crew` downloads the one for the home's `VERSION` into the gitignored `bin/release/`.
  - A command returns its refusal as an error: `fail` (exit 1), `notYet` (exit 4, a dispatch that only has to wait) or `exitQuietly(code)`. `warn` is for what doesn't stop it.
- **`crew tick`** only runs other `crew` commands, each as a process of its own (`step`), so a refusal skips one step and not the rest. Give it new duties as `crew` commands first.
- **`crew`** has one `cmd…` function per subcommand. A new subcommand also needs:
  - an entry in `commands` in `cmd/crew/main.go`;
  - an entry in the help text, `cmd/crew/help.go`;
  - `fish/completions/crew.fish`;
  - `ARCHITECTURE.md`.
- **Ledger files:**
  - Front matter stays flat `key: value` (there's no YAML parser).
  - `## Log` stays the last section of task files.
  - A new front-matter key goes in `templates/task.md` and `LEDGER.md`.
  - A status is a value of `Status` or `GoalStatus` in `ledger.go`; a Go test checks they match `LEDGER.md`.
- **Settings files** (`*.env`) are shell-style but parsed, not sourced (`env.go`): `KEY=value`, quotes, `$VAR`, `${VAR}` and a trailing `# comment`. Anything else is an error naming the file and line. Keep `config/defaults.env` and `templates/crew.env` within that.
- **Safety boundaries**:
  - The lead, goal lead and implementer have no `tools:` allowlist, only `disallowedTools`, so they get whatever MCP servers the target repo has. Never add project-specific tools to an agent file. Because they can spawn subagents, their guard hook (`bin/crew-guard`) lives in `lead/.claude/settings.json` and `config/worker-settings.json` as well as the frontmatter; keep both. Goal leads run with the lead's settings minus `agent`.
  - `crew guard` only checks Edit/Write paths, and blocks whenever it can't work out where a path leads. Don't add shell-command parsing back: it can't be made complete. Bash is governed by the permission mode (the lead and workers run in `auto`, like the user's own sessions), the `allow`/`deny` rules in `config/*-settings.json`, and the agent prompts.
  - No Claude Code sandbox. The user dropped it: on Linux it blocks `localhost`, so test suites that need a local database broke, and exempting them left little of it. Don't add it back unless the user asks. Leave the network open.
  - A project's `projects/<p>/worker-settings.json` is merged add-only (`workerSettings`): it may widen lists like `allow`, never override a value the shipped file sets or drop a `deny` rule. Keep it that way.
- **Stacks** are derived, not declared: a task is stacked when its `base` is another task's `branch` (`parentOf`). Don't add a second marker.
  - Whether a restack is needed is computed from git (`restackReason`), never stored. `crew sync` reports it and never restacks; `crew tick` restacks through `crew restack`, which never sends the same target twice (`restack_sent`).
  - `crew restack` only resumes the worker with the rebase to run. The worker pushes, retargets its PR and sets `base` last, so a failed restack still shows as needed. Only `crew` writes `base_sha`.
  - The restack note and the "Stacked tasks" / "Workflow: restack" sections of `implementer.md` are one contract; `tests/guard.sh` checks they agree.
- **Worktrees** are created and removed only in `worktreeCreate`/`worktreeRemove` (`worktree.go`), for both `WORKTREE_TOOL` values (`git`, `worktrunk`). Never pass `--yes` to `wt`: unapproved repo hooks must stop the dispatch.
  - What workers may do on GitHub is spelled out in `implementer.md` ("On GitHub, you may do exactly this"). Keep it, the `allow`/`deny` rules in `worker-settings.json`, and `tests/guard.sh` in agreement.
- **Agent profiles** stay project-agnostic.
  - Project conventions belong in each target repo's `AGENTS.md`; the user's personal taste belongs in their `~/.claude/CLAUDE.md`.
  - Frontmatter field names are exact and camelCase (`disallowedTools`, `initialPrompt`, …). Unknown fields are silently ignored, so check the subagent docs.
- **`crew install`** must stay safe to re-run.
  - It changes nothing tracked in the home (it writes the gitignored `lead/.claude/settings.local.json`), and backs up anything it replaces or removes to `~/.local/share/agent-crew/backups/`.
  - It discovers what to ship from the source dirs, so a new file under `agents/`, `fish/`, `systemd/` or `launchd/` is installed automatically, and one you delete is removed on the next run. New files still need a line in the ARCHITECTURE.md layout table.
- **Ledger commits** (`commit`) take only the state paths (`statePaths`), never the home's code or config.
- **`--add-dir` is variadic** in the Claude CLI. Never place it immediately before a positional prompt.

## When behaviour changes

- **Versions and releases:** `VERSION` holds crew's semantic version. A release is the tag `v<version>` pushed to the public repo: the release workflow checks it matches `VERSION`, runs the suites and publishes the binaries. Homes upgrade only to releases (`crew upgrade` takes the newest tag), so a change reaches users when the user tags it, not when it lands on `main`. Bump `VERSION`, in its own commit, for each release that changes something users get: the patch number for a fix, the minor for a new command, changed behaviour, or anything an upgrade needs them to do. A change that doesn't affect users, a docs tweak say, needs no release. It stays 0.x until the user calls it stable.
- Update the **Contracts** table in `ARCHITECTURE.md` if you change anything listed there, and update every side of that contract.
- Update `SETUP.md` for anything the user sees: commands, config keys, workflow.
- Update `LEDGER.md` for ledger conventions: statuses, keys, layout.
- Claude Code changes quickly. Before relying on a flag, a frontmatter field or a JSON shape, check https://code.claude.com/docs (for example `sub-agents`, `cli-reference`, `agent-view`, `memory`) or `claude --help`. Keep the "Assumptions to re-check" list in `ARCHITECTURE.md` current.
