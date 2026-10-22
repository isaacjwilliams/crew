# Plan: run crew on macOS

For an agent working through this alone. Before the first step, read this file, then `CLAUDE.md`, `ARCHITECTURE.md` and `tests/README.md`. Keep [Progress](#progress) current, and commit it with each step: it's how a fresh or compacted session knows where things stand. Steps marked **[user]** are the user's: stop, say what you need, and wait.

The edits are made on Linux; the user tests them on their Mac.

## Why

The user wants the crew to run on a Mac as well as Linux: as the machine that runs the crew, or as a second machine that reads it or takes over in a failover (live check L6). Go was chosen for the rewrite partly for this. Linux matters more and stays the reference: nothing changes there except what this plan names.

## Where crew depends on Linux today

- **systemd.** `crew install` puts `systemd/*` in `~/.config/systemd/user/` and runs `daemon-reload`, and `prune` disables a unit it removes (`cmd_install.go`). `crew host up`/`down` and the stand-down enable and disable `crewUnits` (`setUnits`, `cmd_host.go`; the list is in `lease.go`). `crew upgrade` restarts `crew-lead.service`. `crew doctor` reads each enabled service's `PATH` with `systemctl --user show`.
- **`/proc`.** `stopProcessesIn` (`proc_linux.go`) finds the processes left in a worktree by their working directory. On macOS, `proc_darwin.go` returns "not supported on macOS yet", and `crew cleanup` and `crew goal done` warn and carry on.
- **The machine's name.** `newCrew` takes the short `os.Hostname()` (`home.go`). On a Mac that can change with the network (a DHCP server can rename it), and a machine whose name changes no longer holds its own lease.
- **Releases.** `.github/workflows/release.yml` builds only `crew-Linux-x86_64` and `crew-Linux-aarch64`. The launcher needs no change: it asks for `crew-$(uname -s)-$(uname -m)`, which is `crew-Darwin-arm64` or `crew-Darwin-x86_64` on a Mac.
- **Docs.** SETUP.md says "macOS isn't supported yet" and gives only Linux commands (pacman, `systemctl`, `journalctl`, `loginctl enable-linger`). `crew install` suggests a pacman command for missing tools.

The rest already works on macOS, as far as reading the code shows. Locks use `flock(2)`, the background push uses `setsid`, and the launcher is POSIX `sh`. The guard resolves `/tmp` and `$TMPDIR` (under `/private/var/folders/`) through their symlinks. The fish files, `~/.claude` and `~/.config/fish` are the same on both.

## Decisions

Don't reopen these.

- **The launchd jobs are plist files in `launchd/`,** beside `systemd/`, installed the way the units are (the user's choice, 2026-10-22). A reader sees what runs, and `crew install` discovers them as it does the units. A Go test checks that each plist agrees with its unit: the command, the `PATH` entries, `Nice` and keeping the children running. That's two shapes of one idea, which the user accepted over generating the plists from the units in Go.
- **`crew install --no-systemd` becomes `--no-services`,** with no alias (the user's choice, 2026-10-22: nothing of theirs passes it). On a Mac, `--no-systemd` would really mean "no launchd". The rename touches `cmd_install.go`, the help, the fish completions, SETUP.md and ARCHITECTURE.md.
- **The Mac's jobs get a broad default `PATH`** (the user's choice, 2026-10-22): the home's `bin`, `~/.local/bin`, mise's shims, `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`, `/usr/sbin` and `/sbin`. launchd has no drop-ins like `systemctl --user edit`, and `crew install` replaces an installed plist (keeping a backup), so an edit there wouldn't last. A directory beyond those goes in an edit to the home's `launchd/*.plist`, committed, which `crew upgrade` merges like any other edit to crew's files. If a release changes the same `PATH` line, the upgrade stops on the conflict for the user to resolve. There's no setting for it.
- **Linux stays the reference.** Its behaviour, output and tests don't change, except the `--no-services` rename and messages that now come from the platform's file.
- **The shell suites stay Linux-only.** They lean on GNU and util-linux tools (`sed -i`, `stat -c`, `flock`, `setsid`, `script -qc`) and on `/proc` in the stub `claude`. Porting them isn't part of this plan.
  - Don't add a test-only switch that runs the launchd code on Linux.
  - The Mac is covered three ways:
    - Go unit tests of the pure logic, which run on Linux;
    - `go test ./...` on a macOS runner in the release workflow;
    - the live checks in step 9.
- **Pure logic in neutral files, OS calls in `_linux.go`/`_darwin.go`.** The parsing and rendering then run under `go test` on Linux: plist rendering, the PATH in a plist, `lsof` and `ps` output. Only the code that runs `systemctl`, `launchctl`, `lsof`, `ps` or `scutil` goes in the platform files.
- **One set of service functions per platform, chosen by build tags.** `services_linux.go` and `services_darwin.go` each define the same small set:
  - the jobs' names (today's `crewUnits`);
  - enable and disable them now;
  - whether one is enabled;
  - restart the lead's server;
  - the `PATH` an enabled job runs with;
  - installing and pruning the files, with the right reload. The manifest's component is `systemd` on Linux and `launchd` on a Mac.

  Callers (`install`, `host`, `standDown`, `upgrade`, `doctor`) don't branch on the OS. The names are yours to choose; keep them plain and consistent with the code around them. No interface type: build tags already pick one implementation.
- **launchd jobs are LaunchAgents in the user's `gui/<uid>` domain,** in `~/Library/LaunchAgents/`. Their labels are `crew-tick`, `crew-gardener` and `crew-lead`, the unit names without their suffix, so the docs and logs read the same on both. They run only while the user is logged in: macOS has no `enable-linger`. A LaunchDaemon would need root, so it isn't used.
- **Each systemd setting has its launchd counterpart:**

  | systemd | launchd |
  |---|---|
  | `crew-tick.timer`: `OnBootSec=2min`, `OnUnitActiveSec=3min` | `StartInterval` 180 (no `RunAtLoad`) |
  | `crew-gardener.timer`: `OnCalendar=Mon *-*-* 06:00`, `Persistent=true` | `StartCalendarInterval` `{Weekday 1, Hour 6, Minute 0}`. launchd runs a job missed during sleep when the Mac wakes |
  | `crew-lead.service`: `Restart=always`, `RestartSec=30` | `KeepAlive` true, `ThrottleInterval` 30 |
  | `KillMode=process` (tick, lead) | `AbandonProcessGroup` true |
  | `Nice=10` | `Nice` 10 |
  | `Environment=PATH=…`, `%h` | `EnvironmentVariables`. launchd expands nothing, so `crew install` always renders the plists, replacing `%h/crew` with the home and `%h` with `$HOME` |
  | the journal | `StandardOutPath` and `StandardErrorPath` in the home's `logs/` (`logs/crew-tick.log`, …) |

- **The Mac's jobs always get `CREW_HOME`.** Each plist sets `CREW_HOME` to `%h/crew`, which rendering replaces like every other `%h/crew` path. The systemd units' commented `# Environment=CREW_HOME=` line has no counterpart.
- **The machine's name on a Mac is `scutil --get LocalHostName`,** falling back to the short `os.Hostname()` when that fails. `CREW_HOST` still wins. It's set in System Settings → General → Sharing, and doesn't change with the network.
- **The process scan on a Mac runs `/usr/sbin/lsof -nP -a -d cwd -F pn`** for the working directories and `/bin/ps -A -o pid=,ppid=,tty=,stat=` for parents, terminals (`??` means none) and zombies. Use absolute paths: a launchd job's `PATH` may lack `/usr/sbin`. The rules stay as `proc_linux.go` has them: leave crew and its parents alone, and processes with a terminal; TERM, then KILL after 10 seconds. Delete the "not supported" error and the warnings about it.
- **Releases build four binaries:** both Linux ones, and `crew-Darwin-arm64` and `crew-Darwin-x86_64`. The Intel one costs nothing to build. A `macos-latest` job in the release workflow runs `go test ./...`, and publishing waits for it.
- **Version 0.3.0:** a new platform, and a renamed flag.

## Things that are easy to get wrong

- **A plist in `~/Library/LaunchAgents/` loads at the next login,** unless it's disabled. `crew install` must leave the jobs off, as it leaves the units off on Linux. So:
  - the shipped plists carry `Disabled` true;
  - `crew host up` runs `launchctl enable gui/<uid>/<label>` and then `launchctl bootstrap gui/<uid> <plist>`;
  - `crew host down` and the stand-down run `launchctl disable`, so the job stays off after a reboot, and then `launchctl bootout`.
  - `bootstrap` fails on a job that's already loaded. Treat that as done, not as an error.
- **launchd doesn't reread a plist file.** When `crew install` changes the plist of a loaded job, it must boot the job out and bootstrap it again. That is launchd's `daemon-reload`.
  - For `crew-lead`, this restarts the lead's server, which `crew upgrade` does anyway.
  - Restart a job only when its plist changed: `crew install` runs on every upgrade.
- **"Enabled" means loaded:** `launchctl print gui/<uid>/<label>` succeeds. Use that for `doctor` and for the installer's "off on this machine" note.
- **`AbandonProcessGroup` on the tick and the lead,** or launchd kills the workers a tick resumed, and the `claude --bg` daemon a tick started, when the run ends. This is `KillMode=process` (L2), and it needs the same live check.
- **launchd can't skip restarting on exit code 3.** It has no `RestartPreventExitStatus`, and `KeepAlive`'s `SuccessfulExit` doesn't help with 3.
  - Accept the restarts: the lead's job is enabled only on the machine that holds the lease. When the lease moves, the next tick's stand-down boots the job out, so a few restarts 30 seconds apart, each exiting 3 at once, are the worst case.
  - Don't change `crew serve`'s exit code: Linux depends on it.
  - Say so in the Contracts row.
- **The logs' directory must exist before launchd starts a job.** `crew install` creates the home's `logs/`.
- **The guard on a case-insensitive APFS volume** compares paths as spelled. A path written as `~/Crew/projects/…` doesn't match the home `~/crew`, so the guard blocks a ledger edit it would allow. It never allows one it should block, so leave it alone.
  - Don't lowercase paths to "fix" it: a case-sensitive volume would then be judged wrongly.
  - Note it in ARCHITECTURE.md.
- **`crew-Darwin-arm64` must be signed to run on Apple silicon.** Go's linker signs darwin/arm64 binaries ad hoc, even when cross-compiling with `CGO_ENABLED=0`, so nothing extra should be needed. `curl` doesn't set the quarantine attribute, so Gatekeeper shouldn't ask. Both are live checks.
- **Credentials from a launchd job.** `claude` and `gh` keep their tokens in the login Keychain on a Mac, and git's SSH pushes need the agent's socket. A LaunchAgent in the `gui` domain should reach both while the user is logged in. That's a live check, not something to work around ahead of time.

## Steps

1. Branch `macos` from `main`. **A preparatory refactor, with no change in behaviour.**
   - Gather every `systemctl` call into `services_linux.go`, behind the functions named in Decisions.
   - Add a `services_darwin.go` whose functions return "not supported on macOS yet".
   - Move the hostname into `machineName()` in `machine_linux.go`, with a darwin twin that still uses `os.Hostname()`.
   - Every suite must pass, unchanged. One commit.
2. **`--no-services`:** the flag, help, completions, SETUP.md and ARCHITECTURE.md. Add a `tests/host.sh` check that it leaves a dropped unit alone, beside the `--no-fish` one. One commit.
3. **The machine's name on a Mac:** `scutil`, as in Decisions. Update the SETUP.md line that says `hostname -s`.
4. **The process scan on a Mac.**
   - Put the parsing of `lsof -F pn` and `ps` output, and the choice of what to stop, in a neutral file. Give it unit tests with sample output written from the two tools' man pages, since there's no Mac here. Say so in the test; the user can swap in real output in step 9.
   - `proc_darwin.go` only runs the two tools.
   - Update ARCHITECTURE.md (the cleanup section and the `crew` internals).
5. **The launchd jobs.**
   - Add `launchd/crew-tick.plist`, `crew-gardener.plist` and `crew-lead.plist`, per the table in Decisions. Use XML comments for what the units explain in theirs.
   - Fill in `services_darwin.go`.
   - Make `crew install`'s rendering neutral and unit-tested: `%h/crew` → home, `%h` → `$HOME`, always on darwin, and for a custom home only on Linux.
   - Add the Go test that each plist agrees with its unit.
   - `plutil -lint` can't run on Linux. Parse the plists with `encoding/xml` in the test, so a malformed one fails.
6. **`crew doctor` and `crew install` on a Mac.**
   - Doctor reads each loaded job's `PATH` from its installed plist (the same parser), and its hint says to add the directory to the home's `launchd/<label>.plist`, commit it and run `crew install`.
   - Install's hint for missing tools names Homebrew on darwin (`brew install git gh fish`) and pacman on Linux, as now.
7. **The release workflow.**
   - Build the two Darwin binaries.
   - Add the `macos-latest` `go test` job, and make publishing wait for it.
   - Check it with `actionlint` if that's installed, otherwise by reading it closely. It can't run before a tag is pushed.
8. **Docs, then the version.**
   - SETUP.md:
     - requirements: macOS with Homebrew, on Apple silicon or Intel, and the oldest macOS version that both Go's binaries and Claude Code support (look both up);
     - the installed-files table for both systems;
     - reading a tick's log (`~/crew/logs/crew-tick.log`);
     - pausing the gardener (`launchctl disable` and `bootout`);
     - that the jobs run only while you're logged in, and not while the Mac sleeps, which matters most for the lead on your phone;
     - the first-time prompts for the lead's service, and the troubleshooting entries that name `systemctl`.
   - ARCHITECTURE.md:
     - the layout table (`launchd/`, the services files);
     - the Contracts rows on units, `PATH`, rendering and exit code 3;
     - "Assumptions to re-check": add the Mac's live checks as M1–M8, from step 9.
   - CLAUDE.md: `launchd/` beside `systemd/` in the conventions, and the service functions as the one place that touches either.
   - `tests/README.md`: the suites are Linux-only, and `go test` runs on macOS in the release workflow.
   - Help and completions, if anything changed there.
   - Then, in its own commit, `VERSION` 0.3.0. Draft release notes for the user, with the renamed flag, and put them in your final report.
9. **[user]** Review the branch, merge it, and release `v0.3.0`. Check that the release shows four binaries. Upgrade the Linux home first, with `crew upgrade`. Then, on the Mac:
   - **M1:** `git clone` the private home to `~/crew` and run `~/crew/bin/crew install`, with no Go installed. The binary downloads and runs without a Gatekeeper prompt (`codesign -dv ~/crew/bin/release/crew-0.3.0` shows a signature). `crew doctor` passes. `crew host` shows the Mac's name from `scutil`.
   - **M2:** after `crew install`, a logout and login leave the jobs off (`launchctl print gui/$(id -u)/crew-tick` fails).
   - **M3:** `crew host up --take` on the Mac. `crew-tick` runs every 3 minutes (`~/crew/logs/crew-tick.log`). A tick reaches `claude` and `gh`, and pushes the ledger: no Keychain prompt, no auth error.
   - **M4:** `crew` appears on the phone from the Mac's `crew-lead`, after the two first-time prompts.
   - **M5:** workers dispatched from the phone keep running when a tick ends and when `crew upgrade` restarts `crew-lead` (L2 on the Mac).
   - **M6:** `crew cleanup` on a merged task stops a dev server left running in its worktree, and leaves a process started from a terminal there alone.
   - **M7:** after the Mac sleeps and wakes, the ticks carry on. `crew host down` turns the jobs off, and they stay off after a logout and login.
   - **M8:** L6, the failover drill, between the Linux machine and the Mac.

   Record what passes in ARCHITECTURE.md, with the Claude Code version, as for L2.
10. Once the user confirms the checks, delete this file in its own commit. Git history keeps it.

## Progress

- 2026-10-22: the plan written. The user settled its three open questions (plist files in `launchd/`, `--no-services`, a broad default `PATH` and edits to the home's plists); they're in Decisions. Step 1 is next.
- 2026-10-22: step 1 done on branch `macos`: `services_linux.go`, `services_darwin.go` (stubs), `machine_*.go`. The doctor's heading is "Enabled services" now, on both systems. Step 2 is next.
- 2026-10-22: step 2 done: `--no-services`, with a `tests/host.sh` check. Step 3 is next.
- 2026-10-22: step 3 done: `scutil --get LocalHostName` on a Mac. Step 4 is next.
- 2026-10-22: step 4 done: `proc.go` (the choice and the parsing, shared by both systems), `proc_darwin.go` runs `ps` and `lsof`. The samples in `proc_test.go` are from the man pages; swap in real output in step 9. Step 5 is next.
- 2026-10-22: step 5 done: `launchd/*.plist`, `services_darwin.go`, the rendering and plist reader in `services.go`, and `services_test.go` (each plist against its unit, rendering, the reader). Two things beyond the plan, both in `services_darwin.go`: booting a job out can end crew when crew runs in it (the tick's own stand-down, say), so the bootout is the last thing crew asks for a job: `crew-tick` is turned off last, after the rest of the stand-down, and a reload's bootout and bootstrap run in a shell of their own; and `servicePath` reads the installed plist already, which is step 6's doctor half. Step 6 (the install hint) is next.
- 2026-10-22: step 6 done: `installTools` in `machine_*.go`. Step 7 is next.
- 2026-10-22: step 7 done: four binaries, and a `test-macos` job that publishing needs. No actionlint here: checked by reading, by parsing the YAML, and by running the build loop locally (the darwin/arm64 binary has `LC_CODE_SIGNATURE`). Step 8 is next.
