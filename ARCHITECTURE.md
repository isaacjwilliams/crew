# Architecture

This document is for anyone changing the crew, human or Claude Code. `SETUP.md` covers installing and using it. This file explains how the pieces fit together, the contracts between them, and the invariants that changes must preserve.

## What the system is

Seven Claude Code agents run on one machine at a time (principle 8):

- **lead**: the only agent you talk to, for every project, from a terminal or from Claude Mobile. It turns requests into briefs, dispatches work, routes PR feedback, and reports status.
- **implementer**: one background session per task, each in its own git worktree and branch. It opens a PR and addresses review rounds.
- **casual-observer**: a read-only readability review, spawned by workers before a PR (or used ad hoc).
- **plan-reviewer**: a read-only review of a finished brief's structure, spawned by the lead before the user approves it.
- **reviewer**: a read-only, adversarial review of a worker's open PR, spawned by the `pr-review` skill the worker runs after opening it. When Codex reviews too (`CODEX_REVIEW`), a fresh reviewer answers Codex's findings in each rebuttal round.
- **gardener**: runs on a schedule and turns merged commits into refactor *proposals*, never code. A proposal is any idea worth keeping that isn't a task yet; the user, a lead or a worker can write one too.
- **goal-lead**: one background session per goal the user starts. It does the lead's intake for every item of the goal, without the user.

They coordinate through the **ledger**: Markdown files under `projects/` in the crew home, a clone of the person's private repo, driven by a CLI, `crew`, and pushed to that repo as they change. The crew's code is a public repo; each person's private repo is a copy of it with the ledger added.

## Design principles

These are invariants. Breaking one changes the design, not just the implementation. A change that weakens one needs the user's say first: raise it before writing the code.

1. **Nothing coordination-related touches the shared repo.**
   - No labels, comments other than the ones below, committed config, or files in the working tree. The repo belongs to other people too.
   - The only repo-side footprints are:
     - task branches pushed under `BRANCH_PREFIX`, after every commit;
     - the PRs workers open;
     - optional one-line replies to the user's own review comments: in the thread for an inline comment, otherwise as a PR comment that quotes the one it answers;
     - one comment per pass of automated review summing it up (`crew review-summary`), which a worker edits rather than posting a second for the same pass;
     - one comment for each commit CI went red on after the worker handed its PR over, saying why and what it did (`crew ci-summary`), edited rather than posted twice for the same commit;
     - 👀 and 🚀 reactions to the user's own review comments (`crew react`): picked up, and being changed;
     - two lines in `.git/info/exclude`, which is local to the clone.
2. **The ledger is the single source of truth, it's plain text, and it's pushed to the person's private remote.**
   - Every change is committed at once and pushed in the background, normally within seconds, so another machine can take over with what this one knew. A push that fails is retried by the next commit, and by every `crew tick`.
   - Agents never rely on their conversation for state. The lead is effectively stateless: every session starts by reading the ledger.
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
   - A goal's `max_in_flight` (`GOAL_MAX_IN_FLIGHT` unless set) limits its dispatched-but-unmerged tasks, including open PRs and every task of a stack. Goals are where work outruns review: a goal lead readies tasks with nobody watching, and a stack it builds on a wrong turn has to be undone PR by PR. The lead's own tasks need no such cap, since the user approves each dispatch.
   - `crew dispatch` enforces both under the ledger lock.
6. **The user is the only one who merges.**
   - The agent prompts say exactly what each agent may do on GitHub. For workers that is: push their own task branch (with `--force-with-lease` allowed after a rebase), open a PR, edit it, post its automated-review summary, mark it ready after that review, rerun their PR's failed CI jobs once, and post what they found about red CI. Merging stays with the user.
   - We rely on the prompts for GitHub behaviour and don't try to parse shell commands for it; that approach can't be made complete. `deny` rules in the settings files catch the obvious mistakes (`gh pr merge`, `git push --force`, `gh run cancel`). Branch protection, where the repo allows it, is the real backstop.
   - File edits are enforced mechanically: `crew guard` checks Edit/Write paths. Bash isn't sandboxed (see [Bash](#bash-no-sandbox)). Workers run it in auto mode, the same trust you give your own sessions, and the gardener only runs its read-only allow list and `crew remember`.
7. **Project-agnostic agents, project-specific conventions.**
   - Agents live at user level (`~/.claude/agents/`).
   - Project conventions come from the repo's `AGENTS.md`/`CLAUDE.md`, which Claude Code loads automatically for every session in the repo. The lead and plan-reviewer, which run outside every repo, read it explicitly. The user's personal taste goes in `~/.claude/CLAUDE.md` or `~/.claude/rules/`.
   - Facts about a project that the user approved, and that collaborators shouldn't see or that would be lost in a merged task's Decisions, go in its project memory in the ledger (see [Project memory](#project-memory)).
   - Agent prompts should stay generic.
8. **One machine runs the crew at a time.**
   - The home's `host` file names it, and a git push claims it, so two machines can't both believe they hold it.
   - Every change to the ledger checks it, and so does everything that starts a session, so two machines never dispatch the same task. Other machines only read.
   - Moving the crew is `crew host up --take` on the new machine. Work in flight carries on there with fresh workers: sessions stay behind, but task files and pushed branches don't.
9. **Plain words.**
   - Each concept gets an ordinary word, used in its ordinary sense: lead, worker, task, goal, proposal, memory.
   - A new concept gets a word a newcomer would guess right, not a coined term or a metaphor that needs a glossary.
   - Existing terms stay, crew's own among them (ledger, brief, gardener, stack and restack, crew home, goal lead, host). This principle is for what's added.
10. **Small and quiet.**
    - crew is one Go binary on the standard library, the agent files, and plain text. There's no database. The dependency rules are in `CLAUDE.md`, under Conventions.
    - Plain `crew` commands on timers coordinate the work, not agents supervising agents. crew starts a worker, a goal lead or the gardener only when there's work for it: see [`crew tick`](#crew-tick) and [Gardener](#gardener) for when. So a crew with no work sends no model requests of its own. For the gardener, new commits on the default branch are work, whoever made them.
    - Two processes stay up while the crew is idle, and both are Claude Code's. One is the lead's Remote Control server (`crew serve`, the `crew-lead` service), which waits for you to open the lead from your phone. The other is the daemon that `claude --bg` hands workers and goal leads to, which outlives them. Claude Code's docs don't say either one uses the model while it waits (see [Assumptions](#assumptions-to-re-check-when-claude-code-updates)).

## Repository layout and where each file installs

A clone of this repo is a **crew home** (`~/crew` by default): `crew` runs from its `bin/`, reads its `config/` and `templates/`, and keeps the ledger under `projects/`. `crew install` links `crew` and `lead` into `~/.local/bin`, and copies only what Claude Code, fish and systemd (launchd on a Mac) have to find elsewhere.

This repo is the public one, and never contains state: nothing under `projects/` but `.gitkeep`, and no `reports/`, `config/crew.env` or `host`. A person's private home adds those (see [Runtime state](#runtime-state-created-by-use-never-shipped)), so merging a new public version into it never conflicts on them.

| In this repo | Installed to | Role |
|---|---|---|
| `agents/lead.md` | `~/.claude/agents/` | Lead system prompt, tool denylist, `initialPrompt` briefing (terminal sessions only; cut to what needs input when a request follows it), write-guard hook (role `lead`) |
| `agents/implementer.md` | `~/.claude/agents/` | Worker prompt: first-run, automated-review and review-round workflows, stop-and-ask rules; GitHub allowances; tool denylist; write-guard hook (role `worker`) |
| `agents/casual-observer.md` | `~/.claude/agents/` | Read-only reviewer: context budget, what to flag, output format; Sonnet, medium effort |
| `agents/reviewer.md` | `~/.claude/agents/` | Read-only PR reviewer: gets a scope line, the rubric's path and the briefing's path; derives intent from the code before reading the stated intent; Opus, high effort |
| `.claude/skills/pr-review/` | — | The `pr-review` skill (`SKILL.md`) and its generic rubric (`rubric.md`): one automated review of a worker's PR, by the reviewer agent and, when it's on, Codex through `crew codex`. Not installed: every worker and goal lead loads skills from the home through its `--add-dir`, and leads load them as the home's project skills |
| `agents/plan-reviewer.md` | `~/.claude/agents/` | Read-only plan reviewer: gets only the task file path; reads the code at the task's base on `origin`; runs in the background (`background: true`); the structural questions, report format; Opus, high effort |
| `agents/goal-lead.md` | `~/.claude/agents/` | Goal lead prompt: intake without the user (it reads the intake sections of the installed `lead.md`), parking rules, the new-goal and new-input workflows; tool denylist; write-guard hook (role `lead`) |
| `agents/gardener.md` | `~/.claude/agents/` | Proposal writer: survey process, dedup against open proposals from any source, max 3 proposals; write-guard hook (role `gardener`) |
| `bin/crew` | `~/.local/bin/` (a link) | The launcher, a POSIX `sh` script: runs `bin/release/crew-<VERSION>`, downloading it from that release the first time (below) |
| `bin/crew-guard` | — | PreToolUse hook: two lines of `sh` that run `crew guard` (below) |
| `bin/lead` | `~/.local/bin/` (a link) | The `lead` command: a short `sh` shim that finds its home through symlinks, as `bin/crew` does, and runs `crew lead` (`cmd_lead.go`, below) |
| — | `bin/release/crew-<version>` | The binary for this machine, from the release; gitignored |
| `cmd/crew/` | — | The CLI, in Go (below); `go.mod` names the module |
| `.github/workflows/release.yml` | — | On a pushed `v*` tag in the public repo: tests (the Go tests on macOS too), builds the Linux and macOS binaries and publishes them as the release |
| `config/defaults.env` | — | Every setting, its default and its comment; read by `crew` |
| `templates/crew.env` | — | The short starting point for a person's `config/crew.env` (their overrides of `defaults.env`) |
| `lead/.claude/settings.json` | — | The lead's session, in `lead/`: `"agent": "lead"`, its permission `allow`/`deny` rules, the `crew-guard` hook (role `lead`), which covers its subagents, and the SessionStart hook that loads project memory. Goal leads get the same file without `agent`, with their project in its `env` (`sessionSettings`), as a JSON string to `--settings` |
| — | `lead/.claude/settings.local.json` | Written per machine by `crew install`, gitignored: `permissions.additionalDirectories` (the home and `WORK_ROOT`), `claudeMdExcludes` (the home's `CLAUDE.md`, which is for developing crew) and `enableAllProjectMcpServers`, which approves `lead/.mcp.json`. Not in `lead/.claude/settings.json`: goal leads load that file in a project's checkout, where it would approve the repo's own `.mcp.json` |
| — | `lead/.mcp.json` | Every project's MCP servers, which the leads load as their folder's; written by `crew mcp sync`, gitignored (see [MCP servers for one project](#mcp-servers-for-one-project)) |
| `config/{worker,gardener,readonly}-settings.json` | — | Passed with `--settings`: permission `allow`/`deny` rules for workers (`worker`), the gardener (`gardener`: the read-only list plus `crew remember`) and `crew observe` (`readonly`, which has no hooks). The worker and gardener files load project memory at session start (`crew memory --hook`); the worker file also hooks `crew-guard`, which covers their subagents. crew passes the worker and gardener files as a JSON string, with the project and home in their `env` (`sessionSettings`) |
| `templates/task.md` | — | Task file shape: front matter and sections |
| `templates/goal.md` | — | Goal file shape: front matter and sections |
| `templates/review-comment.md` | — | The automated-review summary a worker fills in from its task's `## Automated reviews` and posts with `crew review-summary` |
| `templates/ci-comment.md` | — | Why CI was red on a worker's PR and what it did, which the worker fills in for a round's `CI failed on` line and posts with `crew ci-summary` |
| `templates/proposal.md` | — | Proposal file shape: front matter and sections. The gardener fills every section; `crew propose` only Why it matters |
| `LEDGER.md` | — | Ledger conventions |
| `VERSION` | — | crew's version, `X.Y.Z`; `crew upgrade`, `crew install` and `crew doctor` show it |
| `projects/.gitkeep` | — | Where the ledger goes |
| `.gitignore` | — | Ignores what crew keeps per machine: `bin/release/`, `logs/`, the `.crew*.lock` files, each project's `local.env` and `mcp.local.json`, the lead's `settings.local.json` and `.mcp.json`, and `tests/.tmp/` |
| `fish/completions/crew.fish` | `~/.config/fish/completions/` | Subcommand, task-ID and proposal-ID completion |
| `fish/completions/lead.fish` | `~/.config/fish/completions/` | `lead --project` completion |
| `systemd/crew-gardener.{service,timer}` | `~/.config/systemd/user/` | Weekly `crew gardener --all` (installed but not enabled) |
| `systemd/crew-tick.{service,timer}` | `~/.config/systemd/user/` | `crew tick` every 3 minutes, `KillMode=process` so the sessions it resumes outlive the run (installed but not enabled) |
| `systemd/crew-lead.service` | `~/.config/systemd/user/` | `crew serve`: the lead as a Remote Control server named `crew` in the home's `lead/`, for Claude Mobile. Restarted unless `crew serve` exits 3 (this machine doesn't run the crew). Enabled by `crew host up`. See "The lead as a service" under Assumptions |
| `launchd/crew-{tick,gardener,lead}.plist` | `~/Library/LaunchAgents/` | On a Mac, the twins of the three services above: LaunchAgents in the user's `gui/<uid>` domain, labelled like the units without their suffix, shipped `Disabled`, logging to the home's `logs/`. Rendered for the home on every install, since launchd expands nothing. A Go test checks each against its unit (`services_test.go`) |
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
    mcp.json                            optional, hand-written: MCP servers for the project's sessions
    mcp.local.json                      written from mcp.json, ${NAME}s filled in, mode 0600; not committed
    tasks/NNN-<branch-name>.md          one task each (`crew new --branch`, else from the title)
    goals/NNN-<slug>.md                 one goal each (`crew goal new`)
    proposals/YYYY-MM-DD-slug.md        one idea each, from the gardener or `crew propose`
    memory.md                           project memory: facts the user approved, written by the lead
    memory-suggestions.md               facts suggested for memory.md (`crew remember`), each waiting on the user
    gardener.last                       last commit the gardener reviewed
  lead/.mcp.json                        every project's mcp.local.json servers, for the leads, mode 0600; not committed
  logs/gardener-*.log                   not committed
  logs/worktree-<p>-<id>.log            Worktrunk hook output from dispatch/cleanup; not committed
  .crew.lock                            flock target, not committed
  .crew-tick.lock                       held while a crew tick runs, not committed
  .crew-push.lock                       held while the ledger is pushed, not committed
  host                                  the machine that runs the crew (the lease), committed and pushed by crew host
  .crew-lease-lost                      a push found the lease moved to another machine; not committed
  logs/push.log                         output of background ledger pushes; not committed
  logs/crew-{tick,gardener,lead}.log    on a Mac, the launchd jobs' output (systemd keeps it in the journal); not committed
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
you ⇄ lead (a session in <home>/lead, for one project or every one; reads code in crew checkout <p>)
  lead: confirm goal → crew new "<title>" --branch <name> → investigate → plan → acceptance criteria
        → plan-reviewer subagent in the background (task file path only) → summary right away
        → review arrives → findings settled → Raise findings to you
        (each point written into the draft task file as it's agreed)
  you: "go"                             # if the review is still running, the lead dispatches once it's settled
  lead: crew ready TASK                 # checks Goal / Structure / Plan / Acceptance criteria are non-empty
        crew dispatch TASK
          ├─ lock; check status, depends_on (same project, must be merged), MAX_WORKERS, the goal's max_in_flight; unlock
          │     a stacked task's parent is exempt from "must be merged";
          │     a parent that has already merged → base rewritten to its base
          ├─ git fetch origin <base> (must succeed for a stacked task: the parent's branch has to be pushed);
          │   create ~/agent-work/<p>/<id> on a new <branch> from origin/<base>, recorded as base_sha:
          │     WORKTREE_TOOL=git        git worktree add --no-track -b …
          │     WORKTREE_TOOL=worktrunk  wt switch --create … --no-cd --format json --config-set worktree-path=…
          │                              (runs the repo's pre-start hooks; output → logs/worktree-<p>-<id>.log)
          ├─ copy WORKTREE_COPY files
          ├─ lock; record worktree; repeat the checks
          ├─ cd worktree; claude --add-dir <home> --bg --agent implementer --permission-mode … \
          │     --autocompact <WORKER_AUTOCOMPACT_WINDOW> --settings <worker settings, env: project and home> --name crew-<project>-<id> "<prompt>"
          ├─ strip color codes; parse "backgrounded · <job>"; poll `claude agents --json --all` until the job's
          │     process reports a session ID that starts with the job ID (job `failed` → stop, record nothing)
          └─ record worktree, worker_name/job/session; status → working
```

### Worker first run
The worker reads the task file, then implements with commits, pushing its branch after each one so its work is never only on one machine, and runs the checks from `AGENTS.md`. Next it spawns casual-observer (if `pre_pr_review: yes`), fixes the findings it agrees with, and logs the ones it declines. It pushes, runs `gh pr create` (draft by default, and always draft while automated review is to run), and records the PR with `crew set TASK pr …` and `crew log`.

Then, unless `AUTO_REVIEWS` is 0, it reviews its own PR:
```
worker: /pr-review TASK  (the skill, from the home's .claude/skills/)
          ├─ range: fork point of the PR's baseRefOid and HEAD
          ├─ briefing (Scope, How to verify, Hard constraints, Stated intent) in a temp dir
          ├─ CODEX_REVIEW=yes: crew codex, in the background: the same briefing and rubric
          ├─ reviewer subagent: the range, the rubric's path, the briefing's path
          ├─ both reported: up to AUTO_REVIEW_REBUTTALS rounds between them, while anything is open
          │    (each reviewer started fresh, with a round file: its report, earlier rounds, this round's items)
          └─ checks each finding, settles its severity, appends "### Automated review N" under "## Automated reviews"
        → per finding, by agreed severity: fix and push / crew propose / settle a split / left for the user → its Outcome line
        → again, until a review is clean or AUTO_REVIEWS have run
        → templates/review-comment.md → crew review-summary TASK FILE → "Summary posted"
        → gh pr ready (pr_mode ready, PR still a draft) → crew set TASK status pr-open
```
That's the first pass of automated review. A review round with an `Automated review requested` line (below) has the worker run another, once it has addressed the round's comments. A pass is the reviews after the last `Summary posted`, derived from the record rather than stored: it runs up to `AUTO_REVIEWS` reviews (at least one when the user asked for it), numbered on from the last pass, and posts a summary comment of its own.

The task file's record holds the loop's whole state, so a worker resumed or compacted in the middle carries on from it. If it gets blocked, it writes an Open question, sets `status needs-input`, and ends its turn with the question.

### Review feedback
```
you review on GitHub → tell the lead (or run it yourself):
  crew feedback TASK     # your comments newer than feedback_since → "### Round N" before "## Log"
                         # collaborators' comments (write access, not bots) → unchecked items under Open questions
                         # skips: anyone else's comments, replies tagged <!-- crew -->,
                         #        comments in PENDING reviews; inline comments timed by review submitted_at
  crew followup TASK     # refuses if the worker is busy (mid-turn or on an open prompt), or if there's no new round and no --note
                         # refuses a merged or abandoned task, checked under the ledger lock
                         # a worker that isn't busy but whose process is still open (pid set) → claude stop <job> first
                         # resume: claude --resume <worker_session> --bg "<prompt>" in the worktree, with no launch flags:
                         #   the session keeps its own agent, permission mode, settings and --add-dir, and
                         #   passing any of them again makes Claude Code start a copy instead, as does an open process
                         # a copy anyway (session ID changed) → warn, record the copy's job and session
                         # if resume fails, or its job ends `failed` → fresh worker with a "pick up this task" prompt
                         # a new round's prompt says whether to reply on the PR (REPLY_TO_THREADS)
                         # records last_round_sent; status → addressing-feedback
  crew review TASK       # another automated review: a round holding only "- [ ] Automated review requested (crew review)";
                         # refuses a task with no open PR; sends it now (crew followup) if the worker is idle,
                         # else leaves it for crew tick. Your PR comment "crew review" (its whole text, any case)
                         # becomes the same line in crew feedback's round: "(comment <id>)", one line for each
  crew ci-fix TASK       # red CI: run by crew tick (below); a round holding only "- [ ] CI failed on <sha>: <checks>"
worker: crew react TASK eyes <every ID in the unsent rounds>
        → a "CI failed on" line first: find why (gh run view --log-failed for GitHub Actions, else as the project says)
          → the branch caused it: fix, commit, push; or not: rerun the failed jobs once (gh run rerun --failed), ask if
          still red → crew ci-summary TASK SHA FILE (templates/ci-comment.md) → tick the line
        → address each comment, oldest round first (crew react TASK rocket <id> before changing code for it)
        → commit (no force-push) → push
        → crew reply TASK <id> "…" (if told to) → an "Automated review requested" line: a new pass → status pr-open
                         # crew reply: an inline comment → reply in its thread (to the thread root);
                         #   a conversation comment or review summary → a PR comment quoting it;
                         #   refuses an ID that isn't a comment or review on the task's own PR
                         # crew react: GraphQL addReaction on each one's node_id, the same for all three kinds;
                         #   eyes or rocket only; checks every ID first (on the task's own PR, the user's own)
                         #   and reacts to none if one fails
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
  └─ crew followup TASK --note "…git rebase --onto <target sha> <base_sha>…
                                  git push --force-with-lease=<branch>:<origin/<branch> sha> origin <branch>…"
worker: rebase → checks → the note's push, as a command on its own
        → if the base changed: gh pr edit --base <new>; crew set TASK base <new>   → status pr-open
```

- **`base_sha`** is the newest commit of the parent's branch that the task's own branch contains, the cut point for `git rebase --onto`. `crew dispatch` records it and `noteBaseSHA` (from `crew sync` and `crew restack`) moves it to the two branches' merge base. Once the parent is squash-merged or rebased, that merge base falls back to a commit before the parent's work, so the recorded value is kept for as long as the task's branch still contains it. Only `crew` writes it.
- **Whether a restack is needed is computed, never stored** (`restackReason`): the parent is `merged` or `abandoned`, or `origin/<parent branch>` isn't an ancestor of the task's local branch. It reads the refs as last fetched, so `crew status` is as fresh as the last `crew sync`.
- **The worker changes `base` and the PR's base, last.** `crew restack` returns as soon as the worker is resumed. If it rewrote `base` itself, a rebase that then failed would leave a task that no longer looks stacked, with the parent's commits still in it.
- **A goal's `max_in_flight`** (`goalInFlight`) counts every task of a stack, as `MAX_WORKERS` counts every session. A deep stack is the case it's for.
- **`restack_sent`** is the commit the last `crew restack` asked the worker to rebase onto. `crew restack` refuses to send the same one again unless `--again`, so a worker that couldn't restack isn't sent the same note in a loop. Only `crew` writes it.
- **`crew sync` never restacks.** `crew tick` does, through `crew restack`. Nothing restacks a whole stack at once: each restack is a background worker, and the next task up only needs one after the worker below it has pushed.
- **`crew observe TASK`** compares against `base_sha` when `origin/<base>` is gone.

### Sync and cleanup
- `crew sync` finds missing PR URLs by branch (`gh pr list --head`), records `pr_state`, `pr_updated` and `ci_state` (from `statusCheckRollup`, by `rollup` in `cmd_ci.go`), and sets `status: merged` when GitHub says merged. Then it checks the stacks, as above.
- `crew cleanup TASK|--merged` stops the worker and removes it from agent view (`removeSession`), then ends any background process still running in the worktree (`stopProcessesIn`: TERM, then KILL after 10 seconds), then removes the worktree (`git worktree remove`, or `wt remove --no-delete-branch --foreground` so the repo's remove hooks run) and deletes the local branch (merged tasks only). The worker goes first so that it doesn't carry on in a deleted directory. Its leftover processes go next: a dev server it started in a background shell outlives `claude stop`, and would otherwise keep running, holding its port, after the worktree is gone. They're found by working directory: in `/proc` on Linux, with `lsof` and `ps` on a Mac. Only processes without a terminal are ended: one with a terminal is the user's, such as their shell sitting in the worktree. crew's own process and those that started it are left alone too. A worker whose process won't close keeps its worktree until a later cleanup; one that `claude rm` won't delete only gets a warning. Cleaning up a merged parent is safe while tasks are still stacked on it: their branches contain its commits, a restack cuts at `base_sha`, and nothing reads the parent's local branch.

### `crew tick`
One pass over every project, run by `crew-tick.timer` every 3 minutes (the `crew-tick` job on a Mac), by the lead (at session start and for status), or by hand. A second tick while one runs exits at once (it doesn't wait for `.crew-tick.lock`). Each step is a `crew` command run as a process of its own, so one that refuses doesn't stop the rest. It prints one line per thing it did, and one per review round or goal input it holds back for a busy session; nothing otherwise.

```
crew tick
  ├─ git fetch origin (soft). origin's host names another machine, and this one held the lease
  │    → stand down: crew-* sessions stopped, unpushed ledger work kept on
  │      lost/<host>-<time>, home reset to origin, services disabled; nothing else happens
  ├─ require_host: this machine holds the lease, or the tick stops
  ├─ crew sync --all
  ├─ crew mcp sync: every project's mcp.local.json, then lead/.mcp.json
  ├─ for each task:
  │    merged, worktree still recorded           → crew cleanup
  │    in flight, PR changed since last read     → crew feedback (the user's comments → a round;
  │                                                  collaborators' → unchecked Open questions)
  │    pr-open, ci_state failing                 → crew ci-fix: reads the head's checks again; once all have finished
  │                                                  and one failed, a round "CI failed on <sha>", once a head; past
  │                                                  MAX_CI_FIXES, an unchecked Open question instead
  │    working or addressing-feedback, its job   → crew followup --fresh --note "This task moved to <host>…"
  │      unknown here and no worktree here          (moved_here: a task that moved here; next tick its new job is listed)
  │    worker busy (mid-turn, or a prompt open)  → stop here, saying so if a round waits; the next tick tries again
  │    review_rounds > last_round_sent           → crew followup
  │    pr-open, needs a restack, parent doesn't, → crew restack
  │      and restack_sent isn't the target yet
  │    not needs-input, unchecked Open questions → crew set TASK status needs-input
  ├─ for each running goal:
  │    a note or a worker question it hasn't had → crew goal followup, once the goal lead is free (saying so if it isn't)
  │    every task merged or abandoned            → crew goal done
  ├─ dispatchReady: the ready tasks of running goals, every project's together,
  │    highest priority first, then by project and ID
  │                                              → crew dispatch, each in turn (exit 4, a dispatch that has to
  │                                                  wait: nothing said, and the next task is tried)
  └─ push_ledger in the foreground (soft): whatever a background push missed
```

The stand-down and the lease check are gates, not steps: they decide whether the tick does anything, so they run in the tick's own process rather than as `crew` commands of their own.

- **One rule for "waiting on you":** a task whose worker is idle and that has an unchecked item under Open questions is `needs-input`. `crew tick` only sets it. Whoever settles the last question sets the status back: the worker when it's resumed with the answer, the lead otherwise. Going back automatically would hide the questions of workers still running on a prompt from before questions were checkboxes.
- **Rounds wait for the end of the worker's turn.** Delivering one mid-turn would need `SendMessage`, which only a Claude session can call.
- **Only changed PRs are read.** `crew sync` records each PR's `updatedAt` as `pr_updated`, and `crew tick` runs `crew feedback` only when that is newer than `feedback_since`. A quiet PR then costs one API call per tick instead of four.
- **Nothing is sent twice:** `last_round_sent` for rounds, `restack_sent` for restacks. Red CI is recorded once for each head commit: the `CI failed on` and `CI is still red on` lines in the task file are the record (`Task.CIReported`), not a key.
- **CI is the worker's own business until it hands the PR over.** `crew ci-fix` acts only on a `pr-open` task whose PR is still open and whose head's checks have all finished, so a red build while the worker is still at it, or before a rerun ends, isn't sent. `ci_state` only spares a tick the call while CI isn't failing; `crew ci-fix` reads the checks again and decides.
- **Dispatch comes last.** A review round or a restack resumes an idle worker without checking `MAX_WORKERS`, and a merged task's cleanup can stop a worker that is still busy. Dispatching after every other step means `crew dispatch`, which counts busy workers afresh, sees what this tick has already done. Rounds and restacks still don't check `MAX_WORKERS` themselves, so one sent while every worker slot is taken puts the crew over it until that worker's turn ends.
- **A task's `priority` orders the dispatch** (a whole number, higher first, empty is 0; set with `crew set`). It's one order across every goal and project, because `MAX_WORKERS` counts busy workers across them all: a free worker slot goes to the highest-priority task that its caps and dependencies let through. A task they hold doesn't hold back the ones after it, since caps are per goal and dependencies per task. (A project can set a lower `MAX_WORKERS` of its own, which holds back its tasks while another project's go ahead.) A value written by hand that isn't a whole number counts as 0, with a warning.
- **Restacks need no word from the user any more.** `crew sync` still never restacks; `crew tick` does, bottom-up, only for a task whose PR is open and whose worker is idle. A restack force-pushes the task's branch.

### Goals
The user hands the crew a goal, a list of items or a column on the project's tracker, and a **goal lead** works through it without them. The interactive lead stays the user's interface to everything, goal leads included.

```
you ⇄ lead: what the goal is for, where its items come from, what bounds it
  lead: crew goal new "<title>" → the user's words into the goal file's Goal → your go-ahead
        crew goal start GOAL
          ├─ detached checkout ~/agent-work/<p>/_goal-<id> at origin/<default> (plain git, like the gardener's)
          └─ claude --name crew-<p>-goal-<id> --add-dir <home> --bg --agent goal-lead
               --permission-mode <LEAD_PERMISSION_MODE> --settings "<lead settings without agent, env: project and home>" "<prompt>"
  goal lead, for every item, in one turn: crew new … --goal GOAL → intake to the brief standard
        → plan-reviewer → findings settled → crew ready TASK, or parked (draft, with Open questions)
  crew tick: dispatches the goal's ready tasks, highest priority first, as MAX_WORKERS / the goal's max_in_flight allow;
             resumes the goal lead (crew goal followup) with the user's notes and its workers' questions;
             closes the goal (crew goal done) once every task is merged or abandoned
```

- **A goal is a ledger file**, `projects/<p>/goals/NNN-<slug>.md`, with flat front matter (`status`: `draft`, `running`, `done` or `abandoned`; the goal lead's `checkout`, `lead_name`, `lead_job` and `lead_session`; `unsent_notes`; `max_in_flight`) and the sections Goal (the user's words, quoted, including where the items come from), Items (each one with its task, written by the goal lead), Decisions and Log. Like a task file, it is the goal lead's record, and the session can be replaced.
- **A task belongs to a goal through its `goal:` key**, set by `crew new --goal`. `crew status` lists each goal with its tasks under it.
- **Parked is a task state, not a goal one.** The goal lead parks an item by leaving its task in `draft` with unchecked Open questions, so the rest of the goal carries on. The user answers through the lead, which records the answer in the task and passes it on with `crew goal note`.
- **The goal lead is told only what's new.** `crew goal note` records the user's words under the goal's Decisions and counts them in `unsent_notes`. A goal task's worker that asks something is found from its Open questions (not the `@login` lines `crew feedback` writes for collaborators); `questions_routed`, a checksum of the questions last sent, keeps one the goal lead left for the user from being sent again. `crew goal followup` sends both, once the goal lead is free, and resets them.
- **The goal lead works one turn per input** and never polls (its prompt, and `disallowedTools` without `ScheduleWakeup` or cron). Every turn starts in a freshly fetched checkout. It doesn't count toward `MAX_WORKERS`, which caps workers: what it produces is capped where it reaches the user, by the goal's `max_in_flight`.
- **Dispatches wait quietly.** `dispatchChecks` refuses with exit 4 (`notYet`) when a task only has to wait: a cap, an unmerged dependency, a stacked parent that hasn't pushed. `crew tick` prints nothing for those and tries again; any other refusal is a warning.
- **Ending a goal** (`crew goal done` or `abandon`) stops and removes the goal lead, any background process still running in its checkout, and the checkout, and leaves the tasks alone. A note to a done goal reopens it, for items added later. The ready tasks of an abandoned goal are the lead's to dispatch.
- **Items come from wherever the goal says.** If that's an external tracker, the goal lead reads it with the tools its session has and changes nothing there. How to reach a project's tracker belongs in that project's `AGENTS.md`, never in this repo.

### Gardener
`crew gardener [--all|--project P]`:
1. Fetches the default branch and moves `_gardener` to `origin/<default>`. This checkout always uses plain `git worktree`, whatever `WORKTREE_TOOL` says: the gardener only reads, so it doesn't need the project's per-worktree setup.
2. Sets the review range to `gardener.last..HEAD`. On the first run it starts from the commit before `GARDENER_LOOKBACK`.
3. Runs `claude -p --add-dir <home> --agent gardener --permission-mode acceptEdits --permission-prompts none --max-turns … --settings "<gardener settings, env: project and home>" "<prompt>"`, writing its output to `logs/`.
4. On success, updates `gardener.last` and commits. It exits non-zero if any project fails.

### Proposals
A proposal is an idea worth keeping that isn't ready to be a task: a gardener refactor, or something the user, a lead or a worker wants written down. `crew status` shows only how many are open, so they don't look like work in progress.

- **One shape for every source.** Each is a file from `templates/proposal.md`. Its `source` says who wrote it: `gardener`, `user`, `lead` or `worker:<task id>`. `revisit_when`, optional, is the condition that makes it worth acting on.
- **`crew propose "<title>" [--why TEXT] [--revisit-when TEXT] [--source S]`** writes one from a title and a paragraph (under Why it matters), with `source: lead` by default and the evidence sections left empty. A `worker:` source must name one task of the project; crew records its full ID.
- **`crew proposals [--any-status] [--all|--project P]`** lists the open ones, or all of them with `--any-status` (for P, the default project, or every project), with their source, and `revisit_when` on a line below. The lead runs it at intake and raises any whose title or `revisit_when` touches the task.
- **`crew promote`** turns an `open` or `deferred` proposal into a draft task. The task's Context names the proposal and who wrote it, its `revisit_when`, and whichever of Why it matters, Proposal and First step have text (`Proposal.TaskContext`).

### Project memory
A project's memory is `projects/<p>/memory.md`: durable facts about the project that the user approved and every agent working on it should know, mostly settled domain decisions that outlive their task and private working facts collaborators shouldn't see. It isn't status, work, the repo's conventions (AGENTS.md) or the user's taste (`~/.claude/CLAUDE.md`). Claude Code's auto memory can't do this job: it's per repository, so the lead and plan-reviewer, which run outside every repo, never see it, and nobody reviews what goes in.

```
any agent: crew remember "<fact>" --why "<reason>" --source S
  → memory-suggestions.md: - [ ] **<fact>** <reason>. (<source>, <date>)   crew status counts it
lead ⇄ user: keep it? → the text after the checkbox, unchanged, into memory.md;
             the item ticked "→ kept" or "→ declined: <reason>"
session start, resume, clear, compaction: SessionStart hook → crew memory --hook
             → the memory of CREW_PROJECT, into the session's context
```

- **Agents suggest, the user approves.** Only the lead writes `memory.md`, on the user's say-so; `crew guard` blocks the worker and gardener roles from editing it. Everyone else suggests with `crew remember`, whose `--source` is checked by `ledgerSource`, as for proposals.
- **Every agent loads the whole file**, mechanically. A SessionStart hook with no matcher, in `lead/.claude/settings.json` (so goal leads too), `config/worker-settings.json` and `config/gardener-settings.json`, runs `crew memory --hook`, and Claude Code adds its stdout to the session's context. It runs again at every resume, clear, fork and compaction, so a session gets the memory as it is at that moment; a change made since reaches it at the next one. Launch-prompt lines would load only if the agent obeyed, and `--append-system-prompt-file` would keep a resumed session on its old copy.
- **The hook prints the memory of `CREW_PROJECT`**, after a line naming the file and the rule for weighing it (`memoryRule`, the only place that rule is written). It prints nothing for no project or `all`, so a lead started for every project, and plan-reviewer (a subagent gets no SessionStart), run `crew memory --project <p>` instead, as their prompts say. It always exits 0.
- **casual-observer gets none**, on purpose: its value is the read of a teammate who knows little. `crew observe` runs it with `readonly-settings.json`, which has no hooks, and without `CREW_PROJECT`.
- **The whole file is loaded**, and Claude Code passes on at most 10,000 characters of a hook's output, so the lead keeps `memory.md` under 9,000. Past the cap, Claude Code would replace the output with a 2,000-character preview and a file path, without asking the agent to read it, so `crew memory --hook` prints its header and an instruction to read the file instead (`hookOutputCap`). Loading an index and fetching entries on demand is the next step once a memory nears that.

### MCP servers for one project
A project's MCP servers, a tracker say, go in `projects/<p>/mcp.json`, the same shape as a repo's `.mcp.json`. A repo's `.mcp.json` doesn't fit: collaborators see it, and the lead, which doesn't run in the repo, never reads it. User scope doesn't either: it reaches every session in every project.

```
projects/<p>/mcp.json  (committed; a secret only as ${NAME}, a setting from the project's settings files, local.env for a secret)
  → projects/<p>/mcp.local.json   each server named <p>_<server>, ${NAME}s filled in
       → workers and goal leads of <p>: claude --mcp-config <it>   (rebuilt before every launch, resumes too)
  → lead/.mcp.json                every project's mcp.local.json servers
       → every lead, as the servers of its folder, approved by enableAllProjectMcpServers
       → a lead for Q: --settings {"disabledMcpjsonServers": [every server not in Q's mcp.local.json]}
```

- **crew fills in the values,** not Claude Code, so the rule is the same for every kind of session and no secret goes in a session's environment or on a command line. A value comes only from crew's settings, not the environment, so a worker the timer dispatches gets what one dispatched from your shell gets, as long as `local.env` holds the value itself: a `$VAR` in a settings file is still read from the environment.
- **Names are `<project>_<server>`** in both generated files. A project's name is `[a-z0-9-]`, so names can't clash between projects. A repo's own `.mcp.json` could still use such a name, which the prefix makes unlikely but doesn't prevent. One permission rule or `AGENTS.md` line works in every kind of session.
- **A resume takes no flags,** so a resumed worker keeps the `--mcp-config` path it started with. The launchers rebuild the file first, so it's current. Every worker and goal lead starts with the path, whether or not its project has an `mcp.json` yet, so one added later reaches sessions already running. Generated files are never deleted: a project that drops its `mcp.json` gets one with no servers, which a resumed session can still load.
- **A lead for one project switches the others off** by exact name, from its own project's `mcp.local.json`, never by parsing names. It reads `lead/.mcp.json` as it is on disk, the file the session loads, so a server the last sync left there is switched off too. `claude remote-control` takes no `--mcp-config`, which is why leads get their servers from a file in `lead/`. The fallback, if `disabledMcpjsonServers` doesn't work as assumed, is a lead folder per project.
- **Keeping it current:** `crew tick` runs `crew mcp sync`. `crew install`, `crew lead` and `crew serve` call the same code, the lead commands before they start. Building one project's file, as a dispatch does, never reads another project's.
- **A broken `mcp.json`** stops its project's dispatches and followups before anything starts, with an error naming the file and the setting, and `crew doctor` reports it. `crew mcp sync` reports it and leaves that project's `mcp.local.json` as it was.

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
4. If `$WORK_ROOT` isn't trusted yet, open Claude Code there for the user to accept the trust dialog (`trustWorkRoot`), since workers' background sessions can't show it. crew catches Ctrl+C and hangup until `claude` exits, so leaving it any way goes on to step 5. Without a terminal, `crew doctor` says how.
5. Enable the services (`crewServices`: `crew-tick.timer`, `crew-gardener.timer` and `crew-lead.service`, or on a Mac the jobs `crew-tick`, `crew-gardener` and `crew-lead`), and run `crew doctor`.

**Holding it:** every change takes `lock`, which runs `requireHost`: `host` must name this machine, and `.crew-lease-lost` must not exist. Every ledger commit starts a background push (`pushSoon`).

**Losing it** (another machine ran `crew host up --take`):
1. This machine's next background push is rejected, finds origin's `host` names the other machine, and creates `.crew-lease-lost`. From then on this machine refuses every change.
2. Its next `crew tick` fetches, sees the same, and stands down: its `crew-*` sessions stopped, what origin lacks kept on a branch `lost/<host>-<time>`, the home reset to origin, and its services off. That removes `.crew-lease-lost`, as a later `crew host up` would. The services go off last, so a stand-down that fails on the way leaves the tick on, and the next tick tries again.

**Carrying on, on the new machine:** its `crew tick` gives a working task whose worker and worktree aren't there a fresh worker (`movedHere`, `crew followup --fresh`). `crew followup` and `crew restack` recreate a missing worktree from `origin/<branch>`, or start over from `origin/<base>` when the branch was never pushed.

**Handing over on purpose** (`crew host down [--force]`): refused while a task worktree has unpushed commits; then the services off, sessions stopped, the ledger pushed, and an empty `host` pushed. `crew host up` on the next machine needs no `--take`.


- **Losing a machine loses little:** the ledger is pushed seconds after each change, and workers push their branch after every commit. Session transcripts stay on the old machine, which is why a moved task gets a fresh worker: principle 3 already lets any worker be replaced.
- **The phone follows the lease:** `crew-lead.service` runs only where `crew serve` finds the lease (it exits 3 elsewhere), so Claude Mobile's `crew` is always the machine that runs the crew. On a Mac, launchd can't leave the job stopped on exit 3, so it's started again every 30 seconds, exiting 3 at once, until the next tick there stands the machine down and boots it out.

### Upgrading
```
crew upgrade   lock (the host only) → refuse a dirty home
               → the newest v<version> tag upstream has (git ls-remote --tags upstream)
               → CREW_VERSION=<version> bin/crew version: the launcher downloads that release's binary;
                 if it can't (the release may still be building), refuse, with nothing changed
               → git fetch upstream tag v<version> → git merge --no-edit v<version>
                 a conflict (only the person's own edits to crew's files can conflict) → stop, merge left in progress
               → the new version's bin/crew install → push → restartLeadService (systemctl restart, launchctl kickstart -k)
               → remove the other versions' binaries from bin/release/
```

A home moves only from release to release, so its agent prompts and its binary always come from the same one. Releases are made in the public repo: pushing the tag `v<version>` (it must match `VERSION`) runs `.github/workflows/release.yml`, which runs the tests, and `go test` on `macos-latest`, builds `crew-Linux-x86_64`, `crew-Linux-aarch64`, `crew-Darwin-arm64` and `crew-Darwin-x86_64` with the version stamped in, and publishes them as the tag's release only once both pass, titled `crew <version>`, the tag's message as its notes. It refuses a lightweight tag, for which `gh` would publish the commit's message instead. The workflow runs only in the public repo, so a private home that pushes a tag builds nothing.

## `crew` internals (`cmd/crew/`)

Go, standard library only, in one package. Each area has a file of its own; each command is a `cmd…` function in a `cmd_*.go` file.

- **The launcher (`bin/crew`):** a POSIX `sh` script, the only `crew` on `PATH`. It finds itself through symlinks, reads `VERSION` from its own checkout (or takes `CREW_VERSION` from the environment), and runs `bin/release/crew-<version>` with `CREW_HOME` set to that checkout, unless it's set already.
  - A missing binary is downloaded once with `curl` from the public repo's release `v<version>` (`crew-<uname -s>-<uname -m>`), to a temporary file that is run with `version` to check it, then renamed into place. `bin/release/` is gitignored, so a fresh clone works on a machine without Go.
  - When it can't get a binary, it says why and exits 1, or 2 when called as `crew guard`, so a missing binary blocks edits.
  - The release URL is written into it; a fork changes that one line.
- **Home and config (`home.go`, `env.go`):**
  - `CREW_HOME`, exported, so the hooks, `wt` runs and foreground sessions crew starts see it. A background session gets it from its settings (`sessionSettings`).
  - The settings are `config/defaults.env`, which sets every key, then `config/crew.env` if it exists. `projectSettings` adds `projects/<p>/project.env` (committed: `REMOTE`, `DEFAULT_BRANCH`, overrides), then `projects/<p>/local.env` (this machine's `REPO`, gitignored), starting from the shared settings for each project, so one project's values never reach the next. `loadProject` builds on it, and fails when this machine has no clone; `crew mcp sync` needs no clone, so it reads only the settings.
  - The settings files are shell-style but parsed, not sourced. A line is blank, a `#` comment, or `KEY=value`, where the value is one shell word: plain, `'single-quoted'` and `"double-quoted"` parts, with `$VAR` and `${VAR}` taken from the settings read so far or the environment, then an optional `# comment`. Anything else, a command substitution or an unset variable say, is an error naming the file and line.
  - `crew config [KEY] [--project P]` prints the effective value, for P or the default project.
- **Project scope (`home.go`):** a command works on the project `--project` names, else the default project (`defaultProject`): `CREW_PROJECT` when it's set, else the project whose repo the current directory is in. `--project all`, `--all` or `CREW_PROJECT=all` is every project, and so is no project at all for the commands that list (`scope`). Commands that write to one project fail without one (`oneProject`).
  - `CREW_PROJECT` wins over the current directory because a session's directory is only wherever its shell last went: a lead reading code in `_lead` is still the lead it was started as.
  - Every session crew starts has it: a worker, goal lead or gardener has its project in its `--settings` env (`sessionSettings`, below), and the Remote Control lead has `all` from `crew serve`'s environment. `crew lead` sets it to `--project P`, the project whose repo you ran it in, or `all`. So the current directory scopes only your own shell.
- **Errors (`main.go`):** a command returns its refusal as an error: `fail` (exit 1), `notYet` (exit 4) or `exitQuietly(code)` for one that has said what it had to (`crew feedback`'s and `crew ci-fix`'s 3 for "nothing new"). `main` prints the message as `crew: …`. `warn` reports what doesn't stop the command.
- **Locks (`lock.go`):** `flock(2)` on files in the home, the same lock util-linux `flock` takes. `lock` takes `.crew.lock` (30 s timeout) for the rest of the process, or until `unlock`, and then checks the lease (`requireHost`). `takeLock` is the lock alone, for the commands that move the lease. `.crew-tick.lock` lets one `crew tick` run at a time, and `.crew-push.lock` one ledger push. Go opens files close-on-exec, so the sessions and processes crew starts never hold a lock. `crew dispatch` unlocks while it creates the worktree, since Worktrunk hooks can take minutes and workers' `crew log` calls would otherwise time out; it repeats its checks once it has the lock again.
- **Codex (`cmd_codex.go`):** `crew codex` is the only way crew runs Codex, for `pr-review`, in a new session each time: a rebuttal round starts Codex fresh with a round file, as it does the Claude reviewer, so there's no thread to keep. Every call passes `sandbox_mode="read-only"` and `approval_policy="never"`, fixed in code. The model and effort come from `CODEX_MODEL` and `CODEX_MODEL_EFFORT`, for the default project, so a new model needs no release. It runs Codex in the review root without `CLAUDECODE`, with the prompt file on stdin and the event stream in `<out>.events.jsonl`, and stops Codex, exiting 124, after `codexStallLimit` (15 minutes) without an event. Workers can't go around it: `worker-settings.json` denies `Bash(codex *)`. `crew doctor` requires `codex` installed and signed in (`codexSignedIn`) for each project with `CODEX_REVIEW=yes`, and on the `PATH` of each enabled service, since a worker's Bash may have the daemon's `PATH` from a service.
- **CI (`cmd_ci.go`):** all of crew's CI handling: `rollup` (a head's checks → its state and the checks that failed), `crew ci-fix` and `crew ci-summary`. `crew sync` only calls `rollup`, for `ci_state`, and `crew tick` reaches CI only through a `crew ci-fix` step. What was reported is read from the task file's CI lines (`Task.CIFailures`, `Task.CIReported`), so no key counts or records it.
- **The lead (`cmd_lead.go`):** `crew lead`, which `bin/lead` runs, builds a lead's `claude` call: `--name lead` or `lead-<p>`, `--permission-mode` from `LEAD_PERMISSION_MODE` (the project's, for one project), `--settings` from `leadMCPSettings` for one project, then the user's own arguments in order, with `--project P` taken out (`takeProject`). It syncs the MCP servers before it builds `--settings`, which go by `lead/.mcp.json` as it is on disk. `execLead`, which `crew serve` uses too, changes into `lead/` and replaces crew with `claude` (`execClaude`).
- **Running claude (`sessions.go`):** `claude` runs it with `$CREW_HOME/bin` first on `PATH` and `CLAUDECODE` unset, so it can be launched from inside a Claude session, and without the caller's `CREW_PROJECT`, which the caller sets for its own session (see Project scope). That environment reaches a session in the foreground, like `crew observe`'s. A background session gets Claude Code's daemon's environment instead (see "Environment" under Assumptions to re-check).
  - `execClaude` is the one place crew hands the terminal to `claude`: it replaces crew's process, so signals go straight to `claude` and crew holds no lock. `execLead` and `crew attach TASK` use it. `crew attach` runs `claude attach <worker_job>` from the current directory, for TASK as `crew get` resolves it. It refuses inside a Claude Code session (`CLAUDECODE` set), whose Bash has no terminal to hand over, so a lead that follows a hint gets a plain error. A resume of that worker stops its idle process first (`stopIdleSession`), and that ends the attach ("Attaching to a worker" under Assumptions to re-check).
- **What crew gives a session (`sessionSettings`):** every session crew starts for a project (workers, goal leads and the gardener) gets `CREW_PROJECT` and `CREW_HOME` in the `env` of its `--settings`, never through the environment crew launches it with. For a background session that's the only way that works, since the daemon serves every project's sessions at once. The gardener runs in the foreground and would get crew's environment, but it follows the same rule so that there's one. Each one's `--settings` is a JSON string, set last, so a project's `worker-settings.json` can't change them. `crew serve` and `crew lead` set `CREW_PROJECT` in their environment instead, as `remote-control` takes no `--settings`, and `crew observe` gets no project.
- **Ledger commits (`push.go`):** `commit` adds and commits the state paths (`statePaths`: `projects/`, `reports/`) and nothing else in the home, but only if `$CREW_HOME` is its own repo toplevel. It never fails the command. After a commit it calls `pushSoon`.
- **MCP servers (`cmd_mcp.go`):** everything about a project's `mcp.json`: reading it (`readMCPFile`), naming and filling in its servers (`projectMCPServers`, `expandSettings`, `expandString`), writing `mcp.local.json` and `lead/.mcp.json` (`writeMCPFile`: a temporary file renamed into place, mode 0600), `crew mcp sync` (`syncMCP`, which tick, install, `crew lead` and serve use too), the settings that switch a project lead's other servers off (`leadMCPSettings`) and the flags the launchers add (`refreshMCPConfig`). Other files only call it.
- **Project memory (`cmd_memory.go`):** `crew remember` appends a suggestion to `memory-suggestions.md` under the ledger lock (`memorySuggestion`), and `crew memory` prints `memory.md` after its header and `memoryRule`. `crew memory --hook` takes the project from `CREW_PROJECT` (`sessionMemory`), and for a memory longer than Claude Code passes on (`hookOutputCap`) prints only the header and an instruction to read the file. `memorySuggestions` counts the unchecked items for `crew status`. A hook never fails: `main` exits 0 for any command run with `--hook` first, whatever went wrong.
- **Agents' own edits:** `crew commit --hook` is a PostToolUse hook on Edit, Write, NotebookEdit and Bash, in `lead/.claude/settings.json` and `config/worker-settings.json`. It ignores the hook JSON and asks git what changed under the state paths (`ledgerChanges`), since agents often edit the ledger with a script run through Bash. It commits any change as `edit <path>`, or `edit <n> files`, which pushes it. A call that changed nothing in the ledger costs one `git status` (about 4 ms on a home with a few hundred task files). It always exits 0, so it never fails the tool call.
- **Public and private repos:** `cmdSetup` and `cmdUpgrade` (see [Public and private repos](#public-and-private-repos) and [Upgrading](#upgrading)). `crew setup` refuses when `config/crew.env` exists, when there's no `origin`, or when `gh` isn't signed in. `crew upgrade` never resolves a conflict itself.
- **Lease (`lease.go`, `cmd_host.go`):** one machine runs the crew at a time, the one the home's `host` file names (`CREW_HOST`, from the environment, default `machineName()`: the short hostname on Linux, `scutil --get LocalHostName` on a Mac, since a Mac's hostname can change with the network).
  - `requireHost` fails unless `host` names this machine and `.crew-lease-lost` doesn't exist, with a message naming the holder. `lock` runs it after taking the flock, so every mutation is refused elsewhere, and `crew dispatch`, `crew gardener` and `crew tick` run it before they start a session. Reads work anywhere, and `crew status` on another machine pulls first and says it's read-only.
  - `crew host up` claims the lease by a push (`commitHost`), so a rejected push means another machine moved first: it resets and looks again. `--take` claims it from another machine. It also runs `crew install`, clones each project this machine has no `local.env` for into `$WORK_ROOT/<p>/repo`, has the user trust `$WORK_ROOT` (`trustWorkRoot`), enables `crewServices` and runs `crew doctor`.
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

  It searches the default project first (see Project scope), then all projects, so a task number or ID two projects share resolves in a scoped session. Every project numbers its own tasks, so even an exact ID can match in two. On ambiguity it prints the candidates, each with its project, and fails. `taskArg` names a task for a command to paste, as `crew attach`'s hints do: by its ID when that resolves back to it from here, else by its path.
- **Sessions (`sessions.go`):**
  - `agents` returns what `claude agents --json --all` lists as `Agent`s, or nothing on failure. It reads each field on its own, so a field whose type changes loses only that field. It sets `TurnEnded` on a live `working` session whose transcript (`~/.claude/projects/*/<session>.jsonl`) ends with the `turn_duration` entry Claude Code writes when a turn ends (`turnEnded`).
  - `busy` counts a job as busy while a turn runs (`state` working) or while its live process holds an open prompt, such as a permission request (`status` waiting). A `blocked` job without an open prompt ended its turn on a question for the user, so it takes a followup: the answer is what the followup sends. A background shell the worker started, such as a dev server, keeps its `state` working and its `status` busy after its turn ends, so a `working` job counts as mid-turn only until it has `TurnEnded`. `jobState` shows such a job as "idle, background task running". `runningWorkers` and `stopIdleSession` use the same rule.
  - `runningWorkers` counts the busy sessions whose job a task file records as `worker_job`, across all projects. It doesn't go by name: a session Claude Code copies on resume gets a name of its own.
  - `stopJob` runs `claude stop` and waits up to 10 seconds for the job's `pid` to clear. `stopIdleSession` uses it on a recorded session that isn't busy but whose process is still open, before a resume.
  - `removeSession` stops a listed job whose process is open, busy or not, then runs `claude rm` on it. It fails only if the process won't close.
  - `launchSession` starts or resumes the background session a ledger file records (`<role>_name`, `<role>_session`) and confirms it started (`waitForLaunch`). It strips color codes from `claude`'s output before reading the job ID, and fails rather than record an ID that doesn't look like one. A resume passes only the session and the prompt. `relaunch` falls back to a fresh session when a resume didn't start one (`errNotStarted`).
  - `launchWorker` is the only place a worker command line is built, on top of `launchSession`. A fresh worker gets `--agent`, `--permission-mode`, `--add-dir`, `--autocompact` (`WORKER_AUTOCOMPACT_WINDOW`, 400k by default, so a worker carried through several review rounds compacts long before its 1M window fills) and `workerSettings`: `config/worker-settings.json`, merged with `projects/<p>/worker-settings.json` when it exists, and with the session's env (`sessionSettings`), as a JSON string. The merge is add-only (`addOnly` in `json.go`): objects merge, arrays append new entries, and any value the base sets wins, so a project can add `allow` rules or `env` but can't drop a `deny` rule.
  - `launchWorker` and `launchGoalLead` both start with `refreshMCPConfig`, before a fresh start and a resume alike: it rebuilds the project's `mcp.local.json`, and gives every fresh session `--mcp-config <file>`, empty while the project has no `mcp.json`, so servers added later reach it on its next resume. `--mcp-config` is variadic, so it goes first, never right before the prompt.
- **JSON (`json.go`):** the settings crew writes keep their keys in the order their files have them, as jq's output did: the user's `~/.claude/settings.json`, merged worker settings, and a goal lead's settings.
- **Stacks (`stacks.go`):** `Branches` indexes a project's task files by branch, and `parentOf`, `effectiveBase`, `restackReason` and `noteBaseSHA` build on it (see [Stacked tasks](#stacked-tasks)). They read git only through `REPO`, whose refs the task worktrees share, and never touch its working tree.
- **Goals (`cmd_goal.go`):** `goalTasks` finds a goal's tasks by their `goal` key; `goalFinished`, `workerQuestions` and `unroutedQuestions` build on it. `questions_routed` is a POSIX `cksum` of the questions (`cksum`), so ledgers the bash crew wrote keep working. `launchGoalLead` is the only place a goal lead command line is built, on top of `launchSession`, and `relaunch` gives it the same resume-or-fresh fallback as `crew followup`. `retireGoal` ends a goal for `crew goal done` and `abandon`.
- **Worktrees (`worktree.go`):** `worktreeCreate` and `worktreeRemove` are the only places task worktrees are made or removed, for both `WORKTREE_TOOL` values. `setupWorktree` wraps `worktreeCreate` for a task: it creates the worktree unless it exists (on the task's local branch, or a new one from `origin/<base>`), refuses one that a failed Worktrunk dispatch left behind, and copies the `WORKTREE_COPY` files. Its callers run it without the ledger lock, since Worktrunk hooks can take minutes. A missing branch comes from `origin/<branch>` when another machine pushed it. `ensureWorktree` is how `crew followup` and `crew restack` find a task's worktree on this machine: it makes it at `$WORK_ROOT/<p>/<id>` if it's missing (on a machine the task moved to, say), rewrites `worktree:` when the recorded path differs, and logs "recreated worktree from origin/<branch>", or "started over: … was never pushed from <machine>" when the branch is nowhere and starts again from its base. `crew observe` and `crew cleanup` treat a missing worktree as nothing to do. With `worktrunk`, crew never passes `--yes`: hooks you haven't approved make `wt` fail non-interactively, and dispatch stops before a worker starts. It checks that the JSON `path` `wt` returns is the expected one. If a hook fails after `wt` has made the worktree, the task never records it, and the next dispatch refuses that leftover directory instead of starting a worker in a half-set-up worktree.
- **Services (`services_linux.go`, `services_darwin.go`):** the only places crew runs `systemctl` or `launchctl`. Each file defines the same functions, and build tags pick one, so callers never ask which system they're on. `crewServices` names the units or jobs; `enableServices` and `disableServices` turn them on and off now (`crew host up`, `crew host down`, the stand-down), `serviceEnabled` and `servicePath` are what `crew install` and `crew doctor` read, `restartLeadService` is `crew upgrade`'s restart, and `installServices` is `crew install`'s step (manifest component `systemd`, or `launchd`). What they share, so it's tested on Linux, is in `services.go`: `renderUnit`, `renderPlist` and `readPlist`, a reader for the plists crew ships.
  - On a Mac, turning a job on is `launchctl enable gui/<uid>/<label>`, then `bootstrap` (a job already loaded counts as done, and it's retried for a few seconds, since launchd can refuse it just after a bootout); off is `disable`, so it stays off after a login, then `bootout`. "Enabled" means loaded: `launchctl print` succeeds. A changed plist of a loaded job is booted out and bootstrapped again: launchd never rereads one. `restartLeadService` is `launchctl kickstart -k`.
  - Booting out a job can end crew itself, when crew runs in that job: the tick's stand-down runs in `crew-tick`, and a phone lead's `crew upgrade` in `crew-lead`. So for each job the bootout is the last thing crew asks of launchd. `crewServices` lists `crew-tick` last, and the stand-down turns the services off as its last step. A reload's bootout and bootstrap run in a shell in a session of its own, which loads the job again even if crew has ended.
- **This machine (`machine_linux.go`, `machine_darwin.go`):** `machineName` is its name for the lease, and `installTools` how `crew install` suggests getting the tools it lacks (pacman, or Homebrew).
- **Processes (`proc.go`):** `stopProcessesIn` ends the processes left in a worktree or checkout, for `crew cleanup` and `crew goal done`. `processesToStop` chooses them, the same way on both systems: those whose working directory is in it, without a terminal, other than crew and the processes that started it. `processes` lists them per system: from `/proc` on Linux (`proc_linux.go`), and on a Mac (`proc_darwin.go`) from `/bin/ps -A -o pid=,ppid=,tty=,stat=` and `/usr/sbin/lsof -nP -a -d cwd -F pn`, named by their full paths since a launchd job's `PATH` may lack `/usr/sbin`. The parsing of all three is in `proc.go`, so it's tested on Linux; the `ps` and `lsof` samples in `proc_test.go` are written from their man pages. When the processes can't be listed, it warns and leaves them running.
- **Commands:** one `cmd…` function each, listed in `commands` in `main.go`. `version` and `guard` run before the home is read: the launcher checks a download with `version`, and the guard must work whatever the home's settings say.

## The write guard (`crew guard`)

It's called from the agents' frontmatter hooks, and from the hooks in `lead/.claude/settings.json` and `config/worker-settings.json`, as `"${CREW_HOME:-$HOME/crew}/bin/crew-guard" <role>` on Edit, Write and NotebookEdit. `bin/crew-guard` is a two-line `sh` file that runs `crew guard <role>`: it stays, so sessions started before an upgrade keep their hook (a hook command that's gone would count as a non-blocking error, and allow their edits). The guard reads the hook JSON on stdin (`.cwd`, `.tool_input.file_path`). Exit 0 allows the call; exit 2 blocks it, and stderr goes back to the agent. It blocks whenever it can't work out where a path leads: input that isn't JSON, a symlink loop. Paths are judged by where they lead, symlinks resolved (`canonical`, which works like `realpath -m`).

- In the crew home, only the state paths, `projects/` and `reports/`, take edits, for every role. The rest of the home (`bin/`, `agents/`, `config/`, `templates/`, `lead/`, `.claude/`, `.git/`, the root files) is crew itself, and is blocked.
- Always allowed elsewhere: `/tmp`/`$TMPDIR`, `~/.claude/jobs`, `~/.claude/plans`, `$CLAUDE_JOB_DIR`.
- `worker`: only paths inside the git toplevel of `$CLAUDE_PROJECT_DIR` (falling back to `cwd`), and never when that toplevel is the main checkout.
- `lead` (the lead and goal leads), `gardener`: nothing else.
- A project's `memory.md` takes edits only from the `lead` role: `worker` and `gardener` are told to suggest the fact with `crew remember` instead. It's a tripwire, like the rest of the guard: a goal lead shares the lead's role, so only its prompt keeps it out, and a Bash write isn't checked.
- A project's `mcp.json` and `mcp.local.json` take edits from no role. Every lead starts their servers without asking, so they're the user's to change by hand. A goal lead shares the lead's role, which is why the lead is blocked too.

The target path is structured input, so this needs no shell parsing. Bash commands aren't checked: shell parsing can never be complete.

On a Mac's usual case-insensitive APFS volume, `~/Crew/projects/…` is the home `~/crew`'s ledger, but the guard compares paths as spelled, so it blocks that edit. That errs only one way: it never allows an edit it should block. Paths aren't lowercased to "fix" it, since a case-sensitive volume would then be judged wrongly. The one exception errs the same way: a project's `memory.md`, `mcp.json` and `mcp.local.json` are matched ignoring case, so `Memory.md` is blocked too, on any volume.

Why the hook is in two places:

- The lead and the implementer have no `tools:` allowlist, only `disallowedTools`. A `tools:` list can name MCP servers only one by one, so a shared allowlist would hide every repo's servers or leak one repo's into all of them. Without it, each session gets the MCP servers configured for its repo, and those its project declares in `mcp.json`.
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
- **The gardener and `crew observe`** run with `--permission-prompts none` and a read-only `allow` list, so any other command is denied: `crew observe` with `readonly-settings.json`, and the gardener with `gardener-settings.json`, the same list plus `crew remember`.
- **The lead** runs in `LEAD_PERMISSION_MODE` (`auto` by default) in the home's `lead/`, and **goal leads** in the same mode in their own checkouts. `crew guard` keeps their file edits to the ledger.
- **An allow rule's `*` goes after the subcommand**, never before it. `Bash(git -C * show *)` would also approve `git -C . -c core.pager=<program> show`, and Claude Code warns about such a rule at every start. `tests/guard.sh` checks the settings files for it.
- **Network is left open.** `WebFetch(domain:*)` pre-allows every host, so background sessions never stop at a network prompt.

## `crew install`

Order: check dependencies and the Claude Code version (≥ 2.1.281) → agents → `env.CREW_HOME`, `WORK_ROOT` and the lead's `settings.local.json` → links → fish completions → services (systemd, or launchd) → manifest → the optional AGENTS.md setting → next steps. It only puts files where Claude Code, your `PATH`, fish and systemd or launchd look for them, and writes the lead's gitignored `lead/.claude/settings.local.json` for this machine; it changes nothing tracked in the home.

- **The home** is `CREW_HOME`, else the checkout of the `bin/crew` that ran (symlinks resolved), so `~/crew/bin/crew install` works straight after a clone.
- **What gets installed** is discovered, not listed: every `agents/*.md`, every file under `fish/` (the completions), and every `systemd/*` (or on a Mac every `launchd/*.plist`). Adding a file here is enough to ship it. The links, `crew` and `lead`, are the exception: `crew-guard` in the same `bin/` is for hooks, not for `PATH`.
- **Links** (`link`, component `links`): `~/.local/bin/crew` and `~/.local/bin/lead` point at the home's `bin/crew` and `bin/lead`, and the launcher finds its home through them, so nothing needs `CREW_HOME` in a shell. A link that points there already is left alone. Anything else at that path, a file or a link to another home, is backed up and replaced by rename. When `~/.local/bin` isn't on the `PATH` install runs with, it says what to add; it never edits a shell's config. `crew doctor` checks the links as a required check, and `~/.local/bin` on its `PATH` as an optional one, since that may not be your shell's.
- **Files are replaced by rename** (`installContent`: a temp file in the same directory, then a rename), never rewritten in place, so a running guard hook never reads a half-written file. A file that differs is backed up first.
- **A home other than `~/crew`** needs nothing in the links, which name the home itself. It's substituted into the systemd services (`renderUnit`) *before* comparing, so an unchanged re-run takes no backups. On a Mac every plist is rendered (`renderPlist`): launchd expands nothing, so `%h/crew` becomes the home and every other `%h` the user's home, XML-escaped. It also makes the home's `logs/`, which launchd needs before it starts a job.
- **`~/.claude/settings.json`** is rewritten, after a backup, only when a value crew sets there changes. It keeps its keys in their order (`json.go`).
- **Stale files** (`prune`): `~/.local/share/agent-crew/manifest` records `component<TAB>path` for every shipped file. After installing a component (`agents`, `links`, `fish`, `systemd` or `launchd`), files the previous install of that component shipped and this one doesn't are backed up and removed (services are turned off first). A symlink is backed up as a link, never as the file it points at. This is how the fish `lead` function and `conf.d` snippet that installs before 0.14 shipped are removed. Components skipped with `--no-fish`/`--no-services` keep their manifest entries and lose nothing, and only paths under the component's current root are touched.
- **The AGENTS.md question** is asked only if the setting is off and the user hasn't declined before (`~/.local/share/agent-crew/agents-md-declined`).
- **Re-running** is idempotent: a second run with no changes in the home backs nothing up. Backups go to `~/.local/share/agent-crew/backups/<timestamp>/`.
- **Services** are installed but not enabled: `crew host up` enables them. Install names the ones that are off on this machine. On a Mac, a plist installed in `~/Library/LaunchAgents/` would load at the next login, so the shipped ones say `Disabled`; `launchctl enable` overrides that.

Flags: `--yes`, `--no-fish`, `--no-services` (the systemd units, or the launchd jobs), `--skip-agents-md-setting`.

## Contracts between components

Changing any of these means updating every side listed.

| Contract | Producer → consumer |
|---|---|
| What README.md's pitch claims (What's different, Limits) rests on the design principles CLAUDE.md's "What crew is for" names, the requirements in SETUP.md §1 (its platforms) and SETUP.md's "Several at once" (its leads) | `ARCHITECTURE.md` Design principles, `SETUP.md` §1 Requirements and "Several at once" → `README.md`, `CLAUDE.md` "What crew is for" |
| `worker_job` in task files is the job ID `claude agents` lists, and the one `claude attach` takes | `launchWorker` → `runningWorkers` (the concurrency cap), `Agents.jobState`, `Agents.jobBusy`, `cmdAttach` |
| `claude --bg` prints `backgrounded · <job> · …`, the job possibly in color, and a `note:` line when it starts a copy | Claude Code → `launchSession` (color codes stripped, the job is the line's third field) |
| `claude agents --json --all` entries have `id`, `state`, `pid`, `status`, `waitingFor`, `sessionId`, `name` | Claude Code (research preview) → `agents`, `Agents.jobState`, `Agents.jobBusy`, `runningWorkers`, `stopIdleSession`, `waitForLaunch` |
| A session's transcript is `~/.claude/projects/<dir>/<session>.jsonl` (under `CLAUDE_CONFIG_DIR` when set), one JSON entry per line; a turn ends with a `system` entry whose `subtype` is `turn_duration`, and the next one starts with a `user` entry | Claude Code (not documented) → `turnEnded` |
| A job's ID is the first 8 characters of its session ID. A job with a `pid` reports the session its process has loaded. A resume whose session can't be found ends in `failed` | Claude Code → `waitForLaunch` |
| `claude --resume <session> --bg` continues the session in place only when given no other launch flags and the session's process has exited | Claude Code → `launchSession` (resume), `stopIdleSession` |
| `claude stop <job>` ends the job's process (its `pid` clears); background shells it started keep running for its next process. `claude rm <job>` takes it out of `claude agents` and leaves a worktree the session was started in | Claude Code → `stopJob`, `removeSession` (`crew cleanup`) |
| `--add-dir` is variadic, so it's never placed right before the prompt | `launchSession`, `crew gardener` |
| `REPO` is per machine: `projects/<p>/local.env`, gitignored. `project.env` holds what every machine shares (`REMOTE`, `DEFAULT_BRANCH`, overrides) | `crew project add` → `loadProject`, `crew doctor`, `.gitignore` |
| `host` at the home's root holds one line, the `CREW_HOST` of the machine that runs the crew, or nothing; it counts once origin has it | `crew host up`/`down` (`commitHost`) → `requireHost`, `originHolder` (`pushLedger`, the tick's stand-down), `crew status` |
| `crew host up` enables and `down`, `prune` and the stand-down disable `crewServices`; on a Mac, a job's label is its plist's file name | `crew host up`, `crew host down`, `standDown` ↔ `systemd/`, `launchd/` |
| `crew serve` exits 3 on a machine that doesn't hold the lease; the unit doesn't restart on 3. launchd has no counterpart: `launchd/crew-lead.plist` restarts it every 30 seconds (`ThrottleInterval`) until the stand-down boots the job out, which is why the job is enabled only on the lease holder | `crew serve` → `systemd/crew-lead.service` (`RestartPreventExitStatus=3`); checked in `tests/guard.sh` |
| Front-matter key names | `templates/task.md`, `templates/goal.md`, `templates/proposal.md`, `crew`, the agent prompts (`crew set TASK status pr-open`, …), `LEDGER.md` |
| A proposal's or memory suggestion's `source` is `gardener`, `user`, `lead` or `worker:<task id>` | `crew propose` and `crew remember` (`ledgerSource`), the gardener → `Proposal.Author` (`crew promote`), `crew proposals`, `lead.md`, `implementer.md`, `gardener.md`, `LEDGER.md` |
| Proposal section headings (`## Why it matters`, `## Proposal`, `## First step`, `## Log` last) | `templates/proposal.md`, the gardener → `crew propose`, `Proposal.TaskContext` (`crew promote`) |
| A task's `goal` is its goal file's ID, in the same project | `crew new --goal` → `goalTasks`, `taskGoal` (`goalReady` for `crew tick` dispatch, `max_in_flight` in `dispatchChecks`), `crew status` |
| Goal status values `draft running done abandoned` | `crew goal`, `tickGoal`, `crew status`, `lead.md`, `LEDGER.md` |
| A dispatch that only has to wait exits 4 (`notYet`) | `dispatchChecks`, `crew dispatch` → `dispatchReady` |
| The goal lead follows the sections "Intake: turning a request into a brief", "The brief standard" and "Stacked tasks" of the installed `lead.md` | `lead.md` → `goal-lead.md`; checked in `tests/guard.sh` |
| A goal task's worker question that the goal lead leaves for the user gets a "For the user: …" Log line | `goal-lead.md` → `lead.md` "Goals"; checked in `tests/guard.sh` |
| Collaborators' items under Open questions start with `- [ ] @<login>` | `crew feedback` → `workerQuestions` |
| A task is stacked when its `base` equals another task's `branch` in the same project | `crew new --on`, `crew set TASK base` → `parentOf` (dispatch gate, `inFlight`, `crew status`, `crew sync`, `crew restack`) |
| The restack note: `git rebase --onto <target> <base_sha>`, the push `git push --force-with-lease=<branch>:<sha> origin <branch>` with the commit origin had before the rebase, and, when the base changes, `gh pr edit --base <new>` and `crew set TASK base <new>` | `crew restack` → implementer "Workflow: restack"; the worker's `crew set TASK base` is what ends the "needs restack" state after a merge |
| "This task is stacked on task …" in the dispatch prompt | `crew dispatch` → implementer "Stacked tasks" |
| A review round's followup prompt says whether to reply ("Reply to each comment in those rounds with `crew reply`", from `REPLY_TO_THREADS`) | `crew followup` → implementer "Workflow: review rounds" step 7; checked in `tests/guard.sh` |
| Status values `draft ready working needs-input pr-open addressing-feedback merged abandoned` | `crew` (`Status.InFlight`, `crew ready`, `crew sync`, `crew status`), `implementer.md`, `lead.md`, `LEDGER.md` |
| Section headings (`## Goal`, `## Structure`, `## Plan`, `## Acceptance criteria`, `## Review rounds`, `## Context`, `## Log` last) | `templates/task.md` → `crew ready`, `crew feedback`, `Doc.Log`, `crew promote` |
| "Findings the brief settled": structural points from the pre-PR review that the worker left alone because the brief decides them, listed in its Log entry and final report | implementer pre-PR review → lead's first report of the PR; checked in `tests/guard.sh` |
| A PR's `updatedAt` moves whenever a comment is posted or a review submitted | GitHub → `crew sync` (`pr_updated`) → `prMayHaveNewComments` in `crew tick` |
| `<!-- crew -->` marker on worker replies, the automated-review summary and the CI comment | `crew reply`, `crew review-summary`, `crew ci-summary` (`postOrEditComment`) → the `crew feedback` filter |
| `<!-- crew review-summary: review N -->` marker on an automated-review summary, N the newest review it covers (`Task.NewestReview`, from `### Automated review N`), found on the user's own comments | `crew review-summary` (edits the comment with the same N, or posts a new one: a later pass's summary is a comment of its own) |
| `<!-- crew ci-summary: <short sha> -->` marker on a worker's CI comment, for a commit a `CI failed on` or `CI is still red on` line reports (`Task.CIReported`), found on the user's own comments | the worker names the commit (implementer "Workflow: review rounds" step 4) → `crew ci-summary` (edits the comment for the same commit, or posts a new one) |
| A head's checks, from `gh pr view --json statusCheckRollup`: a check run (`__typename` `CheckRun`) has `name`, `status` and, once `COMPLETED`, `conclusion`; a status context (`StatusContext`) has `context` and `state`. Red is a check run's `FAILURE`, `TIMED_OUT`, `STARTUP_FAILURE` or `ACTION_REQUIRED`, or a status context's `FAILURE` or `ERROR`; a status context is unfinished while `PENDING` or `EXPECTED` | GitHub → `rollup` in `cmd_ci.go` → `crew sync` (`ci_state`), `crew ci-fix` |
| A red build's round line `- [ ] CI failed on <short sha>: <check>, <check>`, and past `MAX_CI_FIXES` the Open questions line `- [ ] CI is still red on <short sha> after …`, each checked or not (`ciFailedOn`, `ciStillRedOn`) | `crew ci-fix` (`reviewRound.add`) → `Task.CIFailures` (the count against `MAX_CI_FIXES`), `Task.CIReported` (once a head, and the commits `crew ci-summary` accepts), `Task.FeedbackRounds` (`MAX_REVIEW_ROUNDS` leaves CI-only rounds out), `workerQuestions` (the open question is the user's, never sent to a goal lead), implementer "Workflow: review rounds" step 4, `LEDGER.md`; checked in `tests/guard.sh` |
| The briefing: first line `Code review`, then exactly `## Scope`, `## How to verify`, `## Hard constraints`, `## Stated intent`, stated intent last; the reviewer reads up to `## Stated intent` and derives intent first | `pr-review` `SKILL.md` step 3 → `reviewer.md`, `rubric.md` ("Derived intent vs. stated intent") |
| A line under a task's Decisions ends with whose call it was: `(user)`, `(lead)` or `(worker)`; only `(user)` lines are the user's decisions in the review briefing, and `## Hard constraints` always reads `None recorded.` | `lead.md`, `goal-lead.md`, `implementer.md` (writers), `templates/task.md`, `LEDGER.md` → `pr-review` `SKILL.md` step 3; checked in `tests/guard.sh` |
| The rubric is `rubric.md` beside `SKILL.md`, passed to the reviewer by path (`${CLAUDE_SKILL_DIR}/rubric.md`), with severities `must fix`, `should fix`, `fix later`, `nitpick` and its report format | `pr-review` → `reviewer.md`; checked in `tests/guard.sh` |
| The record: `### Automated review N` under `## Automated reviews` (before `## Review rounds`), a `Reviewers` line naming any reviewer that failed, each finding a `####` heading with `Raised by` (`both`, `Codex` or `Claude reviewer`), `Finding`, a `Round <n>, <reviewer>` line per round reply, `Verdict`, `Agreed severity` (the rubric's four, or `withdrawn`, `split: …`, `disputed`, `uncertain`) and `Outcome` lines, and `Summary posted` under the newest review once the summary is up | `pr-review` step 8 → implementer "Workflow: automated review" (outcomes, resuming, passes) → `crew review-summary` (`Task.NewestReview` reads the headings) → `templates/review-comment.md` → lead (what was left for the user); `templates/task.md`, `LEDGER.md` |
| An unchecked item (`- [ ]`) under Open questions is a question waiting on the user; whoever records its answer under Decisions ticks it | `crew feedback` (collaborators' comments), implementer "When you're blocked" → `OpenQuestions` (`crew tick` marks the task `needs-input`), lead (relays and ticks); checked in `tests/guard.sh` |
| Who a comment is from: the user (`gh api user`), a collaborator (`author_association` OWNER, MEMBER or COLLABORATOR, `user.type` not `Bot`), or anyone else | GitHub REST API → `crew feedback` |
| GitHub comment lookups: `pulls/comments/<id>` has `pull_request_url` and `in_reply_to_id`, `issues/comments/<id>` has `issue_url`, and both and `pulls/<n>/reviews/<id>` have `html_url`, `body`, `user` and `node_id` | GitHub REST API → `prComment` → `crew reply` (which PR a comment is on, the thread root, the quote), `crew react` (whose it is) |
| GraphQL `addReaction(input: {subjectId, content})` takes any `Reactable`, which `PullRequestReview`, `PullRequestReviewComment` and `IssueComment` all are; `content` is `EYES` or `ROCKET` here | `crew react`, from the comment's REST `node_id` |
| Round text "Round N", an ID listed for every comment and review summary (`comment <id>`, `review <id>`), and a request for another pass as `- [ ] Automated review requested (<source>)` (`reviewRound.add`) | `crew feedback`, `crew review` → implementer review-round workflow, `crew reply` and `crew react`; the request line, `pr-review` step 3 (left out of the briefing) |
| A followup prompt names every unsent round, "round N" or "rounds N to M" (`unsentRounds`) | `crew followup`, `crew tick` → implementer "Workflow: review rounds" step 1 |
| State paths, the ledger: `projects/` and `reports/` in the home. Only they are committed by `crew`, edited by agents, or absent from the public repo | `statePaths` in `crew` (`commit` and the guard); checked in `tests/guard.sh`, which also checks this repo tracks nothing there but `projects/.gitkeep`, and no `config/crew.env` or `host` |
| The PostToolUse hook `"${CREW_HOME:-$HOME/crew}/bin/crew" commit --hook` on `Edit\|Write\|NotebookEdit\|Bash`; it reads nothing from its JSON | `lead/.claude/settings.json`, `config/worker-settings.json` → `commitHook`, `crew doctor`; checked in `tests/guard.sh` |
| Hook command path `${CREW_HOME:-$HOME/crew}/bin/crew-guard` and role names. `~/crew` is the usual home; `crew install` sets `env.CREW_HOME` in `~/.claude/settings.json`, so hooks find a home anywhere | agent frontmatter, `lead/.claude/settings.json` and `config/worker-settings.json` → `bin/crew-guard` (which runs `crew guard`), `crew doctor`; checked in `tests/guard.sh` |
| A home other than `~/crew` is written into the `%h/crew` paths and the `# Environment=CREW_HOME=` line of `systemd/*.service`. Every `launchd/*.plist` is rendered: `%h/crew` is the home, any other `%h` the user's home | those files → `renderUnit` and `renderPlist` in `crew install`; checked in `tests/host.sh` and `services_test.go` |
| Each `launchd/<label>.plist` is the twin of `systemd/<label>.service`: the same command, the unit's `PATH` entries within its own, the same `Nice`, `AbandonProcessGroup` where the unit has `KillMode=process`; `Label` is the file's name, `Disabled` is true, `CREW_HOME` is `%h/crew`, output goes to `%h/crew/logs/<label>.log` | `systemd/` ↔ `launchd/`; checked in `services_test.go` |
| What workers may do on GitHub (a restack adds nothing new: `--force-with-lease` on their own branch, and `gh pr edit --base`; automated review adds `crew review-summary` and `gh pr ready`; review rounds add `crew react`; red CI adds `gh run view`, `gh run rerun --failed`, `gh run watch` and `crew ci-summary`, and denies `gh run cancel` and `gh run delete`) | `implementer.md` ("On GitHub, you may do exactly this") ↔ `worker-settings.json` `allow`/`deny`; checked in `tests/guard.sh` |
| Locks: `.crew.lock` (every mutation), `.crew-tick.lock` (one `crew tick` at a time) and `.crew-push.lock` (one ledger push at a time), taken with `flock(2)`; Go opens them close-on-exec, so a session or background push crew starts never holds one | `crew` → `claude`; checked by `tests/stubs/claude` (no open fd on a `.crew*.lock` file) and `tests/crew-e2e.sh` |
| `CREW_PROJECT` is a registered project or `all`, set for every session: by `lead` (`--project P`, the repo it's run in, or `all`), by `crew serve`, and by crew in the `--settings` `env` of the sessions it starts for a project | `cmd_lead.go`, `sessionSettings`, `crew serve` → `defaultProject`; checked in `tests/crew-e2e.sh` and `tests/host.sh` |
| `crew` on PATH for agents | `claude` (in `sessions.go`) prepending `bin/` (the lead, through `crew lead` and `crew serve`, and the gardener), the units' `PATH`, and the `~/.local/bin/crew` link `crew install` makes. Workers and goal leads get the PATH of Claude Code's daemon: the units, the launchd jobs and `claude`, any of which may start it, put `bin/` first, and a daemon started from your own shell finds `crew` through the link ("Environment" under Assumptions to re-check) |
| `bin/lead` finds its home through symlinks, as `bin/crew` does, and runs `crew lead` with its arguments; `crew install` links both into `~/.local/bin` | `bin/lead`, `bin/crew` ↔ `link` in `crew install`; checked in `tests/host.sh` (`lead` run through its link, without `CREW_HOME`) |
| `crew setup` makes `upstream` the public repo, with its push URL `DISABLED`, and `origin` the private one; `crew upgrade` merges the newest `v<version>` tag `upstream` has, once its binary has been fetched | `crew setup` → `crew upgrade`; checked in `tests/host.sh` |
| A release is the tag `v<version>`, matching `VERSION`, with `crew-Linux-x86_64`, `crew-Linux-aarch64`, `crew-Darwin-arm64` and `crew-Darwin-x86_64` attached, each stamped with that version (`-X main.version`) | `.github/workflows/release.yml` → `crew upgrade` (`newestRelease`), `bin/crew`, `crew doctor` (the binary's version is `VERSION`'s) |
| `bin/crew` runs `bin/release/crew-<VERSION>` (or `CREW_VERSION`), fetching a missing one from `https://github.com/isaacjwilliams/crew/releases/download/v<version>/crew-<uname -s>-<uname -m>` and checking it with `crew version`; it exits 2 for `crew guard` when it can't, so a missing binary blocks edits | `bin/crew` ↔ the release's asset names, `crew upgrade` (`CREW_VERSION=<new> bin/crew version`); checked in `tests/host.sh`, with `tests/stubs/curl` serving releases |
| The lead's settings are `lead/.claude/settings.json`; a goal lead's are the same without `agent` (deleted from the parsed file, key order kept), so the two can't drift | `launchGoalLead`; checked in `tests/guard.sh` and `tests/crew-e2e.sh` |
| Settings file names: `lead/.claude/settings.json` (the lead, and goal leads without `agent`), `config/worker-settings.json`, `config/gardener-settings.json` (`crew gardener`), `config/readonly-settings.json` (only `crew observe`) | `crew`, `crew doctor`; checked in `tests/guard.sh` and `tests/crew-e2e.sh` |
| Project memory is `projects/<p>/memory.md` (the lead's: `- **<fact>** <reason>. (<source>, <date>)` per entry) and `projects/<p>/memory-suggestions.md` (`crew remember`'s: the same text after `- [ ] `, ticked `→ kept` or `→ declined: <reason>`) | `crew remember` → `crew status` (`memorySuggestions`), `crew memory`, `lead.md` "Project memory", the guard, `LEDGER.md` |
| The SessionStart hook `"${CREW_HOME:-$HOME/crew}/bin/crew" memory --hook`, with no matcher; it reads nothing from its JSON | `lead/.claude/settings.json`, `config/worker-settings.json`, `config/gardener-settings.json` → `crew memory --hook`, `crew doctor`; checked in `tests/guard.sh` |
| A SessionStart hook's stdout, on exit 0, is added to the session's context, up to 10,000 characters; longer output becomes a file path and a preview | Claude Code → `crew memory --hook` (`hookOutputCap`), the 9,000-character budget in `lead.md` and `LEDGER.md` |
| A project's `mcp.json` is `{"mcpServers": {…}}` with server names of `[A-Za-z0-9_-]`; crew names each `<project>_<server>` in `mcp.local.json` and `lead/.mcp.json`, and fills in `${NAME}` from the project's settings files only | `projects/<p>/mcp.json` → `cmd_mcp.go` → `launchWorker`, `launchGoalLead`, `lead/.mcp.json`, `crew lead`, `crew doctor`, `SETUP.md`, `lead.md`, `implementer.md`; checked in `cmd_mcp_test.go` and `tests/crew-e2e.sh` |
| `--mcp-config` is variadic, so it's never placed right before the prompt | `launchWorker`, `launchGoalLead`; checked by `tests/stubs/claude` |
| `crew mcp sync` prints nothing and exits 1 after reporting any problem | `cmd_mcp.go` → `crew tick`; checked in `tests/crew-e2e.sh` |
| `crew lead` syncs the MCP servers before it builds a project lead's `--settings`, which go by `lead/.mcp.json` as it is on disk | `cmd_lead.go` → `syncMCP`, `leadMCPSettings`; checked in `tests/host.sh` (with `lead/.mcp.json` removed first) |
| Every tool `crew` needs (`runtimeTools` in `crew doctor`, and `codex` once a project has `CODEX_REVIEW=yes`) is on the `PATH` its services set | `systemd/*.service`, `launchd/*.plist` (read from the installed copy) → `crew doctor`, for enabled services (`crewServices`) |
| `crew codex ROOT PROMPT_FILE OUT_PREFIX` writes `<out>.md` (Codex's last message) and `<out>.events.jsonl`, and exits 124 for a stall or with Codex's own status for a failure | `crew codex` → `pr-review` `SKILL.md` steps 4 and 6 |
| `codex exec` takes `-c key=<TOML>`, `-m`, `--skip-git-repo-check`, `--json` (JSON lines on stdout), `-o <file>` for the last message, `-C <dir>` and `-` for a prompt on stdin; `codex login status` fails when signed out | Codex CLI → `crew codex`, `crew doctor`; mirrored in `tests/stubs/codex` |
| A rebuttal round starts both reviewers fresh: the prompt starts `Rebuttal round <n>` and names the scope, rubric, briefing and a round file holding the reviewer's own report and each earlier round's items with its reply, then numbered, labelled items (the other reviewer's findings and objections, the author's cited evidence); the reply answers each item with confirm, refute, withdraw or reseverity, then lists new findings | `pr-review` `SKILL.md` step 6 → `reviewer.md` ("Rebuttal rounds"); Codex gets the same kind of round file, and the answer shape in its round prompt; checked in `tests/guard.sh` |
| `wt switch … --format json` prints `{"action", "path", …}`; `--config-set worktree-path=<TOML string>` sets the path; unapproved hooks fail when stdin isn't a terminal | Worktrunk → `worktreeCreate`; mirrored in `tests/stubs/wt` |

## Extension points

| Goal | Where |
|---|---|
| Change how the lead interviews you or writes briefs | `lead.md` (intake, brief standard); `templates/task.md` if sections change (then check `crew ready` and the contracts above) |
| Change coding standards | The user's `~/.claude/CLAUDE.md` (taste) or the repo's `AGENTS.md` (project conventions). `implementer.md` holds no standards of its own; it relies on those loading |
| Change the PR-body format | `implementer.md` (first run, step 7) |
| Tune the readability review | `casual-observer.md` (budget, checklist, severity, model) |
| Tune the automated PR review | `.claude/skills/pr-review/` (`SKILL.md`: the briefing, reconciliation and record; `rubric.md`: what the reviewer looks for and its severities); `reviewer.md` (model, effort); acting on findings is the implementer's "Workflow: automated review"; the summary is `templates/review-comment.md`; the number of reviews is `AUTO_REVIEWS`; Codex is `CODEX_REVIEW`, `CODEX_MODEL`, `CODEX_MODEL_EFFORT` and `AUTO_REVIEW_REBUTTALS`, run by `cmd_codex.go` |
| Tune the plan review | `plan-reviewer.md` (questions, report format, model); where the lead runs it and what it shows the user are intake step 7 of `lead.md` |
| Tune the gardener | `gardener.md`; the range and turn limit are in `config/defaults.env` (set them in `config/crew.env`) |
| Allow or deny a command | `config/*-settings.json` (`allow` for fewer prompts, `deny` as a tripwire); what an agent may do on GitHub goes in its prompt |
| Add a `crew` subcommand | A new `cmd…` function in `cmd/crew/`, its entry in `commands` (`main.go`), the help text (`help.go`), `fish/completions/crew.fish` |
| Change how stacks behave | `cmd/crew/stacks.go`; the restack note and the two implementer sections are one contract (above) |
| Give workers MCP tools (e.g. Playwright) | Nothing: configure the server for the repo (`.mcp.json`, or `claude mcp add`). To keep one project's workers off a server, add a `deny` rule to `projects/<p>/worker-settings.json` |
| Give a project's lead, goal leads and workers an MCP server (its tracker) | `projects/<p>/mcp.json` in the ledger, secrets in its `local.env` as `${NAME}` (see `cmd_mcp.go`) |
| Add worker settings for one project (`allow` rules, `env`) | `projects/<p>/worker-settings.json` in the ledger (user-owned, add-only merge; see `workerSettings`) |
| Support another worktree tool | A new `WORKTREE_TOOL` case in `worktreeCreate`/`worktreeRemove` (`worktree.go`), a stub in `tests/stubs/`, `crew doctor`, `config/defaults.env` |
| Change how a goal lead works | `goal-lead.md` (parking rules, workflows); its intake is the lead's (`lead.md`), so a change there reaches both |
| Add another agent | New `agents/*.md` (`crew install` copies every `*.md`); add a guard role (`guard.go`) if it edits files, and a settings file with its `allow`/`deny` rules if it runs in the background, with the SessionStart hook if it should know the project's memory |

## Verification

```bash
tests/run.sh            # lint + guard + crew-e2e + host; must pass before a change is done
```

- **`lint`**: `gofmt`, `go vet` for Linux and for macOS, `go test ./...` (the ledger model, settings files, the guard's table, `resolve`, statuses against `LEDGER.md`, each launchd job against its unit, plist rendering and reading, `/proc`, `ps` and `lsof` output, and `crew codex` stopping a stalled Codex), `sh -n` on `bin/`, `bash -n` on the tests, shellcheck on both, fish syntax, JSON validity, and `claude plugin validate agents`.
- **`guard`**: the write-guard allow/block table, run directly and through the lead and worker settings hooks, the home's state paths among them, plus checks that this repo tracks no state, that the lead, the implementer and the goal lead have no tool allowlist, that no settings file turns the sandbox on, that the lead and workers default to auto mode, that the settings run `crew commit --hook` and load project memory, that the lead service leaves itself stopped on exit 3, and that the agents' prompts agree with `crew` and the settings.
- **`crew-e2e`**: `crew` end to end in a crew home made from this checkout (`make_home`, which puts a binary built from this checkout in its `bin/release/`), against a bare-repo remote, with stub `claude`, `gh`, `wt` and `codex`.
- **`host`**: a machine and its home, in fake `HOME`s: `crew install` (a fresh install and its links, unchanged re-runs, edited, new and dropped files, a stray file where a link goes, skipped components, the AGENTS.md question, a home outside `~/crew`, `crew doctor`'s checks), the lease between two machines (`host up`, `--take`, a lost lease, the stand-down, `host down`, `crew serve`, `lead` run through its link), `crew setup`, `crew upgrade` to a release tag in a fake public repo, and the launcher (a download, a failed one, `CREW_VERSION`), with `tests/stubs/curl` serving the releases.

Every suite runs in a fake `HOME` under `tests/.tmp/` with the stubs first on `PATH`, so the tests never start a model session or touch the real setup. The shell suites run only on Linux: they use GNU and util-linux tools, and `/proc`. On macOS, `go test ./...` runs in the release workflow, and the rest is the live checks M1–M8 below. See `tests/README.md` for how to add cases.

What the tests *can't* cover is the live Claude Code behaviour listed below. After changing anything that depends on it, do a smoke test with a tiny task.

## Assumptions to re-check when Claude Code updates

Built against Claude Code 2.1.281–2.1.285 docs and CLI (September 2026); the portable crew (the home, the lease, the lead as a service) against 2.1.296 (October 2026).

- **Live checks still open** for the portable crew, each in a person's real setup:
  - L3: in a phone lead session, nothing from the home's `CLAUDE.md` loads, `~/.claude/CLAUDE.md` does, and reading `~/agent-work/<p>/_lead/AGENTS.md` needs no prompt.
  - L4: a question the lead ticks reaches `origin` within seconds as "edit projects/…".
  - L5: plan-reviewer's `git -C` commands run without a denial or a prompt (check its transcript). No allow rule covers them since 0.5.1: its reads are assumed to count as Claude Code's built-in read-only git, and auto mode's classifier judges the fetch.
  - L6: a failover drill with one `pr-open` and one `working` task: `crew host up --take` on another machine, its tick re-homing the working task, and the old machine standing down when it's back on the network.
  - L7: a goal lead started with the lead's settings minus `agent` runs as goal-lead.
  - L8: the colleague path on a fresh account: clone the public repo, `crew setup`, `crew project add`, a lead from the phone.
- **Live checks on a Mac** (0.3.0), each in a person's real setup. None has run yet:
  - M1: a clone of the private home in `~/crew`, `~/crew/bin/crew install` with no Go installed: the binary downloads and runs without a Gatekeeper prompt (`codesign -dv ~/crew/bin/release/crew-0.3.0` shows a signature: Go's linker signs darwin/arm64 ad hoc, and `curl` sets no quarantine attribute), `crew doctor` passes, and `crew host` shows the name from `scutil`.
  - M2: after `crew install`, a logout and login leave the jobs off (`launchctl print gui/$(id -u)/crew-tick` fails): the plist's `Disabled` holds.
  - M3: `crew host up --take`: `launchctl enable` overrides `Disabled`, `crew-tick` runs every 3 minutes (`~/crew/logs/crew-tick.log`), and a tick reaches `claude` and `gh` and pushes the ledger with no Keychain prompt or auth error: a LaunchAgent in the `gui` domain is assumed to reach the login Keychain and the ssh-agent.
  - M4: `crew` appears on the phone from the Mac's `crew-lead`, after the two first-time prompts.
  - M5: workers dispatched from the phone keep running when a tick ends and when `crew upgrade` restarts `crew-lead`: `AbandonProcessGroup` is assumed to do what `KillMode=process` does (L2 on the Mac).
  - M6: `crew cleanup` on a merged task stops a dev server left in its worktree and leaves a process started from a terminal there alone. Swap real `ps` and `lsof` output into `proc_test.go` while there.
  - M7: after the Mac sleeps and wakes, the ticks carry on. `crew host down` turns the jobs off, and they stay off after a logout and login.
  - M8: L6, the failover drill, between the Linux machine and the Mac, both ways. On the Mac, the stand-down runs inside `crew-tick` and its last step boots that job out, which ends the tick: `~/crew/logs/crew-tick.log` says "stood down", the home is reset, `.crew-lease-lost` is gone, and `launchctl print gui/$(id -u)/crew-tick` fails.
- **Releases** have been made since v0.1.0, which checked the Linux side of what follows. `bin/crew` fetches `https://github.com/isaacjwilliams/crew/releases/download/v<version>/crew-<uname -s>-<uname -m>`, GitHub's documented download URL for a release asset, with `curl -fsSL` (which follows GitHub's redirect to its storage). `.github/workflows/release.yml` assumes `actions/checkout@v4` and `actions/setup-go@v5` (with `go-version-file`), that `ubuntu-latest` has jq and util-linux for the test suites, and that `gh release create --notes-from-tag` reads the annotated tag's message from the local clone, hence the explicit fetch of the tag. Not yet checked: the `macos-latest` job and the Darwin binaries, which v0.3.0 is the first to have.
- **Settings and instructions for the lead in `lead/`:** a project's `"agent"` setting picks a session's agent; `permissions.additionalDirectories` and `PreToolUse` hooks apply to sessions a `remote-control` server starts (checked live on 2026-10-20). From the docs, not live: `claudeMdExcludes` (absolute-path globs, any settings layer) keeps a CLAUDE.md out of a session, and CLAUDE.md files in parent directories load by default, hence the exclude for the home's `CLAUDE.md`. `auto` can't come from a project settings file, so the mode is passed on the command line (`LEAD_PERMISSION_MODE`).
- **`initialPrompt` is prepended to a prompt given on the command line** (`lead "<request>"`), and can't be skipped. From the `sub-agents` docs, not live. The lead's `initialPrompt` tells it to start on a request that follows.
- **Background sessions don't show on Claude Mobile** (checked live on 2026-10-20). The phone sees workers only through the lead.
- **Cloud sessions were ruled out** for the lead: they load no user-level agents or plugins, run on throwaway machines, and have no `--bg`.

- **Agent view / background sessions** (`--bg`, `claude agents --json`, `claude rm`) are a research preview. Their output formats are the most fragile contracts.
- **Resuming a background session:** `claude --resume <session> --bg` with launch flags (`--agent`, `--settings`, …) starts a copy under a new session, whether or not the session's process is still open. 2.1.284 says so in a `note:` line ("keeps its own saved options, so the flags you passed started a copy… Without flags, the same command continues <session> itself") and prints the job ID in color. `crew followup` resumes with no flags and relies on the session keeping its agent, permission mode, auto-compact window, settings and `--add-dir`. A finished session also keeps its process open for about an hour (state `done`, `pid` set), and resuming it then starts a copy too ("session <id> is already running in the background"), so `crew followup` runs `claude stop` on it first. A live run confirmed both causes of a copy; resuming in place after a stop hasn't been confirmed yet.
- **Removing a background session:** the agent-view docs say `claude rm` removes a session from the list, and leaves "a worktree you created yourself and started the session inside". They don't say whether it ends a process that's still open, so `crew cleanup` runs `claude stop` first. That `claude rm` needs no confirmation when run without a terminal hasn't been confirmed by a live run.
- **Confirming a launch:** `crew` counts a launch as started once `claude agents` lists the job with a `pid` and a `sessionId` that starts with the job ID. On 2.1.296 every job's ID was its session's first 8 characters. A resumed job's `pid` and `sessionId` come from its live process. A process that has just started can show a new session ID before it loads the resumed one. A resume of a missing session ends in `failed` about 10 seconds after launch ("source session … not found"). This comes from one incident and from reading the 2.1.296 code; a live run hasn't confirmed the timing.
- **`--autocompact <auto|tokens>`** sets a session's auto-compact window (100k–1M, per `claude --help` in 2.1.285) and overrides the user's `autoCompactWindow` setting. Workers on a 1M model otherwise compact at about 967K. That a resumed worker keeps the window it was started with hasn't been confirmed by a live run.
- **AGENTS.md loading** is automatic only when no `CLAUDE.md`/`CLAUDE.local.md` is present, unless Project instructions is set to `claude-md-and-agents-md`.
- **`--settings` given a JSON string** (every worker, goal lead and gardener) is assumed to apply the same way as a file, hooks included. This hasn't been confirmed by a live run.
- **Resumed conversations keep their system prompt** until they're compacted (`--system-prompt-snapshot`, on by default). We assume that includes the `--agent` instructions, so `lead -c` and `crew followup` run on the `lead.md`/`implementer.md` they started with. This hasn't been confirmed by a live run.
- **Session names:** `claude --resume <name>` resumes by name, and an interactive session whose name is already live gets a variant of it. A lead session is named `lead`, or `lead-<project>` for one project, so several past sessions share a name.
- **Environment:** a background session (`claude --bg`) gets the environment of Claude Code's daemon, not of the `claude --bg` that asked for it. The first `claude --bg` on a machine starts the daemon, and every later session, whatever its project, gets the daemon's environment. Seen live on 2026-11-10 (2.1.319) in `/proc`: a daemon started by crew's launch of one project's worker gave that `CREW_PROJECT` to every session after it, including other projects' workers. So crew gives a session its project and home in its `--settings` `env` (`sessionSettings`), which Claude Code sets for the session and the commands it runs; that this reaches a live `--bg` session's hooks and shell hasn't been confirmed by a live run. `PATH` isn't set that way: a settings `env` value replaces a variable and can't add to it, and a session needs the user's own `PATH` for their tools. It gets the daemon's `PATH`, which finds this home's `crew` whoever started the daemon: the systemd units, the launchd jobs and crew's `claude` put `bin/` first, and a daemon started from your own shell finds `crew` through the `~/.local/bin` link, as long as that's on your `PATH`. The sessions `claude remote-control` spawns are assumed to get the environment of the `remote-control` server (`crew serve` sets `CREW_PROJECT=all`); if they didn't, the Remote Control lead would scope itself to whatever repo its shell visits. This hasn't been confirmed by a live run.
- **Auto mode** (`--permission-mode auto`) needs a plan that has it. The lead and workers default to it.
- **Stacked PRs on GitHub:** when a merged PR's head branch is deleted, GitHub retargets the open PRs based on it to the branch it merged into; deleting an unmerged branch closes them instead. Without the deletion, a child PR keeps targeting the merged branch until its worker runs `gh pr edit --base`. Either way the child's branch needs the rebase.
- **Attaching to a worker** (`crew attach`): an attached client leaves the session's agent state as it was, so an idle worker still takes a followup, and the resume ends the attach. A live run with `claude attach` confirmed both: `crew tick` resumed the attached worker with a message, and the attach ended. Check after an update: attach to an idle worker, run `crew followup TASK --note hi` from another terminal, and see that the followup goes through and the attach ends.
- **Restack through followup:** `crew restack` relies on the same resume behaviour as `crew followup`, and on the worker following a rebase command from its prompt. Neither has been confirmed by a live run.
- **Codex** (built against `codex-cli` 0.167.0): the `codex exec` flags and event shape in the Contracts table. Run `codex exec --help` after upgrading it.
- **Worktrunk** (built against `wt` 0.78): the `switch`/`remove` flags and JSON shape in the Contracts table. Run `wt switch --help` after upgrading it.
- **Frontmatter hooks** in `~/.claude/agents/` run without a workspace-trust prompt; project-level agent hooks would need trust.
- **Workspace trust** covers the folders below the one it was accepted in. Seen live on 2026-10-21: trust accepted in `~/crew/lead` was recorded in `~/.claude.json` for the repo's top level, `~/crew`, and `claude` in `~/crew/lead` then didn't ask again. `crew doctor` counts a folder above as trust for that reason. Background sessions are assumed to inherit it the same way from `~/agent-work`; that hasn't been confirmed by a live run.
- **MCP servers for one project** (not confirmed live). Each says how to check it, after `crew install` with a project that has an `mcp.json`:
  - A background session started with `--mcp-config <file>` gets those servers. Check: a dispatched worker for that project finds their tools with ToolSearch, and one for another project doesn't.
  - A session resumed without flags keeps its `--mcp-config` and reads the file again. Check: `crew followup` that worker; `claude agents` shows the same session, not a copy, and it still has the tools. Then remove the `mcp.json`, run `crew mcp sync` and follow up again: it resumes cleanly.
  - A stdio server gets the `env` values in its config as written. Check: a stdio server whose key comes from `local.env` starts and works.
  - Claude Code reads `.mcp.json` from `lead/`, the session's folder, not from the home's git root, and `enableAllProjectMcpServers` in `lead/.claude/settings.local.json` approves those servers, with no prompt. The docs list that setting as settable in any file. Check: `lead`, and the `crew serve` session, list the servers in `/mcp` without asking to approve them.
  - `disabledMcpjsonServers` passed with `--settings` keeps those servers from connecting, over `enableAllProjectMcpServers`, with no approval prompt. The `mcp` docs say it "blocks it in every permission mode"; that it wins over `enableAllProjectMcpServers` is assumed. Check: with servers for two projects, `lead --project Q` has only Q's tools, and `/mcp` shows the other project's disabled or absent. If not, the fallback is a lead folder per project.
  - An OAuth login made in one session serves background sessions. The `mcp` docs say Claude Code keeps sign-ins per endpoint, so a login for a server's URL is assumed to serve every server with that URL, the one in `lead/.mcp.json` and the one in a worker's `mcp.local.json` alike. Check: sign in once with `/mcp` in `lead --project <p>`, then a dispatched worker for `<p>` uses the server.
- **Skills from `--add-dir`** (not confirmed live): the skills docs say Claude Code loads `.claude/skills/` from a directory passed with `--add-dir`, and watches it for changes. Workers and goal leads get `pr-review` that way, from the home; a plain `claude` session elsewhere doesn't. A skill there whose name clashes with one of the repo's own is run as `/<prefix>:pr-review` (2.1.309 and later). `${CLAUDE_SKILL_DIR}` in a skill's text is its own folder. To check: after `crew upgrade`, `/pr-review` is offered in a worker session and not in a `claude` session outside the home.
- **Tool inheritance:** an agent without `tools:` inherits every tool in its session, MCP servers included, also when it runs as the main session with `--agent`. `disallowedTools` names that don't resolve are assumed to be ignored. Neither has been confirmed by a live run.
- **SessionStart hooks** from `--settings` and from `lead/.claude/settings.json` are assumed to fire for `--bg` sessions, for their resumes (`crew followup`), after compaction, and in `claude -p` (the gardener), and not for subagents. The `hooks` docs list the sources (startup, resume, clear, compact, fork), say plain stdout reaches the context, and cap it at 10,000 characters; the rest is inferred, and none of it has been confirmed by a live run. Whether each resume adds another full copy to the context, beside the one already in the transcript, is also unchecked.
- **Hooks from `--settings`** fire for tool calls in subagents, with `agent_type` set, while frontmatter hooks run only for their own agent. `Agent(<name>)` deny rules block one subagent type. Neither has been confirmed by a live run.
- **Background subagents:** `background: true` keeps plan-reviewer in the background even where fork mode is off, so the lead stays free to talk while it runs. Its result arrives in a later turn as a completion notification. A background subagent keeps Read, Grep, Glob and Bash, which is all plan-reviewer uses. Documented in `sub-agents`; not confirmed by a live run.
- **CLAUDE.md in subagents:** a subagent loads the whole CLAUDE.md hierarchy (`~/.claude/CLAUDE.md`, the project's CLAUDE.md or AGENTS.md) unless its frontmatter sets `omitClaudeMd`, and never sees the parent's conversation. plan-reviewer relies on both: the user's taste arrives that way, and its read stays independent of the lead's. As a subagent of the lead, which runs outside every repo, it gets no project CLAUDE.md or AGENTS.md, and reads them at the task's base through `git -C` instead. The lead's `claudeMdExcludes` (in `lead/.claude/settings.local.json`) is assumed to keep the home's `CLAUDE.md` out of plan-reviewer too; not confirmed by a live run. Documented in `sub-agents`; not confirmed by a live run. The implementer, a main session started with `--agent`, is assumed to load `~/.claude/CLAUDE.md` the same way: it has no coding standards of its own. Documented in `memory`; not confirmed by a live run.
- **Worker states** (`claude agents --json`): `state` blocked means the session needs the user, whether it asked a question or holds a prompt open; `status` (busy, waiting or idle) is set only while the process is alive, and `waitingFor` names an open prompt when `status` is waiting. Documented in `agent-view`. Seen live on 2026-10-15: a worker that ended its turn on a question read `blocked`, `busy` (a dev server it had started was still running), with no `waitingFor`. A permission prompt reading `waiting` hasn't been seen live. Seen live on 2026-10-16 (2.1.296): after a turn ended cleanly, a worker whose dev server was still running read `working`, `busy` for over two hours, while its terminal showed "done · 1 shell still running"; its transcript had the turn's `turn_duration` entry. The docs say `working` covers "work it drives on its own". That dev server, started the day before, also survived two `claude stop`s and resumes: the docs say background shells carry over to the session's next process.
- **Transcripts** aren't a documented interface. If Claude Code moves them or stops writing `turn_duration`, `turnEnded` finds nothing and a `working` worker counts as mid-turn again: rounds wait for it, as before 2026-10-16, and nothing is interrupted.
- **Goal leads** haven't been run live. Three things they rely on are untested: that a background session gets the repo's MCP servers and the user's connectors (a tracker, say) and can read them in `auto` mode without a prompt; that `plan-reviewer`'s reports reach a background session in a later turn after it ends its own; and that a goal lead whose turn has ended while reviews still run takes a resume without losing them (the docs say background subagents carry over to the next process).
- **A PR's `updatedAt`** equalled the time of the last review or conversation comment, to the second, on every open PR in cli/cli and rails/rails whose last event was one (checked 2026-10-15). GitHub doesn't document it. If it stops holding, `crew tick` misses comments on quiet PRs until something else touches them; drop `prMayHaveNewComments`.
- **The lead as a service** (`crew-lead.service`, `crew serve`): `claude remote-control --name crew --spawn same-dir --permission-mode <mode>`, run by systemd with no terminal. Checked live on 2026-10-20 (2.1.296): the phone can start sessions on a `remote-control` server and they honour the project's `"agent"`; project `additionalDirectories` and PreToolUse hooks apply in them; `--continue` brings back only the server's own first session, and sessions started from the phone hang after a server restart; `--permission-mode auto` works, `--settings` is refused in server mode. On 2026-10-21 `--continue` failed on a first start ("No recent session found in this directory"), and the docs say it serves only that one session, so `crew serve` doesn't pass it: a plain start brings back every session the last server served, within about four hours. `--spawn same-dir` keeps sessions in `lead/`, whatever spawn mode was saved for it. Checked live on 2026-10-21: the service starts and serves with no terminal, `crew` appears on the phone, and its sessions run as the lead and read the ledger. `initialPrompt` doesn't fire in them (the server runs each as `claude --print --input-format stream-json …`), so the lead on the phone waits for a first message instead of opening with a briefing. Checked live on 2026-10-22 (2.1.296): two idle workers that a phone session had dispatched kept running through the restart `crew upgrade` makes, under `KillMode=process` (L2). Not yet checked: that phone sessions come back after a restart.
- **An idle crew uses no model** (principle 10; not confirmed live). crew sends nothing while idle, but two of Claude Code's processes stay up. The Remote Control docs say the server "registers with the Anthropic API and polls for work", and that it opens one session when it starts (`--create-session-in-dir`, on by default). The agent-view docs tie model requests to a session's turns and the summaries written as each ends, and say the daemon (their "supervisor") stops a session's process once it has waited unattached for about an hour. Neither page says whether a waiting server, its first session or the daemon makes model requests. To check: leave the crew with nothing in flight for a day, with the gardener's timer paused, and compare your usage before and after.
- **`crew tick` from systemd:** `claude --bg` run from a oneshot service with `KillMode=process` is assumed to leave the background-session daemon, and the workers it resumes, running after the service exits. It also assumes `git fetch` works without your ssh-agent. Neither has been confirmed by a live run. `crew doctor` checks that the unit's `PATH` finds the tools `crew` needs.
- **Live verification:** the lead, implementer and gardener haven't been run end to end against a real model. Do a smoke test with a tiny task after any significant change.
