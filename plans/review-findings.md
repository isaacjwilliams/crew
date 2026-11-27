# Plan: the 2026-11-27 code review's twelve findings

On 2026-11-27 five independent reviewers read crew, each through one lens: state and concurrency, outside tools and failure, security and trust, structure and tests, and system design. They found twelve problems worth acting on. This file says what each one is, where it shows in the code, what it costs and which rule crew should hold instead. It also gives the order the work is done in, and how far it has got.

The reviewers read commit `aa8782a`. Everything below was checked against `main` at `f5b790d`, after the typed task records (#119) merged. Where `main` has moved since, the entry says so. Counts are given only where they were measured on `main`; a figure the review gave and nobody has re-measured is marked "not measured".

Read this file, then `CLAUDE.md` and `ARCHITECTURE.md`, before taking an item. Keep [Progress](#progress) true: see [Keeping the list true](#keeping-the-list-true).

## Progress

The twelve findings, described in full [below](#the-findings): 1 a failed lookup reads as "nothing there"; 2 ledger writes aren't safe; 3 nothing times out; 4 crew has no notion of who is calling; 5 the host lease; 6 the status machine isn't written down; 7 the tick infers what its steps did; 8 no seam for git, gh and claude; 9 prompts have no behavioural test; 10 model sessions do deterministic work; 11 proposals have no triage; 12 `ARCHITECTURE.md` has drifted. Below, "the Contract" is the IMP-style list of numbered obligations for each component, each tied to the tests that prove it; "the seam" is item 11's way to stand in for `git`, `gh` and `claude` in a Go test.

Items are in the order they are done. A state is `to do`, `done`, or, for a spike, `answered`. Items marked **(needs intake)** have an objective that is itself a design question, so the user takes each through a high-effort intake session before any work on it starts: 2 (the Contract convention, which item 4 enforces), 11 (the tool seam), 12 (the multi-machine crew) and 15 (roles and authority). Items 2, 11 and 12 are spikes: they open no PR, so see [Keeping the list true](#keeping-the-list-true). The "Settles" column names the findings an item answers; items 1 to 4 settle none, because they build what the rest relies on.

### Now, beside the records migration

These are small, and they touch little of what the records migration (moving crew's text-read facts onto the record store in `records.go`, begun in #119) is still editing.

| # | Item | Settles (finding) | Waits on (item) | State |
|---|------|---------|----------|-------|
| 1 | This document | none | nothing | done (this PR) |
| 2 | The Contract convention, settled by drafting the guard's Contract (a spike) **(needs intake)** | none | nothing | to do |
| 3 | CI runs the suites on every PR | none | nothing | to do |
| 4 | CI fails a PR that breaks the Contract | none | 2, 3 | to do |
| 5 | A clean restack or retarget starts no model session | 10 | nothing | to do |
| 6 | A failed lookup is never read as "nothing there" | 1 | nothing | to do |
| 7 | Every call to an outside tool has a time limit, no lock is held across the network, and a tick that didn't run shows in `crew status` | 3 | nothing | to do |
| 8 | The guard and the allow rules close the gaps the security review names: `project.env`, `worker-settings.json`, force-push to any ref, `crew evidence` taking any file | 4 (configuration half) | nothing | to do |
| 9 | `crew serve` and the phone lead are removed | 5 (in part) | nothing | to do |
| 10 | Proposals get triage | 11 | nothing | to do |

### Design first, as spikes

They change no code the migration touches, so they start now. Each ends in a report, and the build work it shapes is then added to this list.

| # | Item | Settles (finding) | Waits on (item) | State |
|---|------|---------|----------|-------|
| 11 | A seam for git, gh and claude, so a command's behaviour can be tested in Go **(needs intake)** | 8 (first half) | nothing | to do |
| 12 | A multi-machine crew, where some tasks or steps run on one machine and some on another. It replaces the host lease **(needs intake)** | 5 | nothing | to do |

### After the records migration has merged

Each of these rewrites code that every piece of the migration edits. Doing them first would mean rebasing each piece of the migration over them, or the reverse.

| # | Item | Settles (finding) | Waits on (item) | State |
|---|------|---------|----------|-------|
| 13 | A task's transition is one write that either lands whole or not at all, and a session is on record before it starts | 2 | the migration | to do |
| 14 | The legal status transitions are stated in one place and every command goes through it | 6 | the migration | to do |
| 15 | crew knows which role is calling, and a role can't clear its own gates **(needs intake)** | 4 (authority half) | the migration | to do |
| 16 | The tick calls its steps directly and hears from each what it did | 7 | the migration, 11 | to do |
| 17 | The e2e suite is split into cases that run alone, each tied to the obligation it proves | 8 (second half) | the migration, 2, 11 | to do |

### After the Contract exists

| # | Item | Settles (finding) | Waits on (item) | State |
|---|------|---------|----------|-------|
| 18 | Prompt rules that matter have a behavioural check, and `crew review-eval` is run | 9 | 2 | to do |
| 19 | `ARCHITECTURE.md` keeps what code can't say and drops the narration | 12 | 2 | to do |

Item 19 waits on the Contract because the Contract is what takes over the part of `ARCHITECTURE.md` that says what each component must do; what's left is the part code can't say.

### Why this order

- **Items 1 to 10 run beside the migration** because each is small and edits few of the files the migration edits: a document, CI, and changes to one command or one file's rules. The size of a change matters less than whether it overlaps: item 7 is not small, but it works on call sites, locks and a unit file, not on how a task is written.
- **Items 11 and 12 are spikes, started now,** because they only produce a design. No code the migration touches changes until the user has seen the answer.
- **Items 13 to 17 wait for the migration** because each rewrites a layer the migration is still moving. Item 13 changes how every command writes a task, item 14 every `SetStatus` and status comparison, item 15 the checks in `checkTaskSet` and the guard, item 16 the tick, and item 17 the tests that exercise all of them. Doing them first would mean rewriting the migration's pieces, or the pieces rewriting them.
- **Items 16 and 17 also wait on the seam (11).** The tick can only call its steps directly if a step can run without starting real `git`, `gh` and `claude`; the e2e suite can only be split into cases a Go test can run if there is a way to stand in for them.
- **Items 4, 17, 18 and 19 wait on the Contract.** Item 4 enforces it, 17 ties each test to an obligation in it, 18 checks the prompt rules it names, and 19 trims `ARCHITECTURE.md` once the Contract holds what it said.
- **Item 4 also waits on CI (3):** there is nothing to enforce in until CI runs on a PR. Items 2 and 3 are both urgent for that reason, and independent of each other.
- **Item 9 has no reason to wait for item 12:** the phone lead is going whatever the multi-machine design turns out to be.

### Keeping the list true

An item's state changes in the same PR that does the item's work, so this file on `main` never runs ahead of the code. A spike has no PR of its own, so its state is changed by the lead, in a small PR of its own, once the spike's report is in; that PR also adds the build items the report calls for. `CLAUDE.md` says the same under "When behaviour changes".

## The findings

Each entry says what is wrong, where in the code it shows, what it costs, the rule crew should hold that it doesn't, and the items that settle it. Paths are in `cmd/crew/` unless they say otherwise. Where an item changes a convention in `CLAUDE.md` or a design principle in `ARCHITECTURE.md`, the entry quotes it.

### 1. A failed lookup reads as "nothing there"

**What is wrong.** When `claude agents --json --all` fails, `agents()` returns an empty list, and code that starts or stops sessions takes the empty list to mean nothing is running. When `claude --bg` prints something other than a `backgrounded` line, the launch counts as "not started", which is the path that starts another session.

**Where.**

- `sessions.go`: `agents()` is `as, _ := c.listAgents(); return as`. `listAgents`, beside it, returns an error, and its comment says it is for "the callers that mustn't take a failure for nothing running". Only pause, upgrade and status use it.
- The callers that take the empty list as the answer: the tick's snapshot (`cmd_tick.go`, `as := c.agents()`), `followupRefusal`'s busy check and `startRestack` (`cmd_followup.go`), the same check in `cmd_feedback.go` and `cmd_goal.go`, the worker count in `dispatchChecks` (`cmd_dispatch.go`), `closeSession` and `removeSession` (`sessions.go`), and `waitForLaunch` (`sessions.go`).
- `launchSession` (`sessions.go`) discards `claude --bg`'s exit code and looks for a line that starts with `backgrounded`, taking its third field as the job. No such line returns `notStarted`. `relaunch` answers `notStarted` from a resume by starting a fresh session, and `crew dispatch` answers it by leaving the task `ready`, so the tick tries it again.
- `ARCHITECTURE.md` says "A failed `claude agents` is never read as nothing running" and names `listAgents`. That holds for pause alone.

**What it costs.** A failed listing during a tick makes a worker that is mid-turn look idle. `crew followup` then resumes its session, which Claude Code answers by starting a copy beside it (`closeSession`'s own comment says so), or `crew cleanup` removes the worktree under a worker still running. A reworded `backgrounded` line would have every dispatch and followup start a session, report failure and start another. `runningWorkers` counts only recorded jobs, so `MAX_WORKERS` would not see them.

**The rule.** A lookup that fails is an error the caller sees, never an empty answer, and a launch that might have started a session is never retried as if it had not.

**Settled by** item 6.

### 2. Ledger writes aren't safe

**What is wrong.** One status transition is about ten separate rewrites of one task file, nearly all with their errors dropped, and a worker's session is started before anything records it.

**Where.**

- `cmd_dispatch.go`: `launchSession` runs first. Only afterwards come `l.record(t.Doc)`, `t.SetStatus(Working)`, `t.Touch()`, `t.Log(...)` and the commit. None of their errors is checked. `t.Set("worker_name", name)` before the launch isn't either.
- `launch.record` (`sessions.go`) makes up to five `Set` calls, each a full read and rewrite of the file, and drops every error. `cmd_followup.go` has the same shape after a launch: `record`, two `Set`s, `SetStatus`, `Touch` and `Log`.
- `commit` (`push.go`) "never fails the command": when it can't commit it warns and carries on, so the command still prints "dispatched".
- `SetStatus` (`ledger.go`) also clears `go_ahead` with its own `Set` and `Log` before setting the status.
- What has changed on `main`: the review also found each rewrite truncating the file in place, so a full disk could empty a task file. #119 fixed that. `Doc.writeLines` now calls `replaceFile` (`ledger.go`), which writes a temporary file and renames it over the original, so a write that fails leaves the old file. The failure left is between the writes, not within one.
- Not measured on `main`: the review counted 127 calls in statement position that ignore the error and 11 that check it. That count came from a regex on `aa8782a`.

**What it costs.** A kill, a crash or a failed write between the launch and the records leaves a worker running that the ledger doesn't know about. The task is still `ready`, so the next dispatch starts a second worker in the same worktree. `unrecordedSession` was built to make this state impossible, and covers only the case where `claude` printed a job ID that doesn't parse. A kill between two of the `Set`s leaves a half-applied transition, such as a new `worker_job` beside the old `worker_session`.

**The rule.** A task's transition is applied once, so it either lands whole or not at all, and a session is on record before it starts.

**Settled by** item 13. It waits on the records migration, which is rewriting the same code. The migration alone doesn't settle it: the problem is that there is no "apply these changes to the task once" operation, whatever the storage.

### 3. Nothing times out and nothing says the tick is dead

**What is wrong.** No `gh`, `git` or `claude` call has a time limit, `crew sync` holds the ledger lock across GitHub reads, and the unit that runs the tick has no start timeout.

**Where.**

- A search of the non-test Go code for `context.`, `WaitDelay` and `Timeout` finds one use: `WaitDelay` on the Codex call in `cmd_codex.go`. `gh()` (`cmd_feedback.go`) and `git()` (`home.go`) are bare `exec.Command` calls.
- `cmdSync` (`cmd_sync.go`) calls `c.lock()` once and never unlocks. Under it, for every in-flight task of every project, it calls `prOfBranch` and `readPR` (each a `gh` call), and `git fetch` once for each project.
- `flock` waiters give up after 30 seconds (`lock.go`: "ledger is locked by another crew command"). The tick takes `.crew-tick.lock` without waiting, so a second tick prints "another crew tick is running" and returns.
- `systemd/crew-tick.service` is `Type=oneshot` with no `TimeoutStartSec`. The launchd twin has no timeout either. Systemd doesn't start a oneshot unit that is still running, so a hung tick stops all later ones.
- `handOverPR`'s comment (`cmd_new.go`) says it reads the branch and the PR "before crew hand-over takes the lock, so a slow network doesn't hold up other crew commands". Sync does the opposite.
- `crew cleanup` holds the lock across a session stop (which waits up to 10 seconds) and the worktree removal's hooks. Dispatch lets go of the lock for the same hooks.

**What it costs.** When a `gh pr view` or `git fetch` inside sync blocks on a dead connection, the lock stays held, so every worker's `crew log`, `crew set` and `crew hand-over` fails after 30 seconds. The tick never ends, so nothing is routed, dispatched or pushed. Nothing says so: `crew status` shows the last sync, and no news line is written for a step that failed. Even without a hang, a sync over enough PRs holds the lock past 30 seconds. How long a real sync takes on this ledger has not been measured.

**The rule.** Every call to an outside tool has a time limit, no lock is held across a network call, and a tick that didn't run shows in `crew status`.

**Settled by** item 7.

### 4. crew has no notion of who is calling

**What is wrong.** Workers and goal leads are pre-approved to run any `crew` command, and the checks in the CLI and the guard work on text the caller writes about itself. The allow lists admit more than the docs say they do.

**Where.**

- `config/worker-settings.json` and `lead/.claude/settings.json` (which goal leads share) allow `Bash(crew *)`. The binary has one role check, `inWorkerOrGoalLead()` (`sessions.go`), and it decides only whether to record news. `LEDGER.md` says "`crew` can't tell a lead from a worker".
- `checkTaskSet` (`cmd_ledger.go`) refuses `status done` and `status pr-open` and nothing else a caller shouldn't set. A worker can run `crew set <task> auto_review no` or `risk low`, `crew ready`, or `crew dispatch` on any task. Automated review's gate reads the `### Automated review N` headings the worker writes (`ledger.go`).
- The guard (`guard.go`) special-cases only `memory.md`, `mcp.json`, `mcp.local.json`, `ledger-format` and `*.records.jsonl` under `projects/`. #119 added the last two. Every other file there, `project.env` and `worker-settings.json` among them, is open to every role. `worker-settings.json` is merged into every later worker's settings, and `WORKER_PERMISSION_MODE` from `project.env` is passed to `--permission-mode`. Not measured: whether any later value is validated; none was found.
- `config/worker-settings.json` allows `git push --force-with-lease *` and `--force-with-lease=*`, so it applies to any ref. Only `git push --force *` and `-f *` are denied. `implementer.md` and SETUP.md say the lease must name the commit it expects, and allow rules skip the classifier that would check that.
- `crew evidence` (`cmd_evidence.go`) checks only that each file is a file. Any readable path goes into the evidence repo under any task, and that repo's file header says nothing in it is ever deleted or rewritten.
- `config/gardener-settings.json` and `config/readonly-settings.json` allow `sort *`, `uniq *`, `git diff *`, `git log *`, `git show *`, `git grep *` and `WebFetch(domain:*)`. Not measured: whether Claude Code lets `sort -o`, `git diff --output` or `git grep -O` through these patterns; the review read the rules and did not run them.
- Not measured: the reviewers also found that every worker can read the whole home, other projects' tasks and `local.env` included, because the home is added with `--add-dir`.

**What it costs.** A worker led astray by what it reads can clear a gate the docs say only the user or lead can clear, or can publish a file from the machine it runs on. A commit with injected instructions, read by the unattended gardener, can edit `worker-settings.json` and have every later worker run a hook. How likely any of this is depends on how a model behaves; the review found no case of a worker taking it.

**The rule.** crew knows which role is calling, and a role can't clear its own gates; the guard and allow lists admit only what the docs say each role may do.

**Settled by** two items. Item 8 is the configuration half and needs no role: the two files, the force-push rules and `crew evidence`'s inputs. It goes first. Item 15 is the authority half: it needs crew to know the role, which touches `checkTaskSet` and the commands the migration is editing, so it waits for the migration. Item 15 changes a claim `LEDGER.md` and `ARCHITECTURE.md` make ("`crew` can't tell a lead from a worker"), and updates both.

### 5. The host lease is to be replaced, not repaired

**What is wrong.** `crew host up` runs `git reset --hard @{u}` after any failed push without saving what is unpushed, and a host that can't fetch origin never learns it lost the lease. The user means to replace the lease design with a multi-machine one, not to fix these.

**Where.**

- `claimLease` (`cmd_host.go`) loops up to five times: `pull --ff-only`, write `host`, `commitHost`, and on failure `gitOK(c.Home, "reset", "-q", "--hard", "@{u}")`. `commitHost` returns whether `git push -q origin HEAD` worked, so any failure of the push, not only another machine moving first, reaches the reset. The pull succeeds when the home is merely ahead of origin.
- `standDown` (`cmd_tick.go`) calls `saveLostWork` before its own reset. `claimLease` saves nothing.
- `leaseTakenElsewhere` (`cmd_tick.go`) returns "not taken" when `git fetch -q origin` fails. `requireHost` (`lease.go`) reads only the local `host` file and the local lease-lost marker. The marker is written only when a push is rejected, which needs a push that reaches origin.
- The machine's identity is its short hostname (`machineName`), so two machines with the same name both pass `requireHost`.

**What it costs.** When pushes have been failing and someone reruns `crew host up` "to fix it", the reset throws away every unpushed ledger commit and every uncommitted tracked change in the home, a lead's draft included. Separately, when machine A can't reach origin, `crew host up --take` on machine B doesn't stop A: both dispatch, resume and force-push against the same tasks and PR branches. Design principle 8 says "two machines can't both believe they hold it", which is stronger than what the code enforces.

**The rule.** The user's direction, 2026-11-27: "I want to change the host system (#5) -- we need a multi-machine set-up, so some tasks/task steps run on host A and some run on host B. That changes the entire concept of host takeover." The lease, and the failover that stands on it, are to be replaced by a design in which machines share the work. No part of the lease is to be mended first.

**Settled by** item 12, a spike that designs the multi-machine crew; the build work it calls for is added to this list when its report is in. Item 9 removes `crew serve` first, since the phone lead is going whatever that design is. Item 12 goes against a design principle; see [What changes a stated rule](#what-changes-a-stated-rule).

### 6. The status machine isn't written down

**What is wrong.** `SetStatus` takes any status from any status, and each command decides what is legal for itself.

**Where.**

- `Task.SetStatus` (`ledger.go`) sets what it is given, and clears `go_ahead` when the new status isn't `ready`.
- Nine call sites in five files: `cmd_followup.go` (four), `cmd_ledger.go`, `github_pr.go`, `cmd_new.go` (two) and `cmd_dispatch.go`. `checkTaskSet` refuses only `done` and `pr-open` as the target of `crew set TASK status`, so `merged` to `working` or `draft` to `merged` both pass.
- "Which status does `needs-input` return to" is found by reading the Log backwards (`statusBeforeNeedsInput`, `cmd_followup.go`).
- Proposal statuses (`open`, `accepted`, `deferred`) are string literals in `cmd_proposal.go` and elsewhere; there is no type.
- Not measured on `main`: the review counted 65 comparisons of a status in 18 files.

**What it costs.** The legal transitions can be learned only by reading every command. A stray `crew set ... status ...` can leave a task in a state no command expects, and a new status needs every call site found by hand.

**The rule.** The legal status transitions are stated once, in one place, and every command changes a status through it.

**Settled by** item 14. It waits for the migration, which edits the same call sites. It must also fit two conventions in `CLAUDE.md`: "A status is a value of `Status` or `GoalStatus` in `ledger.go`; a Go test checks they match `LEDGER.md`" and "Front matter stays flat `key: value`". Neither is expected to change; if the design needs one to, that goes to the user first.

### 7. The tick runs crew's own commands as child processes and infers what they did

**What is wrong.** `crew tick` starts each of its steps as a separate process, and learns what a step did from its exit code, from reading the ledger before and after, and from the last line it printed. Because the tick has to predict what a step will decide, the rules are written twice.

**Where.**

- `step` (`cmd_tick.go`) runs `os.Executable()` with the step's arguments, throws away its output and returns the exit code. It is called at 13 sites in `cmd_tick.go`.
- Results come back three ways: exit codes 0, 3 and 4 (as `notYet` and `exitNotYet`); a before-and-after comparison of `review_rounds` and the open questions; and the last line of stderr with `crew: ` trimmed off (`splitRefusal`).
- `forWorker`'s restack branch (`cmd_tick.go`) restates the rules of `cmdRestack`, `sendRetarget` and `sendRebase` (`cmd_followup.go`). Its own comment says "As in crew restack".
- "Rounds unsent" (`review_rounds` above `last_round_sent`) is computed in the tick and in `cmd_followup.go`.
- `operation.go` is the abstraction meant to replace this: a command an in-process caller can run, reporting through `c.warn` and `c.printf`. It covers `followup` and no other command.
- `cmd_tick_test.go` is 20 lines and has one test. A step's decisions can be tested only through `tests/crew-e2e.sh`.

**What it costs.** The scheduler, the part with the most decisions, cannot be tested in Go. Every rule a step applies has to be kept in line with the tick's prediction of it by hand. A tick over N tasks forks several processes for each.

**The rule.** The tick calls its steps in-process and hears from each what it did, so that each rule is stated once.

**Against a stated convention.** `CLAUDE.md` says: "**`crew tick`** only runs other `crew` commands, each as a process of its own (`step`), so a refusal skips one step and not the rest. Give it new duties as `crew` commands first." That convention is to change. What it is for, one step's refusal not stopping the rest, has to be kept some other way, such as each step returning an error the tick records and carries on from. `ARCHITECTURE.md`'s `crew tick` section and its "Operations" part change with it.

**Settled by** item 16, which waits for the migration and for the seam (item 11). Two open proposals overlap: that the tick should hear from each step and not re-read the ledger, and that each ledger file should be read once per command.

### 8. There is no seam for git, gh or claude

**What is wrong.** Commands call `git`, `gh` and `claude` through package functions, so nearly all command behaviour can be tested only through the bash suite, which is one linear script that shares one world.

**Where.**

- `git`, `gitOK` and `gitRun` (`home.go`), `gitDo` (`cmd_project.go`), `gitTo` (`push.go`) and `gh` (`cmd_feedback.go`) are package functions. 17 non-test files call `exec.Command`.
- No Go test file exists for `cmd_dispatch`, `cmd_feedback`, `cmd_new`, `cmd_sync`, `cmd_cleanup`, `cmd_pause`, `cmd_catchup`, `cmd_proposal`, `push.go` or `worktree.go`.
- `followup`, the one command tested in Go, needs a harness of its own in `cmd_followup_test.go` that builds real git repos and puts the bash stubs from `tests/stubs/` on `PATH`. A Go test of it depends on bash and jq.
- `buildReport` takes a `statusReads` value and runs nothing, and its tests are fast. It is the model for the rest.
- `tests/crew-e2e.sh` is 3,360 lines with 871 `check` lines and 160 mentions of `crew tick`. It cannot run one case alone: state made early is asserted on late. Not measured: its run time, and the review's counts of long lines, `sed -i` setups and sleeps.
- The Go code in `cmd/crew/` is 28,697 lines, of which 7,951 are tests, in 32 test files.

**What it costs.** A failure early in the suite can cascade, and one late needs the whole run to reproduce. Every command that follows `followup` into Go tests needs the same harness or stays in bash. The shell suites run on Linux only, so macOS gets `go test` and nothing else.

**The rule.** A command's behaviour can be tested in Go without a real `git`, `gh` or `claude`, and a test of one command doesn't depend on another's.

**Settled by** two items. Item 11, a spike, designs the seam. Item 17 splits the e2e suite into cases that run alone, each tied to the obligation it proves, once the seam and the Contract exist. The seam is to be built on the standard library: `CLAUDE.md` says "Don't add other Go modules", and a design that needs one goes to the user first.

### 9. Prompts have no behavioural test

**What is wrong.** The agents' prompts change faster than anything else in the repo, and the only check on them is that sentences are present.

**Where.**

- `tests/guard.sh` is 534 lines; 184 contain `grep -q`, most of them a sentence searched for in a prompt, such as the check for "Push after every commit". Such a check fails when wording changes and passes whatever the agent does.
- `crew review-eval` (`cmd_review_eval.go`) is the one behavioural eval. The maintainer reports that it has never been run; the repo can't show that, so it is not checked here.
- `ARCHITECTURE.md` says of itself, under "Live verification": "the lead, implementer, gardener and misses agent haven't been run end to end against a real model. Do a smoke test with a tiny task after any significant change."
- `lead.md` quotes the implementer's step numbers (first-run steps 5 and 9, for the security review), so renumbering one file breaks another's instructions. `tests/guard.sh` covers some of these by regex.

**What it costs.** Regressions in what an agent does show up in real work, noticed by the user. Not measured: how often.

**The rule.** A prompt rule that matters is checked by what the agent does, and the behavioural eval is run.

**Settled by** item 18, which waits for the Contract: the Contract says which rules matter.

### 10. Model sessions do deterministic work

**What is wrong.** A restack with no conflict, and a retarget of a PR, are fully worked out by crew, and crew then starts a model session to type the commands.

**Where.**

- A retarget is one `gh pr edit --base`. `sendRetarget` (`cmd_followup.go`) has a fresh worker do it.
- A restack is `git rebase --onto <sha> <sha>`, a leased push, and when needed `gh pr edit --base` and `crew set base`. `rebaseFor` computes all of it, and `sendRebase` and `startRestack` then start a worker on `restackModel`, which is `"sonnet"` (`cmd_followup.go`), with the commands in a note. The session loads `implementer.md`.
- crew already calls `gh` itself for replies, reactions and summaries.
- A stack k deep restacks one level at a time, each level waiting for the one below to push and for the next 3-minute tick.
- Not measured: the review's counts of restacks and nudges over six days, and the token cost of each.

**What it costs.** Tokens and latency for a step with no judgment in it when the rebase is clean. Each session is also another chance to stall or improvise. A model is needed only when the rebase conflicts or the checks fail afterwards.

**The rule.** A clean restack or retarget is done by crew, and a model session is started only for a conflict.

**Against a stated convention.** `CLAUDE.md` says: "`crew restack` only starts a fresh worker on Sonnet with the rebase or retarget to run; it never resumes the worker's conversation." That is to change: the command does the clean case itself and starts a session for a conflict, and `ARCHITECTURE.md`'s "Stacked tasks" flow and the "Stacked tasks" and "Workflow: restack" sections of `implementer.md`, which `tests/guard.sh` checks agree with the restack note, change with it.

**Settled by** item 5. An open proposal, "crew does clean restacks itself, and only conflicts start a session", is the same work.

### 11. Proposals have no triage

**What is wrong.** Prompts tell workers to file a proposal for work outside their PR, and nothing sorts, ages out or caps what is filed, while every intake rereads the list.

**Where.**

- The inflow is the implementer's rule that a finding outside the PR's lines becomes `proposal <its file name>`, and that follow-ups worth keeping are filed with `crew propose` (`implementer.md`, first-run step 8 and "Workflow: automated review" step 2).
- `cmdProposals` (`cmd_proposal.go`) prints a flat list with `--any-status`, `--all` and `--project` and no filter by source, age or size. `crew status` shows a count.
- The lead reads the list at every intake, and the gardener and misses agent are told to read every existing proposal first.
- Not measured here: how many are open now, how fast they are filed, and how few are settled. The review gave figures from the ledger, and the ledger is private.

**What it costs.** The pile only grows, and what reads it grows with it: intake's duplicate check is a model comparing titles across a longer list each week. A proposal that matters is no easier to find than one that doesn't.

**The rule.** Every proposal is sorted soon after it is filed, so that the open list stays short enough to read.

**Settled by** item 10. How is open: it may be a cap, an expiry, a grouping, or a different place for the lowest-value ones.

### 12. `ARCHITECTURE.md` narrates the code function by function and has drifted

**What is wrong.** `ARCHITECTURE.md` restates what the code and its doc comments say, at length, and parts of it are no longer true.

**Where.**

- It is 1,122 lines and 40,709 words (`wc`). Its `crew` internals section is followed by a Contracts table of 116 rows (as of `f5b790d`).
- Drift that can be checked now:
  - "Eight Claude Code agents" (`ARCHITECTURE.md`, line 7), against `LEDGER.md`'s opening line, which lists the lead, the implementer, the casual observer, the gardener and the misses agent.
  - `MAX_WORKERS` is described in `config/defaults.env` as workers "running or waiting on you", in SETUP.md as "Your review capacity isn't exceeded", and counted by `runningWorkers` (`sessions.go`) as busy jobs only.
  - The Assumptions section says "the lead, implementer, gardener and misses agent haven't been run end to end against a real model", and carries many "not confirmed" and "None has run yet" notes. Nothing says which are still true. A search for `not confirmed`, `haven't been` and `None has` finds 12 lines.
  - `LEDGER.md` and `ARCHITECTURE.md` say "`crew` can't tell a lead from a worker", while `inWorkerOrGoalLead()` exists.
- Not measured: the review's finding that every Go identifier `ARCHITECTURE.md` names resolves, and its counts of how many recent commits also edit the file.

**What it costs.** A standing tax on every change, and a file too long to read before "any structural change", as `CLAUDE.md` asks. Once it holds claims that are false, a reader can't tell which.

**The rule.** `ARCHITECTURE.md` holds what code can't say (the principles, the flows, the assumptions about Claude Code, the Contracts) and leaves the rest to the code.

**Settled by** item 19, after the Contract exists: the Contract is where the part of the narration that says what each component must do goes. Item 19 trims and rewrites; this document only says it is to.

## What changes a stated rule

The items below are against a rule that `CLAUDE.md` or `ARCHITECTURE.md` states today. The owner of the item changes the rule in the same PR as the code, and the user's say comes first (`CLAUDE.md`: "A change that weakens any of the design principles needs the user's say first").

| Item | The rule | What changes |
|------|----------|--------------|
| 5 (finding 10) | `CLAUDE.md`, Stacks: "`crew restack` only starts a fresh worker on Sonnet with the rebase or retarget to run; it never resumes the worker's conversation." | A clean restack or retarget starts no session. |
| 16 (finding 7) | `CLAUDE.md`, Conventions: "`crew tick` only runs other `crew` commands, each as a process of its own (`step`)". | The tick calls its steps in-process. |
| 12 (finding 5) | `ARCHITECTURE.md`, design principle 8: "One machine runs the crew at a time. The home's `host` file names it, and a git push claims it, so two machines can't both believe they hold it." `CLAUDE.md` lists it among the principles the README's pitch rests on. | Some tasks or steps run on one machine and some on another. The user asked for this, in their own words: "we need a multi-machine set-up". |
| 9 (finding 5) | `README.md`: "You talk to a **lead**, at a terminal or from the Claude app on your phone." | The phone lead (`crew serve`, `cmd_host.go`) goes, and the README, SETUP.md and the Contracts rows for it change. |
| 15 (finding 4) | `LEDGER.md` and `ARCHITECTURE.md`: "`crew` can't tell a lead from a worker". | crew knows the caller's role. |

The seam (item 11) and the status table (item 14) have to fit "Don't add other Go modules" and "Front matter stays flat `key: value`". They don't need to change them.

## Open proposals that overlap

Each was filed by the lead or a worker and is still open. Taking an item is also taking its proposal.

- Item 5: "crew does clean restacks itself, and only conflicts start a session".
- Item 6: "Refuse a resume when claude agents fails, instead of resuming blind" and "Say the worker count is unknown when claude agents fails".
- Item 7: "Bound how long crew status waits for GitHub".
- Item 13: "Read each ledger file once per crew command", which also bears on item 14.
- Item 14: "crew set refuses statuses that have their own command".
- Item 16: "crew tick hears what each step did from the step, not by re-reading the ledger".
- Items 2 and 4: "Make crew an out-of-the-box IMP project", which concerns the Contract. The Contract is the one IMP states (impatientprogramming.org/rules): numbered, plain-sentence obligations for each component, each tied to the tests that prove it.

## What the user has said

- On the review: "All 12 of those need to be addressed, particularly the design-level problems ('Model sessions do deterministic work' is really wasteful)." and "The E2E tests (touched on by the lack of tool seams called out by #8) particularly bother me."
- On CI and the Contract: "We also need CI, and I want the contract enforced in CI. Both of those are separate tasks that we need ASAP." CI exists today only for releases: `.github/workflows/release.yml` runs when a `v*` tag is pushed and runs nothing on a PR.
- On the multi-machine crew and the phone lead: "I want to drop the `crew serve` remote lead, and I want to change the host system (#5) -- we need a multi-machine set-up, so some tasks/task steps run on host A and some run on host B."
- On this document: it is published as it is, security findings included.
