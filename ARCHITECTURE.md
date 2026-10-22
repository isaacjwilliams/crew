# Architecture

This document is for anyone changing the crew, human or Claude Code. `SETUP.md` covers installing and using it. This file explains how the pieces fit together, the contracts between them, and the invariants that changes must preserve.

## What the system is

Six Claude Code agents run on one machine at a time (principle 8):

- **lead**: the only agent you talk to, for every project, from a terminal or from Claude Mobile. It turns requests into briefs, dispatches work, routes PR feedback, and reports status.
- **implementer**: one background session per task, each in its own git worktree and branch. It opens a PR and addresses review rounds.
- **casual-observer**: a read-only readability review, spawned by workers before a PR (or used ad hoc).
- **plan-reviewer**: a read-only review of a finished brief's structure, spawned by the lead before the user approves it.
- **gardener**: runs on a schedule and turns merged commits into refactor *proposals*, never code.
- **goal-lead**: one background session per goal the user starts. It does the lead's intake for every item of the goal, without the user.

They coordinate through the **ledger**: Markdown files under `projects/` in the crew home, a clone of the person's private repo, driven by a CLI, `crew`, and pushed to that repo as they change. The crew's code is a public repo; each person's private repo is a copy of it with the ledger added.

## Design principles

These are invariants. Breaking one changes the design, not just the implementation.

1. **Nothing coordination-related touches the shared repo.**
   - No labels, comments other than the replies below, committed config, or files in the working tree. The repo belongs to other people too.
   - The only repo-side footprints are:
     - task branches pushed under `BRANCH_PREFIX`, after every commit;
     - the PRs workers open;
     - optional one-line replies to the user's own review comments: in the thread for an inline comment, otherwise as a PR comment that quotes the one it answers;
     - two lines in `.git/info/exclude`, which is local to the clone.
2. **The ledger is the single source of truth, it's plain text, and it's pushed to the person's private remote.**
   - Every change is committed at once and pushed in the background, normally within seconds, so another machine can take over with what this one knew. A push that fails is retried by the next commit, and by every `crew tick`.
   - Agents never rely on their own memory for state. The lead is effectively stateless: every session starts by reading the ledger.
   - That holds for a long conversation too. The lead opens a draft task once the goal is agreed and writes each decision into it as it's made; the lead and workers reread the task file after a compaction or a resume. Compaction and fresh sessions lose nothing that matters, so no conversation has to live forever.
   - Task files hold the brief, the decisions, the review rounds, a log, and the worker's identifiers.
3. **Work has identity; sessions are disposable.**
   - A task file records how to reach its worker (`worker_session`, `worker_job`, `worker_name`) and where its work lives (`worktree`, `branch`, `pr`).
   - Routing feedback is a lookup, not something the lead has to remember.
   - Any worker can be replaced by a fresh one that rebuilds context from the task file and the branch.
4. **Judgment goes in at the plan, not the diff.** The lead's intake produces a brief the user approves before any code is written. The observer and the brief standard exist to make diff review a confirmation step. Everything after the plan keeps its design fixed, so the decision most expensive to change later, where the code lives, gets its own required section (Structure, with the alternative that lost) and its own line in the summary the user approves.
   - **Goal tasks are the exception.** For the tasks of a goal the user started, the plan review and the goal lead stand in for the user's approval of the plan, so the user's judgment reaches the work at the PR instead. A wrong structure there costs a PR the user closes, not a plan the user fixes. Parking keeps the decisions that are expensive to undo with the user: anything irreversible, anything outside the goal, and any plan-review finding the goal lead can't settle with confidence.
5. **Throughput is capped by review capacity.**
   - `MAX_WORKERS` limits concurrent sessions.
   - `MAX_IN_FLIGHT` limits dispatched-but-unmerged tasks, including open PRs. A stack of tasks counts once (see [Stacked tasks](#stacked-tasks)).
   - `crew dispatch` enforces both under the ledger lock.
6. **The user is the only one who merges.**
   - The agent prompts say exactly what each agent may do on GitHub. For workers that is: push their own task branch (with `--force-with-lease` allowed after a rebase), open a PR, and edit it. Merging stays with the user.
   - We rely on the prompts for GitHub behaviour and don't try to parse shell commands for it; that approach can't be made complete. `deny` rules in the settings files catch the obvious mistakes (`gh pr merge`, `git push --force`). Branch protection, where the repo allows it, is the real backstop.
   - File edits are enforced mechanically: `crew guard` checks Edit/Write paths. Bash isn't sandboxed (see [Bash](#bash-no-sandbox)). Workers run it in auto mode, the same trust you give your own sessions, and the gardener only runs its read-only allow list.
7. **Project-agnostic agents, project-specific conventions.**
   - Agents live at user level (`~/.claude/agents/`).
   - Project conventions come from the repo's `AGENTS.md`/`CLAUDE.md`, which Claude Code loads automatically for every session in the repo. The lead and plan-reviewer, which run outside every repo, read it explicitly. The user's personal taste goes in `~/.claude/CLAUDE.md` or `~/.claude/rules/`.
   - Agent prompts should stay generic.
8. **One machine runs the crew at a time.**
   - The home's `host` file names it, and a git push claims it, so two machines can't both believe they hold it.
   - Every change to the ledger checks it, and so does everything that starts a session, so two machines never dispatch the same task. Other machines only read.
   - Moving the crew is `crew host up --take` on the new machine. Work in flight carries on there with fresh workers: sessions stay behind, but task files and pushed branches don't.

## Repository layout and where each file installs

A clone of this repo is a **crew home** (`~/crew` by default): `crew` runs from its `bin/`, reads its `config/` and `templates/`, and keeps the ledger under `projects/`. `crew install` copies only what Claude Code, fish and systemd have to find elsewhere.

This repo is the public one, and never contains state: nothing under `projects/` but `.gitkeep`, and no `reports/`, `config/crew.env` or `host`. A person's private home adds those (see [Runtime state](#runtime-state-created-by-use-never-shipped)), so merging a new public version into it never conflicts on them.

| In this repo | Installed to | Role |
|---|---|---|
| `agents/lead.md` | `~/.claude/agents/` | Lead system prompt, tool denylist, `initialPrompt` briefing (terminal sessions only), write-guard hook (role `lead`) |
| `agents/implementer.md` | `~/.claude/agents/` | Worker prompt: first-run and review-round workflows, stop-and-ask rules; GitHub allowances; tool denylist; write-guard hook (role `worker`) |
| `agents/casual-observer.md` | `~/.claude/agents/` | Read-only reviewer: context budget, what to flag, output format; Sonnet, medium effort |
| `agents/plan-reviewer.md` | `~/.claude/agents/` | Read-only plan reviewer: gets only the task file path; reads the code at the task's base on `origin`; runs in the background (`background: true`); the structural questions, report format; Opus, high effort |
| `agents/goal-lead.md` | `~/.claude/agents/` | Goal lead prompt: intake without the user (it reads the intake sections of the installed `lead.md`), parking rules, the new-goal and new-input workflows; tool denylist; write-guard hook (role `lead`) |
| `agents/gardener.md` | `~/.claude/agents/` | Proposal writer: survey process, dedup, max 3 proposals; write-guard hook (role `gardener`) |
| `bin/crew` | — | The launcher, a POSIX `sh` script: runs `bin/release/crew-<VERSION>`, downloading it from that release the first time (below) |
| `bin/crew-guard` | — | PreToolUse hook: two lines of `sh` that run `crew guard` (below) |
| — | `bin/release/crew-<version>` | The binary for this machine, from the release; gitignored |
| `cmd/crew/` | — | The CLI, in Go (below); `go.mod` names the module |
| `.github/workflows/release.yml` | — | On a pushed `v*` tag in the public repo: tests, builds the Linux binaries and publishes them as the release |
| `config/defaults.env` | — | Every setting, its default and its comment; read by `crew` |
| `templates/crew.env` | — | The short starting point for a person's `config/crew.env` (their overrides of `defaults.env`) |
| `lead/.claude/settings.json` | — | The lead's session, in `lead/`: `"agent": "lead"`, its permission `allow`/`deny` rules, and the `crew-guard` hook (role `lead`), which covers its subagents. Goal leads get the same file without `agent`, as a JSON string to `--settings` |
| — | `lead/.claude/settings.local.json` | Written per machine by `crew install`, gitignored: `permissions.additionalDirectories` (the home and `WORK_ROOT`) and `claudeMdExcludes` (the home's `CLAUDE.md`, which is for developing crew) |
| `config/{worker,readonly}-settings.json` | — | Passed with `--settings`: permission `allow`/`deny` rules for workers (`worker`), and the gardener and `crew observe` (`readonly`); the worker file also hooks `crew-guard`, which covers their subagents |
| `templates/task.md` | — | Task file shape: front matter and sections |
| `templates/goal.md` | — | Goal file shape: front matter and sections |
| `templates/proposal.md` | — | Gardener proposal shape |
| `LEDGER.md` | — | Ledger conventions |
| `VERSION` | — | crew's version, `X.Y.Z`; `crew upgrade`, `crew install` and `crew doctor` show it |
| `projects/.gitkeep` | — | Where the ledger goes |
| `.gitignore` | — | Ignores what crew keeps per machine: `bin/release/`, `logs/`, the `.crew*.lock` files, and `tests/.tmp/` |
| `fish/conf.d/agent-crew.fish` | `~/.config/fish/conf.d/` | Sets `CREW_HOME` and puts `crew` on PATH |
| `fish/functions/lead.fish` | `~/.config/fish/functions/` | `lead` = `claude --name lead --permission-mode <LEAD_PERMISSION_MODE> …` in `$CREW_HOME/lead`; the agent and settings come from that directory |
| `fish/completions/crew.fish` | `~/.config/fish/completions/` | Subcommand, task-ID and proposal-ID completion |
| `systemd/crew-gardener.{service,timer}` | `~/.config/systemd/user/` | Weekly `crew gardener --all` (installed but not enabled) |
| `systemd/crew-tick.{service,timer}` | `~/.config/systemd/user/` | `crew tick` every 3 minutes, `KillMode=process` so the sessions it resumes outlive the run (installed but not enabled) |
| `systemd/crew-lead.service` | `~/.config/systemd/user/` | `crew serve`: the lead as a Remote Control server named `crew` in the home's `lead/`, for Claude Mobile. Restarted unless `crew serve` exits 3 (this machine doesn't run the crew). Enabled by `crew host up`. See "The lead as a service" under Assumptions |
| — | `~/.local/share/agent-crew/` | `crew install`'s state: `manifest` (every file the last install shipped), `backups/<timestamp>/`, `agents-md-declined` |
| `SETUP.md`, `README.md`, `ARCHITECTURE.md` | — | Documentation |
| `CLAUDE.md` | — | Instructions for Claude Code when working in this repo |
| `tests/` | — | Test suites, stubs and runner (see `tests/README.md`); sandboxes go in the gitignored `tests/.tmp/` |

`crew install` copies files; it doesn't symlink them. Edit them in the home, then run `crew install` again. If you edit the installed copies directly, copy the changes back or they'll be overwritten (a backup is kept in `~/.local/share/agent-crew/backups/<timestamp>/`).

`crew install` also edits `~/.claude/settings.json`, always after taking a backup:
- `env.CREW_HOME`, so hooks and sessions find the home;
- `pluginConfigs."agents-md@builtin".options.instructionFiles = "claude-md-and-agents-md"`, if the user opts in.

## Runtime state (created by use, never shipped)

```
~/crew/                                 the crew home; crew commits the ledger (projects/, reports/) after every
                                        mutation, and pushes it to origin, the person's private repo
  bin/release/crew-<version>            the binary bin/crew runs, downloaded from its release; not committed
  config/crew.env                       the person's own settings, over config/defaults.env (written by crew setup)
  reports/                              free-form notes the lead keeps
  projects/<owner>-<repo>/
    project.env                         REMOTE, DEFAULT_BRANCH, per-project overrides of the settings
    local.env                           REPO, this machine's clone; not committed
    worker-settings.json                optional, hand-written: settings added to workers' (add-only)
    tasks/NNN-<branch-name>.md          one task each (`crew new --branch`, else from the title)
    goals/NNN-<slug>.md                 one goal each (`crew goal new`)
    proposals/YYYY-MM-DD-slug.md        gardener output
    gardener.last                       last commit the gardener reviewed
  logs/gardener-*.log                   not committed
  logs/worktree-<p>-<id>.log            Worktrunk hook output from dispatch/cleanup; not committed
  .crew.lock                            flock target, not committed
  .crew-tick.lock                       held while a crew tick runs, not committed
  .crew-push.lock                       held while the ledger is pushed, not committed
  host                                  the machine that runs the crew (the lease), committed and pushed by crew host
  .crew-lease-lost                      a push found the lease moved to another machine; not committed
  logs/push.log                         output of background ledger pushes; not committed
~/agent-work/<owner>-<repo>/
  <task-id>/                            git worktree on the task branch (one per dispatched task)
  _gardener/                            detached checkout of origin/<default> for the gardener
  _goal-<goal-id>/                      detached checkout of origin/<default> for a goal's goal lead
  _lead/                                detached checkout of origin/<default> the lead reads code in (`crew checkout`)
```

The home's own files (code, config, templates) are never part of a ledger commit: `commit` adds only the state paths.

## Main flows

### Intake and dispatch
```
you ⇄ lead (a session in <home>/lead, for every project; reads code in crew checkout <p>)
  lead: confirm goal → crew new "<title>" --branch <name> → investigate → plan → acceptance criteria
        → plan-reviewer subagent in the background (task file path only) → summary right away
        → review arrives → findings settled → Raise findings to you
        (each point written into the draft task file as it's agreed)
  you: "go"                             # if the review is still running, the lead dispatches once it's settled
  lead: crew ready TASK                 # checks Goal / Structure / Plan / Acceptance criteria are non-empty
        crew dispatch TASK
          ├─ lock; check status, depends_on (same project, must be merged), MAX_WORKERS / MAX_IN_FLIGHT; unlock
          │     a stacked task's parent is exempt from "must be merged", and the task from MAX_IN_FLIGHT
          │     when its parent is in flight; a parent that has already merged → base rewritten to its base
          ├─ git fetch origin <base> (must succeed for a stacked task: the parent's branch has to be pushed);
          │   create ~/agent-work/<p>/<id> on a new <branch> from origin/<base>, recorded as base_sha:
          │     WORKTREE_TOOL=git        git worktree add --no-track -b …
          │     WORKTREE_TOOL=worktrunk  wt switch --create … --no-cd --format json --config-set worktree-path=…
          │                              (runs the repo's pre-start hooks; output → logs/worktree-<p>-<id>.log)
          ├─ copy WORKTREE_COPY files
          ├─ lock; record worktree; repeat the checks
          ├─ cd worktree; claude --add-dir <home> --bg --agent implementer --permission-mode … \
          │     --autocompact <WORKER_AUTOCOMPACT_WINDOW> --settings <worker settings> --name crew-<project>-<id> "<prompt>"
          ├─ strip color codes; parse "backgrounded · <job>"; poll `claude agents --json --all` until the job's
          │     process reports a session ID that starts with the job ID (job `failed` → stop, record nothing)
          └─ record worktree, worker_name/job/session; status → working
```

### Worker first run
The worker reads the task file, then implements with commits, pushing its branch after each one so its work is never only on one machine, and runs the checks from `AGENTS.md`. Next it spawns casual-observer (if `pre_pr_review: yes`), fixes the findings it agrees with, and logs the ones it declines. It pushes, runs `gh pr create` (draft by default), and records the result with `crew set TASK pr …`, `crew set TASK status pr-open` and `crew log`. If it gets blocked, it writes an Open question, sets `status needs-input`, and ends its turn with the question.

### Review feedback
```
you review on GitHub → tell the lead (or run it yourself):
  crew feedback TASK     # your comments newer than feedback_since → "### Round N" before "## Log"
                         # collaborators' comments (write access, not bots) → unchecked items under Open questions
                         # skips: anyone else's comments, replies tagged <!-- crew -->,
                         #        comments in PENDING reviews; inline comments timed by review submitted_at
  crew followup TASK     # refuses if the worker is busy (mid-turn or on an open prompt), or if there's no new round and no --note
                         # a worker that isn't busy but whose process is still open (pid set) → claude stop <job> first
                         # resume: claude --resume <worker_session> --bg "<prompt>" in the worktree, with no launch flags:
                         #   the session keeps its own agent, permission mode, settings and --add-dir, and
                         #   passing any of them again makes Claude Code start a copy instead, as does an open process
                         # a copy anyway (session ID changed) → warn, record the copy's job and session
                         # if resume fails, or its job ends `failed` → fresh worker with a "pick up this task" prompt
                         # a new round's prompt says whether to reply on the PR (REPLY_TO_THREADS)
                         # records last_round_sent; status → addressing-feedback
worker: address each comment → commit (no force-push) → push → crew reply TASK <id> "…" (if told to) → status pr-open
                         # crew reply: an inline comment → reply in its thread (to the thread root);
                         #   a conversation comment or review summary → a PR comment quoting it;
                         #   refuses an ID that isn't a comment or review on the task's own PR
```

### Stacked tasks
A task is *stacked* when its `base` is another task's `branch`; that task is its *parent*. Nothing else records the relationship (`parentOf` looks the base up among the project's task branches), so it can't disagree with `base`, and `crew set TASK base <a task's branch>` stacks an existing task. `depends_on` stays as it was written.

```
crew new "<title>" --on PARENT   # base = parent's branch, depends_on = parent; refuses a merged or abandoned parent
crew dispatch TASK               # needs origin/<parent branch>, not a merged parent; records base_sha = the parent's tip
worker                           # gh pr create --base <parent branch>; PR body starts "Stacked on #N."
crew sync                        # after the PR pass: git fetch origin (only for projects with a live stacked task),
                                 #   move base_sha forward, print "<task>: needs a restack, <parent> moved|merged"
crew status                      # children listed under their parent, with "needs restack: …"
crew restack TASK                # refuses while a live parent needs a restack itself (bottom-up),
                                 #   and a restack onto the commit it already sent (restack_sent) unless --again
  ├─ git fetch origin; target = origin/<effective base>:
  │     parent unmerged → the parent's branch; parent merged → the parent's base, following merged parents
  │     upwards (the default branch when a stack merges bottom-up)
  └─ crew followup TASK --note "…git rebase --onto <target sha> <base_sha>…"
worker: rebase → checks → git push --force-with-lease
        → if the base changed: gh pr edit --base <new>; crew set TASK base <new>   → status pr-open
```

- **`base_sha`** is the newest commit of the parent's branch that the task's own branch contains, the cut point for `git rebase --onto`. `crew dispatch` records it and `noteBaseSHA` (from `crew sync` and `crew restack`) moves it to the two branches' merge base. Once the parent is squash-merged or rebased, that merge base falls back to a commit before the parent's work, so the recorded value is kept for as long as the task's branch still contains it. Only `crew` writes it.
- **Whether a restack is needed is computed, never stored** (`restackReason`): the parent is `merged` or `abandoned`, or `origin/<parent branch>` isn't an ancestor of the task's local branch. It reads the refs as last fetched, so `crew status` is as fresh as the last `crew sync`.
- **The worker changes `base` and the PR's base, last.** `crew restack` returns as soon as the worker is resumed. If it rewrote `base` itself, a rebase that then failed would leave a task that no longer looks stacked, with the parent's commits still in it.
- **`MAX_IN_FLIGHT`** (`inFlight`) skips a task whose parent is in flight, so a stack counts once, and `dispatchChecks` doesn't apply the cap to such a task. `MAX_WORKERS` still counts every session.
- **`restack_sent`** is the commit the last `crew restack` asked the worker to rebase onto. `crew restack` refuses to send the same one again unless `--again`, so a worker that couldn't restack isn't sent the same note in a loop. Only `crew` writes it.
- **`crew sync` never restacks.** `crew tick` does, through `crew restack`. Nothing restacks a whole stack at once: each restack is a background worker, and the next task up only needs one after the worker below it has pushed.
- **`crew observe TASK`** compares against `base_sha` when `origin/<base>` is gone.

### Sync and cleanup
- `crew sync` finds missing PR URLs by branch (`gh pr list --head`), records `pr_state` and `pr_updated`, and sets `status: merged` when GitHub says merged. Then it checks the stacks, as above.
- `crew cleanup TASK|--merged` stops the worker and removes it from agent view (`removeSession`), then ends any background process still running in the worktree (`stopProcessesIn`: TERM, then KILL after 10 seconds), then removes the worktree (`git worktree remove`, or `wt remove --no-delete-branch --foreground` so the repo's remove hooks run) and deletes the local branch (merged tasks only). The worker goes first so that it doesn't carry on in a deleted directory. Its leftover processes go next: a dev server it started in a background shell outlives `claude stop`, and would otherwise keep running, holding its port, after the worktree is gone. They're found by working directory in `/proc`, so this needs Linux; on macOS, cleanup says so and leaves them. Only processes without a terminal are ended: one with a terminal is the user's, such as their shell sitting in the worktree. crew's own process and those that started it are left alone too. A worker whose process won't close keeps its worktree until a later cleanup; one that `claude rm` won't delete only gets a warning. Cleaning up a merged parent is safe while tasks are still stacked on it: their branches contain its commits, a restack cuts at `base_sha`, and nothing reads the parent's local branch.

### `crew tick`
One pass over every project, run by `crew-tick.timer` every 3 minutes, by the lead (at session start and for status), or by hand. A second tick while one runs exits at once (it doesn't wait for `.crew-tick.lock`). Each step is a `crew` command run as a process of its own, so one that refuses doesn't stop the rest. It prints one line per thing it did, and one per review round or goal input it holds back for a busy session; nothing otherwise.

```
crew tick
  ├─ git fetch origin (soft). origin's host names another machine, and this one held the lease
  │    → stand down: crew-* sessions stopped, unpushed ledger work kept on
  │      lost/<host>-<time>, home reset to origin, units disabled; nothing else happens
  ├─ require_host: this machine holds the lease, or the tick stops
  ├─ crew sync --all
  ├─ for each task:
  │    ready, its goal running                   → crew dispatch (exit 4, a dispatch that has to wait: nothing said)
  │    merged, worktree still recorded           → crew cleanup
  │    in flight, PR changed since last read     → crew feedback (the user's comments → a round;
  │                                                  collaborators' → unchecked Open questions)
  │    working or addressing-feedback, its job   → crew followup --fresh --note "This task moved to <host>…"
  │      unknown here and no worktree here          (moved_here: a task that moved here; next tick its new job is listed)
  │    worker busy (mid-turn, or a prompt open)  → stop here, saying so if a round waits; the next tick tries again
  │    review_rounds > last_round_sent           → crew followup
  │    pr-open, needs a restack, parent doesn't, → crew restack
  │      and restack_sent isn't the target yet
  │    not needs-input, unchecked Open questions → crew set TASK status needs-input
  └─ for each running goal:
       a note or a worker question it hasn't had → crew goal followup, once the goal lead is free (saying so if it isn't)
       every task merged or abandoned            → crew goal done
  └─ push_ledger in the foreground (soft): whatever a background push missed
```

The stand-down and the lease check are gates, not steps: they decide whether the tick does anything, so they run in the tick's own process rather than as `crew` commands of their own.

- **One rule for "waiting on you":** a task whose worker is idle and that has an unchecked item under Open questions is `needs-input`. `crew tick` only sets it. Whoever settles the last question sets the status back: the worker when it's resumed with the answer, the lead otherwise. Going back automatically would hide the questions of workers still running on a prompt from before questions were checkboxes.
- **Rounds wait for the end of the worker's turn.** Delivering one mid-turn would need `SendMessage`, which only a Claude session can call.
- **Only changed PRs are read.** `crew sync` records each PR's `updatedAt` as `pr_updated`, and `crew tick` runs `crew feedback` only when that is newer than `feedback_since`. A quiet PR then costs one API call per tick instead of four.
- **Nothing is sent twice:** `last_round_sent` for rounds, `restack_sent` for restacks.
- **Restacks need no word from the user any more.** `crew sync` still never restacks; `crew tick` does, bottom-up, only for a task whose PR is open and whose worker is idle. A restack force-pushes the task's branch.

### Goals
The user hands the crew a goal, a list of items or a column on the project's tracker, and a **goal lead** works through it without them. The interactive lead stays the user's interface to everything, goal leads included.

```
you ⇄ lead: what the goal is for, where its items come from, what bounds it
  lead: crew goal new "<title>" → the user's words into the goal file's Goal → your go-ahead
        crew goal start GOAL
          ├─ detached checkout ~/agent-work/<p>/_goal-<id> at origin/<default> (plain git, like the gardener's)
          └─ claude --name crew-<p>-goal-<id> --add-dir <home> --bg --agent goal-lead
               --permission-mode <LEAD_PERMISSION_MODE> --settings "<lead settings without agent>" "<prompt>"
  goal lead, for every item, in one turn: crew new … --goal GOAL → intake to the brief standard
        → plan-reviewer → findings settled → crew ready TASK, or parked (draft, with Open questions)
  crew tick: dispatches the goal's ready tasks as MAX_WORKERS / MAX_IN_FLIGHT allow;
             resumes the goal lead (crew goal followup) with the user's notes and its workers' questions;
             closes the goal (crew goal done) once every task is merged or abandoned
```

- **A goal is a ledger file**, `projects/<p>/goals/NNN-<slug>.md`, with flat front matter (`status`: `draft`, `running`, `done` or `abandoned`; the goal lead's `checkout`, `lead_name`, `lead_job` and `lead_session`; `unsent_notes`) and the sections Goal (the user's words, quoted, including where the items come from), Items (each one with its task, written by the goal lead), Decisions and Log. Like a task file, it is the goal lead's memory, and the session can be replaced.
- **A task belongs to a goal through its `goal:` key**, set by `crew new --goal`. `crew status` lists each goal with its tasks under it.
- **Parked is a task state, not a goal one.** The goal lead parks an item by leaving its task in `draft` with unchecked Open questions, so the rest of the goal carries on. The user answers through the lead, which records the answer in the task and passes it on with `crew goal note`.
- **The goal lead is told only what's new.** `crew goal note` records the user's words under the goal's Decisions and counts them in `unsent_notes`. A goal task's worker that asks something is found from its Open questions (not the `@login` lines `crew feedback` writes for collaborators); `questions_routed`, a checksum of the questions last sent, keeps one the goal lead left for the user from being sent again. `crew goal followup` sends both, once the goal lead is free, and resets them.
- **The goal lead works one turn per input** and never polls (its prompt, and `disallowedTools` without `ScheduleWakeup` or cron). Every turn starts in a freshly fetched checkout. It doesn't count toward `MAX_WORKERS`, which caps workers: what it produces is capped where it reaches the user, by `MAX_IN_FLIGHT`.
- **Dispatches wait quietly.** `dispatchChecks` refuses with exit 4 (`notYet`) when a task only has to wait: a cap, an unmerged dependency, a stacked parent that hasn't pushed. `crew tick` prints nothing for those and tries again; any other refusal is a warning.
- **Ending a goal** (`crew goal done` or `abandon`) stops and removes the goal lead, any background process still running in its checkout, and the checkout, and leaves the tasks alone. A note to a done goal reopens it, for items added later. The ready tasks of an abandoned goal are the lead's to dispatch.
- **Items come from wherever the goal says.** If that's an external tracker, the goal lead reads it with the tools its session has and changes nothing there. How to reach a project's tracker belongs in that project's `AGENTS.md`, never in this repo.

### Gardener
`crew gardener [--all|PROJECT]`:
1. Fetches the default branch and moves `_gardener` to `origin/<default>`. This checkout always uses plain `git worktree`, whatever `WORKTREE_TOOL` says: the gardener only reads, so it doesn't need the project's per-worktree setup.
2. Sets the review range to `gardener.last..HEAD`. On the first run it starts from the commit before `GARDENER_LOOKBACK`.
3. Runs `claude -p --add-dir <home> --agent gardener --permission-mode acceptEdits --permission-prompts none --max-turns … --settings readonly-settings.json "<prompt>"`, writing its output to `logs/`.
4. On success, updates `gardener.last` and commits. It exits non-zero if any project fails.

Proposals become tasks through `crew promote`, which only accepts `open` or `deferred` proposals.

### Public and private repos
```
public crew repo (code, no state)
  └─ git clone → ~/crew; crew setup
       ├─ origin → upstream, its push URL DISABLED: a ledger can never reach the public repo
       ├─ gh repo create <login>/crew --private --source . --remote origin
       ├─ config/crew.env from templates/crew.env → commit → push
       └─ crew host up (below)
another machine of the person's: git clone <their private repo> ~/crew; crew host up [--take]
```

### Hosting and failover

**Claiming the lease** (`crew host up [--take]`):
1. `crew install`.
2. Pull, write this machine's name in `host`, commit, push. A rejected push means another machine moved first: drop the claim and look again. Without `--take`, a lease another machine holds is refused.
3. Clone each project this machine has no clone of into `$WORK_ROOT/<p>/repo`, and record it in `local.env`.
4. Enable `crew-tick.timer`, `crew-gardener.timer` and `crew-lead.service`, and run `crew doctor`.

**Holding it:** every change takes `lock`, which runs `requireHost`: `host` must name this machine, and `.crew-lease-lost` must not exist. Every ledger commit starts a background push (`pushSoon`).

**Losing it** (another machine ran `crew host up --take`):
1. This machine's next background push is rejected, finds origin's `host` names the other machine, and creates `.crew-lease-lost`. From then on this machine refuses every change.
2. Its next `crew tick` fetches, sees the same, and stands down: its `crew-*` sessions stopped, what origin lacks kept on a branch `lost/<host>-<time>`, the home reset to origin, and its units off. That removes `.crew-lease-lost`, as a later `crew host up` would. The units go off last, so a stand-down that fails on the way leaves the tick on, and the next tick tries again.

**Carrying on, on the new machine:** its `crew tick` gives a working task whose worker and worktree aren't there a fresh worker (`movedHere`, `crew followup --fresh`). `crew followup` and `crew restack` recreate a missing worktree from `origin/<branch>`, or start over from `origin/<base>` when the branch was never pushed.

**Handing over on purpose** (`crew host down [--force]`): refused while a task worktree has unpushed commits; then the units off, sessions stopped, the ledger pushed, and an empty `host` pushed. `crew host up` on the next machine needs no `--take`.


- **Losing a machine loses little:** the ledger is pushed seconds after each change, and workers push their branch after every commit. Session transcripts stay on the old machine, which is why a moved task gets a fresh worker: principle 3 already lets any worker be replaced.
- **The phone follows the lease:** `crew-lead.service` runs only where `crew serve` finds the lease (it exits 3 elsewhere), so Claude Mobile's `crew` is always the machine that runs the crew.

### Upgrading
```
crew upgrade   lock (the host only) → refuse a dirty home
               → the newest v<version> tag upstream has (git ls-remote --tags upstream)
               → CREW_VERSION=<version> bin/crew version: the launcher downloads that release's binary;
                 if it can't (the release may still be building), refuse, with nothing changed
               → git fetch upstream tag v<version> → git merge --no-edit v<version>
                 a conflict (only the person's own edits to crew's files can conflict) → stop, merge left in progress
               → the new version's bin/crew install → push → systemctl --user restart crew-lead.service
               → remove the other versions' binaries from bin/release/
```

A home moves only from release to release, so its agent prompts and its binary always come from the same one. Releases are made in the public repo: pushing the tag `v<version>` (it must match `VERSION`) runs `.github/workflows/release.yml`, which runs the tests, builds `crew-Linux-x86_64` and `crew-Linux-aarch64` with the version stamped in, and publishes them as the tag's release, the tag's message as its notes. The workflow runs only in the public repo, so a private home that pushes a tag builds nothing.

## `crew` internals (`cmd/crew/`)

Go, standard library only, in one package. Each area has a file of its own; each command is a `cmd…` function in a `cmd_*.go` file.

- **The launcher (`bin/crew`):** a POSIX `sh` script, the only `crew` on `PATH`. It finds itself through symlinks, reads `VERSION` from its own checkout (or takes `CREW_VERSION` from the environment), and runs `bin/release/crew-<version>` with `CREW_HOME` set to that checkout, unless it's set already.
  - A missing binary is downloaded once with `curl` from the public repo's release `v<version>` (`crew-<uname -s>-<uname -m>`), to a temporary file that is run with `version` to check it, then renamed into place. `bin/release/` is gitignored, so a fresh clone works on a machine without Go.
  - When it can't get a binary, it says why and exits 1, or 2 when called as `crew guard`, so a missing binary blocks edits.
  - The release URL is written into it; a fork changes that one line.
- **Home and config (`home.go`, `env.go`):**
  - `CREW_HOME`, exported, so the sessions, hooks and `wt` runs crew starts see it.
  - The settings are `config/defaults.env`, which sets every key, then `config/crew.env` if it exists. `loadProject` adds `projects/<p>/project.env` (committed: `REMOTE`, `DEFAULT_BRANCH`, overrides), then `projects/<p>/local.env` (this machine's `REPO`, gitignored), starting from the shared settings for each project, so one project's values never reach the next. It fails when this machine has no clone.
  - The settings files are shell-style but parsed, not sourced. A line is blank, a `#` comment, or `KEY=value`, where the value is one shell word: plain, `'single-quoted'` and `"double-quoted"` parts, with `$VAR` and `${VAR}` taken from the settings read so far or the environment, then an optional `# comment`. Anything else, a command substitution or an unset variable say, is an error naming the file and line.
  - `crew config [KEY] [--project P]` prints the effective value, for the cwd's project or P.
- **Errors (`main.go`):** a command returns its refusal as an error: `fail` (exit 1), `notYet` (exit 4) or `exitQuietly(code)` for one that has said what it had to (`crew feedback`'s 3 for "nothing new"). `main` prints the message as `crew: …`. `warn` reports what doesn't stop the command.
- **Locks (`lock.go`):** `flock(2)` on files in the home, the same lock util-linux `flock` takes. `lock` takes `.crew.lock` (30 s timeout) for the rest of the process, or until `unlock`, and then checks the lease (`requireHost`). `takeLock` is the lock alone, for the commands that move the lease. `.crew-tick.lock` lets one `crew tick` run at a time, and `.crew-push.lock` one ledger push. Go opens files close-on-exec, so the sessions and processes crew starts never hold a lock. `crew dispatch` unlocks while it creates the worktree, since Worktrunk hooks can take minutes and workers' `crew log` calls would otherwise time out; it repeats its checks once it has the lock again.
- **Running claude (`sessions.go`):** `claude` runs it with `$CREW_HOME/bin` first on `PATH` and `CLAUDECODE` unset, so it can be launched from inside a Claude session.
- **Ledger commits (`push.go`):** `commit` adds and commits the state paths (`statePaths`: `projects/`, `reports/`) and nothing else in the home, but only if `$CREW_HOME` is its own repo toplevel. It never fails the command. After a commit it calls `pushSoon`.
- **Agents' own edits:** `crew commit --hook` is a PostToolUse hook on Edit, Write and NotebookEdit, in `lead/.claude/settings.json` and `config/worker-settings.json`. It reads the hook JSON on stdin; a file under a state path of the home is committed (`edit <path>`, which pushes it), anything else is left alone. It always exits 0, so it never fails the tool call.
- **Public and private repos:** `cmdSetup` and `cmdUpgrade` (see [Public and private repos](#public-and-private-repos) and [Upgrading](#upgrading)). `crew setup` refuses when `config/crew.env` exists, when there's no `origin`, or when `gh` isn't signed in. `crew upgrade` never resolves a conflict itself.
- **Lease (`lease.go`, `cmd_host.go`):** one machine runs the crew at a time, the one the home's `host` file names (`CREW_HOST`, from the environment, default `machineName()`: the short hostname on Linux, `scutil --get LocalHostName` on a Mac, since a Mac's hostname can change with the network).
  - `requireHost` fails unless `host` names this machine and `.crew-lease-lost` doesn't exist, with a message naming the holder. `lock` runs it after taking the flock, so every mutation is refused elsewhere, and `crew dispatch`, `crew gardener` and `crew tick` run it before they start a session. Reads work anywhere, and `crew status` on another machine pulls first and says it's read-only.
  - `crew host up` claims the lease by a push (`commitHost`), so a rejected push means another machine moved first: it resets and looks again. `--take` claims it from another machine. It also runs `crew install`, clones each project this machine has no `local.env` for into `$WORK_ROOT/<p>/repo`, enables `crewServices` and runs `crew doctor`.
  - `crew host down` refuses while a task worktree has commits its remote branch lacks (or no remote branch), unless `--force`. It disables the units, stops every `crew-*` session (`stopCrewJobs`), pushes, and pushes an empty `host`.
  - A background push that origin rejects, and that finds origin's `host` names another machine, creates `.crew-lease-lost` instead of rebasing. The next `crew tick` stands down (`standDown`).
- **Push (`push.go`):** the ledger goes to the home's `origin`, the person's private repo, soon after each commit.
  - `pushSoon` starts `crew push --soon` in a session of its own when `origin` exists, so no command waits on the network. Its output goes to `logs/push.log`. Pushes take turns on `.crew-push.lock`: a push that finds one running leaves the work to it, and the running one checks again after it lets go of the lock, so a commit made while it held the lock still goes out.
  - `pushLedger` pushes until `@` equals `@{u}`, up to five tries. A push rejected because origin has commits this machine lacks fetches them and rebases the ledger onto them (`git pull --rebase --autostash`) under the ledger lock (`adoptOriginCommits`); a rebase that conflicts is aborted and left for the next try.
  - `crew push`, and `crew tick` at its end, push in the foreground (`pushNow`, which waits for the push lock), a catch-all for background pushes that failed.
- **Ledger files (`ledger.go`):**
  - A `Doc` is a task, goal or proposal file. It reads the file afresh for every value and writes it back at once, under the ledger lock: agents edit these files too, so nothing is kept in memory. `Task`, `Goal` and `Proposal` wrap one with the keys each has. `Status` and `GoalStatus` are the statuses; a Go test checks them against `LEDGER.md`.
  - `Get`/`Set` handle *flat* `key: value` between the first two `---` lines. `Set` quotes values containing `: `, ` #` or `"`, replaces newlines with spaces, and adds missing keys before the closing `---`. Keep front matter flat; there's no YAML parser.
  - `Section` returns a section's text without HTML comments or blank lines; `crew ready` uses it. `InsertBefore` puts review rounds before `## Log`.
  - `Log` appends to the end of the file, so **`## Log` must stay the last section**.
- **Resolution (`resolve.go`):** `resolve` accepts:
  - a path;
  - `#<pr>` (matches the `pr` field);
  - an exact ID;
  - a number (`7` → `007-*`);
  - a unique substring.

  It searches the cwd's project first, then all projects. On ambiguity it prints the candidates and fails.
- **Sessions (`sessions.go`):**
  - `agents` returns what `claude agents --json --all` lists as `Agent`s, or nothing on failure. It reads each field on its own, so a field whose type changes loses only that field. It sets `TurnEnded` on a live `working` session whose transcript (`~/.claude/projects/*/<session>.jsonl`) ends with the `turn_duration` entry Claude Code writes when a turn ends (`turnEnded`).
  - `busy` counts a job as busy while a turn runs (`state` working) or while its live process holds an open prompt, such as a permission request (`status` waiting). A `blocked` job without an open prompt ended its turn on a question for the user, so it takes a followup: the answer is what the followup sends. A background shell the worker started, such as a dev server, keeps its `state` working and its `status` busy after its turn ends, so a `working` job counts as mid-turn only until it has `TurnEnded`. `jobState` shows such a job as "idle, background task running". `runningWorkers` and `stopIdleSession` use the same rule.
  - `runningWorkers` counts the busy sessions whose job a task file records as `worker_job`, across all projects. It doesn't go by name: a session Claude Code copies on resume gets a name of its own.
  - `stopJob` runs `claude stop` and waits up to 10 seconds for the job's `pid` to clear. `stopIdleSession` uses it on a recorded session that isn't busy but whose process is still open, before a resume.
  - `removeSession` stops a listed job whose process is open, busy or not, then runs `claude rm` on it. It fails only if the process won't close.
  - `launchSession` starts or resumes the background session a ledger file records (`<role>_name`, `<role>_session`) and confirms it started (`waitForLaunch`). It strips color codes from `claude`'s output before reading the job ID, and fails rather than record an ID that doesn't look like one. A resume passes only the session and the prompt. `relaunch` falls back to a fresh session when a resume didn't start one (`errNotStarted`).
  - `launchWorker` is the only place a worker command line is built, on top of `launchSession`. A fresh worker gets `--agent`, `--permission-mode`, `--add-dir`, `--autocompact` (`WORKER_AUTOCOMPACT_WINDOW`, 400k by default, so a worker carried through several review rounds compacts long before its 1M window fills) and `workerSettings`: `config/worker-settings.json`, or, when `projects/<p>/worker-settings.json` exists, the two merged into a JSON string. The merge is add-only (`addOnly` in `json.go`): objects merge, arrays append new entries, and any value the base sets wins, so a project can add `allow` rules or `env` but can't drop a `deny` rule.
- **JSON (`json.go`):** the settings crew writes keep their keys in the order their files have them, as jq's output did: the user's `~/.claude/settings.json`, merged worker settings, and a goal lead's settings.
- **Stacks (`stacks.go`):** `Branches` indexes a project's task files by branch, and `parentOf`, `effectiveBase`, `restackReason` and `noteBaseSHA` build on it (see [Stacked tasks](#stacked-tasks)). They read git only through `REPO`, whose refs the task worktrees share, and never touch its working tree.
- **Goals (`cmd_goal.go`):** `goalTasks` finds a goal's tasks by their `goal` key; `goalFinished`, `workerQuestions` and `unroutedQuestions` build on it. `questions_routed` is a POSIX `cksum` of the questions (`cksum`), so ledgers the bash crew wrote keep working. `launchGoalLead` is the only place a goal lead command line is built, on top of `launchSession`, and `relaunch` gives it the same resume-or-fresh fallback as `crew followup`. `retireGoal` ends a goal for `crew goal done` and `abandon`.
- **Worktrees (`worktree.go`):** `worktreeCreate` and `worktreeRemove` are the only places task worktrees are made or removed, for both `WORKTREE_TOOL` values. `setupWorktree` wraps `worktreeCreate` for a task: it creates the worktree unless it exists (on the task's local branch, or a new one from `origin/<base>`), refuses one that a failed Worktrunk dispatch left behind, and copies the `WORKTREE_COPY` files. Its callers run it without the ledger lock, since Worktrunk hooks can take minutes. A missing branch comes from `origin/<branch>` when another machine pushed it. `ensureWorktree` is how `crew followup` and `crew restack` find a task's worktree on this machine: it makes it at `$WORK_ROOT/<p>/<id>` if it's missing (on a machine the task moved to, say), rewrites `worktree:` when the recorded path differs, and logs "recreated worktree from origin/<branch>", or "started over: … was never pushed from <machine>" when the branch is nowhere and starts again from its base. `crew observe` and `crew cleanup` treat a missing worktree as nothing to do. With `worktrunk`, crew never passes `--yes`: hooks you haven't approved make `wt` fail non-interactively, and dispatch stops before a worker starts. It checks that the JSON `path` `wt` returns is the expected one. If a hook fails after `wt` has made the worktree, the task never records it, and the next dispatch refuses that leftover directory instead of starting a worker in a half-set-up worktree.
- **Services (`services_linux.go`):** the only place crew runs `systemctl`. `crewServices` names the units; `enableServices` and `disableServices` turn them on and off now (`crew host up`, `crew host down`, the stand-down), `serviceEnabled` and `servicePath` are what `crew install` and `crew doctor` read, `restartLeadService` is `crew upgrade`'s restart, and `installServices` is `crew install`'s systemd step. `services_darwin.go` has the same functions, and says macOS isn't supported yet. `machineName` (`machine_linux.go`, `machine_darwin.go`) is this machine's name for the lease.
- **Processes (`proc_linux.go`):** `stopProcessesIn` finds the processes left in a worktree or checkout by working directory in `/proc`. `proc_darwin.go` says that isn't supported on macOS yet, and `crew cleanup` and `crew goal done` warn and carry on.
- **Commands:** one `cmd…` function each, listed in `commands` in `main.go`. `version` and `guard` run before the home is read: the launcher checks a download with `version`, and the guard must work whatever the home's settings say.

## The write guard (`crew guard`)

It's called from the agents' frontmatter hooks, and from the hooks in `lead/.claude/settings.json` and `config/worker-settings.json`, as `"${CREW_HOME:-$HOME/crew}/bin/crew-guard" <role>` on Edit, Write and NotebookEdit. `bin/crew-guard` is a two-line `sh` file that runs `crew guard <role>`: it stays, so sessions started before an upgrade keep their hook (a hook command that's gone would count as a non-blocking error, and allow their edits). The guard reads the hook JSON on stdin (`.cwd`, `.tool_input.file_path`). Exit 0 allows the call; exit 2 blocks it, and stderr goes back to the agent. It blocks whenever it can't work out where a path leads: input that isn't JSON, a symlink loop. Paths are judged by where they lead, symlinks resolved (`canonical`, which works like `realpath -m`).

- In the crew home, only the state paths, `projects/` and `reports/`, take edits, for every role. The rest of the home (`bin/`, `agents/`, `config/`, `templates/`, `lead/`, `.claude/`, `.git/`, the root files) is crew itself, and is blocked.
- Always allowed elsewhere: `/tmp`/`$TMPDIR`, `~/.claude/jobs`, `~/.claude/plans`, `$CLAUDE_JOB_DIR`.
- `worker`: only paths inside the git toplevel of `$CLAUDE_PROJECT_DIR` (falling back to `cwd`), and never when that toplevel is the main checkout.
- `lead` (the lead and goal leads), `gardener`: nothing else.

The target path is structured input, so this needs no shell parsing. Bash commands aren't checked: shell parsing can never be complete.

Why the hook is in two places:

- The lead and the implementer have no `tools:` allowlist, only `disallowedTools`. A `tools:` list can name MCP servers only one by one, so a shared allowlist would hide every repo's servers or leak one repo's into all of them. Without it, each session gets the MCP servers configured for its repo.
- They can also spawn any subagent, apart from the crew's own agents (`Agent(lead)`, `Agent(implementer)`, `Agent(gardener)` and `Agent(goal-lead)` are denied in their settings).
- A frontmatter hook runs only while its own agent runs, so it doesn't guard a `general-purpose` subagent's edits. The settings hook does: settings hooks fire for subagent tool calls too.
- The frontmatter copy stays for a session started without its settings file, or with an edited one that dropped the hook (`crew doctor` flags that).

## Bash: no sandbox

No crew session runs in Claude Code's sandbox. It was tried and dropped:

- On Linux it gives sandboxed commands their own network namespace, so they can't reach `localhost:<port>`. Test suites that need a local database or cache (a Rails app's `bin/rspec`, for example) failed.
- Exempting those commands with `excludedCommands` depended on how the agent phrased them (a pipe or `cd … &&` missed the rule), and an exempt test runner executes code the worker wrote anyway, so little protection was left.
- Wrapping the whole worker in our own bubblewrap doesn't work either: `claude --bg` hands the session to the `claude daemon` process, so it isn't a child of the command `crew` runs.

What governs Bash instead:

- **Workers** run in `WORKER_PERMISSION_MODE` (`auto` by default): Claude Code's classifier approves routine commands and stops risky ones, as in your own sessions. The `allow` rules in `worker-settings.json` skip the classifier for the usual git and gh commands; the `deny` rules catch `gh pr merge` and force pushes. The implementer prompt tells workers to keep shell writes to their worktree, the ledger, temp directories and tool caches.
- **The gardener and `crew observe`** run with `--permission-prompts none` and the read-only `allow` list in `readonly-settings.json`, so any other command is denied.
- **The lead** runs in `LEAD_PERMISSION_MODE` (`auto` by default) in the home's `lead/`, and **goal leads** in the same mode in their own checkouts. `crew guard` keeps their file edits to the ledger.
- **Network is left open.** `WebFetch(domain:*)` pre-allows every host, so background sessions never stop at a network prompt.

## `crew install`

Order: check dependencies and the Claude Code version (≥ 2.1.281) → agents → `env.CREW_HOME`, `WORK_ROOT` and the lead's `settings.local.json` → fish → systemd → manifest → the optional AGENTS.md setting → next steps. It only puts files where Claude Code, fish and systemd look for them, and writes the lead's gitignored `lead/.claude/settings.local.json` for this machine; it changes nothing tracked in the home.

- **The home** is `CREW_HOME`, else the checkout of the `bin/crew` that ran (symlinks resolved), so `~/crew/bin/crew install` works straight after a clone.
- **What gets installed** is discovered, not listed: every `agents/*.md`, every file under `fish/`, and every `systemd/*`. Adding a file here is enough to ship it.
- **Files are replaced by rename** (`installContent`: a temp file in the same directory, then a rename), never rewritten in place, so a running guard hook never reads a half-written file. A file that differs is backed up first.
- **A home other than `~/crew`** is substituted into the fish `conf.d` snippet and the systemd services (`renderLines`) *before* comparing, so an unchanged re-run takes no backups.
- **`~/.claude/settings.json`** is rewritten, after a backup, only when a value crew sets there changes. It keeps its keys in their order (`json.go`).
- **Stale files** (`prune`): `~/.local/share/agent-crew/manifest` records `component<TAB>path` for every shipped file. After installing a component (`agents`, `fish`, `systemd`), files the previous install of that component shipped and this one doesn't are backed up and removed (systemd units are disabled first). Components skipped with `--no-fish`/`--no-services` keep their manifest entries and lose nothing, and only paths under the component's current root are touched.
- **The AGENTS.md question** is asked only if the setting is off and the user hasn't declined before (`~/.local/share/agent-crew/agents-md-declined`).
- **Re-running** is idempotent: a second run with no changes in the home backs nothing up. Backups go to `~/.local/share/agent-crew/backups/<timestamp>/`.
- **Units** are installed but not enabled: `crew host up` enables them. Install names the ones that are off on this machine.

Flags: `--yes`, `--no-fish`, `--no-services` (the systemd units), `--skip-agents-md-setting`.

## Contracts between components

Changing any of these means updating every side listed.

| Contract | Producer → consumer |
|---|---|
| `worker_job` in task files is the job ID `claude agents` lists | `launchWorker` → `runningWorkers` (the concurrency cap), `Agents.jobState`, `Agents.jobBusy` |
| `claude --bg` prints `backgrounded · <job> · …`, the job possibly in color, and a `note:` line when it starts a copy | Claude Code → `launchSession` (color codes stripped, the job is the line's third field) |
| `claude agents --json --all` entries have `id`, `state`, `pid`, `status`, `waitingFor`, `sessionId`, `name` | Claude Code (research preview) → `agents`, `Agents.jobState`, `Agents.jobBusy`, `runningWorkers`, `stopIdleSession`, `waitForLaunch` |
| A session's transcript is `~/.claude/projects/<dir>/<session>.jsonl` (under `CLAUDE_CONFIG_DIR` when set), one JSON entry per line; a turn ends with a `system` entry whose `subtype` is `turn_duration`, and the next one starts with a `user` entry | Claude Code (not documented) → `turnEnded` |
| A job's ID is the first 8 characters of its session ID. A job with a `pid` reports the session its process has loaded. A resume whose session can't be found ends in `failed` | Claude Code → `waitForLaunch` |
| `claude --resume <session> --bg` continues the session in place only when given no other launch flags and the session's process has exited | Claude Code → `launchSession` (resume), `stopIdleSession` |
| `claude stop <job>` ends the job's process (its `pid` clears); background shells it started keep running for its next process. `claude rm <job>` takes it out of `claude agents` and leaves a worktree the session was started in | Claude Code → `stopJob`, `removeSession` (`crew cleanup`) |
| `--add-dir` is variadic, so it's never placed right before the prompt | `launchSession`, `crew gardener` |
| `REPO` is per machine: `projects/<p>/local.env`, gitignored. `project.env` holds what every machine shares (`REMOTE`, `DEFAULT_BRANCH`, overrides) | `crew project add` → `loadProject`, `crew doctor`, `.gitignore` |
| `host` at the home's root holds one line, the `CREW_HOST` of the machine that runs the crew, or nothing; it counts once origin has it | `crew host up`/`down` (`commitHost`) → `requireHost`, `originHolder` (`pushLedger`, the tick's stand-down), `crew status` |
| `crew host up` enables and `down`, `prune` and the stand-down disable `crewServices` | `crew host up`, `crew host down`, `standDown` ↔ `systemd/` |
| `crew serve` exits 3 on a machine that doesn't hold the lease; the unit doesn't restart on 3 | `crew serve` → `systemd/crew-lead.service` (`RestartPreventExitStatus=3`); checked in `tests/guard.sh` |
| Front-matter key names | `templates/task.md`, `templates/goal.md`, `crew`, the agent prompts (`crew set TASK status pr-open`, …), `LEDGER.md` |
| A task's `goal` is its goal file's ID, in the same project | `crew new --goal` → `goalTasks`, `goalRunning` (`crew tick` dispatch), `crew status` |
| Goal status values `draft running done abandoned` | `crew goal`, `tickGoal`, `crew status`, `lead.md`, `LEDGER.md` |
| A dispatch that only has to wait exits 4 (`notYet`) | `dispatchChecks`, `crew dispatch` → `tickTask` |
| The goal lead follows the sections "Intake: turning a request into a brief", "The brief standard" and "Stacked tasks" of the installed `lead.md` | `lead.md` → `goal-lead.md`; checked in `tests/guard.sh` |
| A goal task's worker question that the goal lead leaves for the user gets a "For the user: …" Log line | `goal-lead.md` → `lead.md` "Goals"; checked in `tests/guard.sh` |
| Collaborators' items under Open questions start with `- [ ] @<login>` | `crew feedback` → `workerQuestions` |
| A task is stacked when its `base` equals another task's `branch` in the same project | `crew new --on`, `crew set TASK base` → `parentOf` (dispatch gate, `inFlight`, `crew status`, `crew sync`, `crew restack`) |
| The restack note: `git rebase --onto <target> <base_sha>`, and, when the base changes, `gh pr edit --base <new>` and `crew set TASK base <new>` | `crew restack` → implementer "Workflow: restack"; the worker's `crew set TASK base` is what ends the "needs restack" state after a merge |
| "This task is stacked on task …" in the dispatch prompt | `crew dispatch` → implementer "Stacked tasks" |
| A review round's followup prompt says whether to reply ("Reply to each comment in the round with `crew reply`", from `REPLY_TO_THREADS`) | `crew followup` → implementer "Workflow: review rounds" step 5; checked in `tests/guard.sh` |
| Status values `draft ready working needs-input pr-open addressing-feedback merged abandoned` | `crew` (`Status.InFlight`, `crew ready`, `crew sync`, `crew status`), `implementer.md`, `lead.md`, `LEDGER.md` |
| Section headings (`## Goal`, `## Structure`, `## Plan`, `## Acceptance criteria`, `## Review rounds`, `## Log` last) | `templates/task.md` → `crew ready`, `crew feedback`, `Doc.Log`, `crew promote` |
| "Findings the brief settled": structural points from the pre-PR review that the worker left alone because the brief decides them, listed in its Log entry and final report | implementer pre-PR review → lead's first report of the PR; checked in `tests/guard.sh` |
| A PR's `updatedAt` moves whenever a comment is posted or a review submitted | GitHub → `crew sync` (`pr_updated`) → `prMayHaveNewComments` in `crew tick` |
| `<!-- crew -->` marker on worker replies | `crew reply` → the `crew feedback` filter |
| An unchecked item (`- [ ]`) under Open questions is a question waiting on the user; whoever records its answer under Decisions ticks it | `crew feedback` (collaborators' comments), implementer "When you're blocked" → `OpenQuestions` (`crew tick` marks the task `needs-input`), lead (relays and ticks); checked in `tests/guard.sh` |
| Who a comment is from: the user (`gh api user`), a collaborator (`author_association` OWNER, MEMBER or COLLABORATOR, `user.type` not `Bot`), or anyone else | GitHub REST API → `crew feedback` |
| GitHub comment lookups: `pulls/comments/<id>` has `pull_request_url` and `in_reply_to_id`, `issues/comments/<id>` has `issue_url`, and both and `pulls/<n>/reviews/<id>` have `html_url` and `body` | GitHub REST API → `crew reply` (which PR a comment is on, the thread root, the quote) |
| Round text "Round N", an ID listed for every comment and review summary (`comment <id>`, `review <id>`) | `crew feedback` → implementer review-round workflow and `crew reply` |
| State paths, the ledger: `projects/` and `reports/` in the home. Only they are committed by `crew`, edited by agents, or absent from the public repo | `statePaths` in `crew` (`commit` and the guard); checked in `tests/guard.sh`, which also checks this repo tracks nothing there but `projects/.gitkeep`, and no `config/crew.env` or `host` |
| The PostToolUse hook `"${CREW_HOME:-$HOME/crew}/bin/crew" commit --hook` on `Edit\|Write\|NotebookEdit`; `.tool_input.file_path` (or `notebook_path`) and `.cwd` in its JSON | `lead/.claude/settings.json`, `config/worker-settings.json` → `commitHook`, `crew doctor`; checked in `tests/guard.sh` |
| Hook command path `${CREW_HOME:-$HOME/crew}/bin/crew-guard` and role names. `~/crew` is the usual home; `crew install` sets `env.CREW_HOME` in `~/.claude/settings.json`, so hooks find a home anywhere | agent frontmatter, `lead/.claude/settings.json` and `config/worker-settings.json` → `bin/crew-guard` (which runs `crew guard`), `crew doctor`; checked in `tests/guard.sh` |
| A home other than `~/crew` is written into the line `set -q CREW_HOME; or set -gx CREW_HOME …` of `fish/conf.d/agent-crew.fish`, and into the `%h/crew` paths and the `# Environment=CREW_HOME=` line of `systemd/*.service` | those files → `renderLines` in `crew install`; checked in `tests/host.sh` |
| What workers may do on GitHub (a restack adds nothing new: `--force-with-lease` on their own branch, and `gh pr edit --base`) | `implementer.md` ("On GitHub, you may do exactly this") ↔ `worker-settings.json` `allow`/`deny`; checked in `tests/guard.sh` |
| Locks: `.crew.lock` (every mutation), `.crew-tick.lock` (one `crew tick` at a time) and `.crew-push.lock` (one ledger push at a time), taken with `flock(2)`; Go opens them close-on-exec, so a session or background push crew starts never holds one | `crew` → `claude`; checked by `tests/stubs/claude` (no open fd on a `.crew*.lock` file) and `tests/crew-e2e.sh` |
| `crew` on PATH for agents | `conf.d` and the units' `PATH` (the lead, through `lead` and `crew serve`, which also prepends it), `claude` (in `sessions.go`) prepending PATH (workers, goal leads, gardener) |
| `crew setup` makes `upstream` the public repo, with its push URL `DISABLED`, and `origin` the private one; `crew upgrade` merges the newest `v<version>` tag `upstream` has, once its binary has been fetched | `crew setup` → `crew upgrade`; checked in `tests/host.sh` |
| A release is the tag `v<version>`, matching `VERSION`, with `crew-Linux-x86_64` and `crew-Linux-aarch64` attached, each stamped with that version (`-X main.version`) | `.github/workflows/release.yml` → `crew upgrade` (`newestRelease`), `bin/crew`, `crew doctor` (the binary's version is `VERSION`'s) |
| `bin/crew` runs `bin/release/crew-<VERSION>` (or `CREW_VERSION`), fetching a missing one from `https://github.com/isaacjwilliams/crew/releases/download/v<version>/crew-<uname -s>-<uname -m>` and checking it with `crew version`; it exits 2 for `crew guard` when it can't, so a missing binary blocks edits | `bin/crew` ↔ the release's asset names, `crew upgrade` (`CREW_VERSION=<new> bin/crew version`); checked in `tests/host.sh`, with `tests/stubs/curl` serving releases |
| The lead's settings are `lead/.claude/settings.json`; a goal lead's are the same without `agent` (deleted from the parsed file, key order kept), so the two can't drift | `launchGoalLead`; checked in `tests/guard.sh` and `tests/crew-e2e.sh` |
| Settings file names | `crew` |
| Every tool `crew` needs (`deps` in `crew doctor`) is on the `PATH` its systemd services set | `systemd/*.service` → `crew doctor`, for enabled units (`crewServices`) |
| `wt switch … --format json` prints `{"action", "path", …}`; `--config-set worktree-path=<TOML string>` sets the path; unapproved hooks fail when stdin isn't a terminal | Worktrunk → `worktreeCreate`; mirrored in `tests/stubs/wt` |

## Extension points

| Goal | Where |
|---|---|
| Change how the lead interviews you or writes briefs | `lead.md` (intake, brief standard); `templates/task.md` if sections change (then check `crew ready` and the contracts above) |
| Change coding standards | The user's `~/.claude/CLAUDE.md` (taste) or the repo's `AGENTS.md` (project conventions). `implementer.md` holds no standards of its own; it relies on those loading |
| Change the PR-body format | `implementer.md` (first run, step 7) |
| Tune the readability review | `casual-observer.md` (budget, checklist, severity, model) |
| Tune the plan review | `plan-reviewer.md` (questions, report format, model); where the lead runs it and what it shows the user are intake step 7 of `lead.md` |
| Tune the gardener | `gardener.md`; the range and turn limit are in `config/defaults.env` (set them in `config/crew.env`) |
| Allow or deny a command | `config/*-settings.json` (`allow` for fewer prompts, `deny` as a tripwire); what an agent may do on GitHub goes in its prompt |
| Add a `crew` subcommand | A new `cmd…` function in `cmd/crew/`, its entry in `commands` (`main.go`), the help text (`help.go`), `fish/completions/crew.fish` |
| Change how stacks behave | `cmd/crew/stacks.go`; the restack note and the two implementer sections are one contract (above) |
| Give workers MCP tools (e.g. Playwright) | Nothing: configure the server for the repo (`.mcp.json`, or `claude mcp add`). To keep one project's workers off a server, add a `deny` rule to `projects/<p>/worker-settings.json` |
| Add worker settings for one project (`allow` rules, `env`) | `projects/<p>/worker-settings.json` in the ledger (user-owned, add-only merge; see `workerSettings`) |
| Support another worktree tool | A new `WORKTREE_TOOL` case in `worktreeCreate`/`worktreeRemove` (`worktree.go`), a stub in `tests/stubs/`, `crew doctor`, `config/defaults.env` |
| Change how a goal lead works | `goal-lead.md` (parking rules, workflows); its intake is the lead's (`lead.md`), so a change there reaches both |
| Add another agent | New `agents/*.md` (`crew install` copies every `*.md`); add a guard role (`guard.go`) if it edits files, and a settings file with its `allow`/`deny` rules if it runs in the background |

## Verification

```bash
tests/run.sh            # lint + guard + crew-e2e + host; must pass before a change is done
```

- **`lint`**: `gofmt`, `go vet` for Linux and for macOS, `go test ./...` (the ledger model, settings files, the guard's table, `resolve`, statuses against `LEDGER.md`), `sh -n` on `bin/`, `bash -n` on the tests, shellcheck on both, fish syntax, JSON validity, and `claude plugin validate agents`.
- **`guard`**: the write-guard allow/block table, run directly and through the lead and worker settings hooks, the home's state paths among them, plus checks that this repo tracks no state, that the lead, the implementer and the goal lead have no tool allowlist, that no settings file turns the sandbox on, that the lead and workers default to auto mode, that the settings run `crew commit --hook`, that the lead service leaves itself stopped on exit 3, and that the agents' prompts agree with `crew` and the settings.
- **`crew-e2e`**: `crew` end to end in a crew home made from this checkout (`make_home`, which puts a binary built from this checkout in its `bin/release/`), against a bare-repo remote, with stub `claude`, `gh` and `wt`.
- **`host`**: a machine and its home, in fake `HOME`s: `crew install` (a fresh install, unchanged re-runs, edited, new and dropped files, skipped components, the AGENTS.md question, a home outside `~/crew`, `crew doctor`'s checks), the lease between two machines (`host up`, `--take`, a lost lease, the stand-down, `host down`, `crew serve`), `crew setup`, `crew upgrade` to a release tag in a fake public repo, and the launcher (a download, a failed one, `CREW_VERSION`), with `tests/stubs/curl` serving the releases.

Every suite runs in a fake `HOME` under `tests/.tmp/` with the stubs first on `PATH`, so the tests never start a model session or touch the real setup. See `tests/README.md` for how to add cases.

What the tests *can't* cover is the live Claude Code behaviour listed below. After changing anything that depends on it, do a smoke test with a tiny task.

## Assumptions to re-check when Claude Code updates

Built against Claude Code 2.1.281–2.1.285 docs and CLI (September 2026); the portable crew (the home, the lease, the lead as a service) against 2.1.296 (October 2026).

- **Live checks still open** for the portable crew, each in a person's real setup:
  - L3: in a phone lead session, nothing from the home's `CLAUDE.md` loads, `~/.claude/CLAUDE.md` does, and reading `~/agent-work/<p>/_lead/AGENTS.md` needs no prompt.
  - L4: a question the lead ticks reaches `origin` within seconds as "edit projects/…".
  - L5: plan-reviewer's `git -C` commands run without a denial or a prompt (check its transcript).
  - L6: a failover drill with one `pr-open` and one `working` task: `crew host up --take` on another machine, its tick re-homing the working task, and the old machine standing down when it's back on the network.
  - L7: a goal lead started with the lead's settings minus `agent` runs as goal-lead.
  - L8: the colleague path on a fresh account: clone the public repo, `crew setup`, `crew project add`, a lead from the phone.
- **Releases** haven't been made yet. `bin/crew` fetches `https://github.com/isaacjwilliams/crew/releases/download/v<version>/crew-<uname -s>-<uname -m>`, GitHub's documented download URL for a release asset, with `curl -fsSL` (which follows GitHub's redirect to its storage). `.github/workflows/release.yml` assumes `actions/checkout@v4` and `actions/setup-go@v5` (with `go-version-file`), that fish installs on `ubuntu-latest`, and that `gh release create --notes-from-tag` reads the annotated tag's message from the local clone, hence the explicit fetch of the tag. The first tag pushed checks all of it.
- **Settings and instructions for the lead in `lead/`:** a project's `"agent"` setting picks a session's agent; `permissions.additionalDirectories` and `PreToolUse` hooks apply to sessions a `remote-control` server starts (checked live on 2026-10-20). From the docs, not live: `claudeMdExcludes` (absolute-path globs, any settings layer) keeps a CLAUDE.md out of a session, and CLAUDE.md files in parent directories load by default, hence the exclude for the home's `CLAUDE.md`. `auto` can't come from a project settings file, so the mode is passed on the command line (`LEAD_PERMISSION_MODE`).
- **Background sessions don't show on Claude Mobile** (checked live on 2026-10-20). The phone sees workers only through the lead.
- **Cloud sessions were ruled out** for the lead: they load no user-level agents or plugins, run on throwaway machines, and have no `--bg`.

- **Agent view / background sessions** (`--bg`, `claude agents --json`, `claude rm`) are a research preview. Their output formats are the most fragile contracts.
- **Resuming a background session:** `claude --resume <session> --bg` with launch flags (`--agent`, `--settings`, …) starts a copy under a new session, whether or not the session's process is still open. 2.1.284 says so in a `note:` line ("keeps its own saved options, so the flags you passed started a copy… Without flags, the same command continues <session> itself") and prints the job ID in color. `crew followup` resumes with no flags and relies on the session keeping its agent, permission mode, auto-compact window, settings and `--add-dir`. A finished session also keeps its process open for about an hour (state `done`, `pid` set), and resuming it then starts a copy too ("session <id> is already running in the background"), so `crew followup` runs `claude stop` on it first. A live run confirmed both causes of a copy; resuming in place after a stop hasn't been confirmed yet.
- **Removing a background session:** the agent-view docs say `claude rm` removes a session from the list, and leaves "a worktree you created yourself and started the session inside". They don't say whether it ends a process that's still open, so `crew cleanup` runs `claude stop` first. That `claude rm` needs no confirmation when run without a terminal hasn't been confirmed by a live run.
- **Confirming a launch:** `crew` counts a launch as started once `claude agents` lists the job with a `pid` and a `sessionId` that starts with the job ID. On 2.1.296 every job's ID was its session's first 8 characters. A resumed job's `pid` and `sessionId` come from its live process. A process that has just started can show a new session ID before it loads the resumed one. A resume of a missing session ends in `failed` about 10 seconds after launch ("source session … not found"). This comes from one incident and from reading the 2.1.296 code; a live run hasn't confirmed the timing.
- **`--autocompact <auto|tokens>`** sets a session's auto-compact window (100k–1M, per `claude --help` in 2.1.285) and overrides the user's `autoCompactWindow` setting. Workers on a 1M model otherwise compact at about 967K. That a resumed worker keeps the window it was started with hasn't been confirmed by a live run.
- **AGENTS.md loading** is automatic only when no `CLAUDE.md`/`CLAUDE.local.md` is present, unless Project instructions is set to `claude-md-and-agents-md`.
- **`--settings` given a JSON string** (a project with `worker-settings.json`) is assumed to apply the same way as a file. This hasn't been confirmed by a live run.
- **Resumed conversations keep their system prompt** until they're compacted (`--system-prompt-snapshot`, on by default). We assume that includes the `--agent` instructions, so `lead -c` and `crew followup` run on the `lead.md`/`implementer.md` they started with. This hasn't been confirmed by a live run.
- **Session names:** `claude --resume <name>` resumes by name, and an interactive session whose name is already live gets a variant of it. Every lead session is named `lead`, so several past sessions share it.
- **Auto mode** (`--permission-mode auto`) needs a plan that has it. The lead and workers default to it.
- **Stacked PRs on GitHub:** when a merged PR's head branch is deleted, GitHub retargets the open PRs based on it to the branch it merged into; deleting an unmerged branch closes them instead. Without the deletion, a child PR keeps targeting the merged branch until its worker runs `gh pr edit --base`. Either way the child's branch needs the rebase.
- **Restack through followup:** `crew restack` relies on the same resume behaviour as `crew followup`, and on the worker following a rebase command from its prompt. Neither has been confirmed by a live run.
- **Worktrunk** (built against `wt` 0.78): the `switch`/`remove` flags and JSON shape in the Contracts table. Run `wt switch --help` after upgrading it.
- **Frontmatter hooks** in `~/.claude/agents/` run without a workspace-trust prompt; project-level agent hooks would need trust.
- **Workspace trust** covers the folders below the one it was accepted in. Seen live on 2026-10-21: trust accepted in `~/crew/lead` was recorded in `~/.claude.json` for the repo's top level, `~/crew`, and `claude` in `~/crew/lead` then didn't ask again. `crew doctor` counts a folder above as trust for that reason. Background sessions are assumed to inherit it the same way from `~/agent-work`; that hasn't been confirmed by a live run.
- **Tool inheritance:** an agent without `tools:` inherits every tool in its session, MCP servers included, also when it runs as the main session with `--agent`. `disallowedTools` names that don't resolve are assumed to be ignored. Neither has been confirmed by a live run.
- **Hooks from `--settings`** fire for tool calls in subagents, with `agent_type` set, while frontmatter hooks run only for their own agent. `Agent(<name>)` deny rules block one subagent type. Neither has been confirmed by a live run.
- **Background subagents:** `background: true` keeps plan-reviewer in the background even where fork mode is off, so the lead stays free to talk while it runs. Its result arrives in a later turn as a completion notification. A background subagent keeps Read, Grep, Glob and Bash, which is all plan-reviewer uses. Documented in `sub-agents`; not confirmed by a live run.
- **CLAUDE.md in subagents:** a subagent loads the whole CLAUDE.md hierarchy (`~/.claude/CLAUDE.md`, the project's CLAUDE.md or AGENTS.md) unless its frontmatter sets `omitClaudeMd`, and never sees the parent's conversation. plan-reviewer relies on both: the user's taste arrives that way, and its read stays independent of the lead's. As a subagent of the lead, which runs outside every repo, it gets no project CLAUDE.md or AGENTS.md, and reads them at the task's base through `git -C` instead. The lead's `claudeMdExcludes` (in `lead/.claude/settings.local.json`) is assumed to keep the home's `CLAUDE.md` out of plan-reviewer too; not confirmed by a live run. Documented in `sub-agents`; not confirmed by a live run. The implementer, a main session started with `--agent`, is assumed to load `~/.claude/CLAUDE.md` the same way: it has no coding standards of its own. Documented in `memory`; not confirmed by a live run.
- **Worker states** (`claude agents --json`): `state` blocked means the session needs the user, whether it asked a question or holds a prompt open; `status` (busy, waiting or idle) is set only while the process is alive, and `waitingFor` names an open prompt when `status` is waiting. Documented in `agent-view`. Seen live on 2026-10-15: a worker that ended its turn on a question read `blocked`, `busy` (a dev server it had started was still running), with no `waitingFor`. A permission prompt reading `waiting` hasn't been seen live. Seen live on 2026-10-16 (2.1.296): after a turn ended cleanly, a worker whose dev server was still running read `working`, `busy` for over two hours, while its terminal showed "done · 1 shell still running"; its transcript had the turn's `turn_duration` entry. The docs say `working` covers "work it drives on its own". That dev server, started the day before, also survived two `claude stop`s and resumes: the docs say background shells carry over to the session's next process.
- **Transcripts** aren't a documented interface. If Claude Code moves them or stops writing `turn_duration`, `turnEnded` finds nothing and a `working` worker counts as mid-turn again: rounds wait for it, as before 2026-10-16, and nothing is interrupted.
- **Goal leads** haven't been run live. Three things they rely on are untested: that a background session gets the repo's MCP servers and the user's connectors (a tracker, say) and can read them in `auto` mode without a prompt; that `plan-reviewer`'s reports reach a background session in a later turn after it ends its own; and that a goal lead whose turn has ended while reviews still run takes a resume without losing them (the docs say background subagents carry over to the next process).
- **A PR's `updatedAt`** equalled the time of the last review or conversation comment, to the second, on every open PR in cli/cli and rails/rails whose last event was one (checked 2026-10-15). GitHub doesn't document it. If it stops holding, `crew tick` misses comments on quiet PRs until something else touches them; drop `prMayHaveNewComments`.
- **The lead as a service** (`crew-lead.service`, `crew serve`): `claude remote-control --name crew --spawn same-dir --permission-mode <mode>`, run by systemd with no terminal. Checked live on 2026-10-20 (2.1.296): the phone can start sessions on a `remote-control` server and they honour the project's `"agent"`; project `additionalDirectories` and PreToolUse hooks apply in them; `--continue` brings back only the server's own first session, and sessions started from the phone hang after a server restart; `--permission-mode auto` works, `--settings` is refused in server mode. On 2026-10-21 `--continue` failed on a first start ("No recent session found in this directory"), and the docs say it serves only that one session, so `crew serve` doesn't pass it: a plain start brings back every session the last server served, within about four hours. `--spawn same-dir` keeps sessions in `lead/`, whatever spawn mode was saved for it. Checked live on 2026-10-21: the service starts and serves with no terminal, `crew` appears on the phone, and its sessions run as the lead and read the ledger. `initialPrompt` doesn't fire in them (the server runs each as `claude --print --input-format stream-json …`), so the lead on the phone waits for a first message instead of opening with a briefing. Checked live on 2026-10-22 (2.1.296): two idle workers that a phone session had dispatched kept running through the restart `crew upgrade` makes, under `KillMode=process` (L2). Not yet checked: that phone sessions come back after a restart.
- **`crew tick` from systemd:** `claude --bg` run from a oneshot service with `KillMode=process` is assumed to leave the background-session daemon, and the workers it resumes, running after the service exits. It also assumes `git fetch` works without your ssh-agent. Neither has been confirmed by a live run. `crew doctor` checks that the unit's `PATH` finds the tools `crew` needs.
- **Live verification:** the lead, implementer and gardener haven't been run end to end against a real model. Do a smoke test with a tiny task after any significant change.
