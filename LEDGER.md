# Agent ledger

The coordination state for the crew: the `lead`, `implementer` workers, `casual-observer`, and `gardener`. It lives in the crew home (`~/crew`, a clone of the crew repo), under `projects/`, outside every repository you work on, so none of it shows up for collaborators. The home is a git repo: `crew` commits the ledger after every mutating command, so `git log -- projects` there is the history of who decided what. Those commits take only the ledger, never the home's code or config. Each one is pushed to the home's `origin`, your private repo, in the background a moment later, and `crew tick` pushes anything that didn't make it.

## Layout

```
~/crew/
  bin/crew               CLI (on PATH via the fish conf.d snippet)
  bin/crew-guard         PreToolUse hook the agents use
  config/defaults.env    every setting, its default and what it does (from upstream)
  config/crew.env        your own settings, over the defaults (caps, branch prefix, PR mode, ...)
  config/*-settings.json permission rules passed to sessions with --settings
  templates/             task.md, goal.md and proposal.md
  projects/<project>/
    project.env          REMOTE, DEFAULT_BRANCH, per-project overrides (e.g. WORKTREE_TOOL)
    local.env            REPO: this machine's clone of the project (not committed)
    worker-settings.json optional: settings added to this project's workers (add-only)
    tasks/NNN-<name>.md  one file per task; <name> is the branch name without BRANCH_PREFIX
    goals/NNN-<slug>.md  one file per goal: a batch of items a goal lead works through
    proposals/*.md       gardener proposals
    gardener.last        last commit the gardener reviewed
  reports/               free-form notes the lead keeps
  host                   the machine that runs the crew; `crew host up` claims it
  logs/                  gardener runs, Worktrunk hook output and background pushes (not committed)
```

`<project>` is `<owner>-<repo>` from the `origin` remote.

Worktrees live outside both the ledger and the repo, at `$WORK_ROOT/<project>/<task-id>` (default `~/agent-work`). A task's `worktree` is this machine's path: when the task moves to another machine, `crew followup` makes the worktree there, from the branch on origin, and records the new path. A goal's goal lead reads the code in a detached checkout beside them, `$WORK_ROOT/<project>/_goal-<goal-id>`, and the lead in `$WORK_ROOT/<project>/_lead`, which `crew checkout <project>` brings up to date.

## One machine at a time

Only the machine named in `host` changes the ledger. On any other, `crew` reads (`crew status` pulls first and says it's read-only) and refuses every change, naming the machine that runs the crew. `crew host up` claims it for this machine with a push, `--take` from another machine, and `crew host down` hands it over. A machine that finds the lease gone stands down at its next `crew tick`: what origin never got stays on a branch `lost/<host>-<time>`.

## Task lifecycle

| status | meaning | set by |
|---|---|---|
| `draft` | brief being written with the lead | `crew new` |
| `ready` | brief complete and approved: by you, or for a goal's task by its goal lead | `crew ready` |
| `working` | worker running on first pass | `crew dispatch` |
| `needs-input` | waiting on you: an unchecked item under Open questions (a worker's question, or a collaborator's PR comment). In a goal's task, the goal lead sees a worker's question first | worker, or `crew tick` once the worker is idle |
| `pr-open` | PR is up and waiting for your review | worker |
| `addressing-feedback` | worker handling a review round | `crew followup` |
| `merged` | PR merged | `crew sync` (and so `crew tick`) |
| `abandoned` | dropped | you or the lead via `crew set` |

"In flight" means `working`, `needs-input`, `addressing-feedback`, or `pr-open`. `MAX_IN_FLIGHT` caps how many tasks can be in flight at once, and `MAX_WORKERS` caps how many worker sessions can be running at once. A task stacked on another in-flight task isn't counted, so a stack takes one place.

## Front-matter keys (task files)

`id`, `title`, `project`, `status`, `goal` (the goal the task belongs to, if any; set by `crew new --goal`), `base` (the branch the task starts from and its PR targets), `base_sha` (written by `crew` only; see Stacked tasks), `restack_sent` (the commit the last `crew restack` asked the worker to rebase onto; written by `crew` only), `branch`, `depends_on` (task ids, space or comma separated), `pre_pr_review` (yes/no), `pr_mode` (draft/ready/none), `worktree`, `pr`, `pr_state`, `pr_updated` (when GitHub last saw a change to the PR, from `crew sync`; written by `crew` only), `worker_name`, `worker_job`, `worker_session`, `review_rounds`, `last_round_sent` (the newest round a worker was sent), `feedback_since` (UTC), `questions_routed` (a checksum of the worker's open questions last sent to the goal lead; written by `crew` only), `created`, `updated`.

Front matter is flat `key: value`. Edit it with `crew set` rather than by hand when a session might be writing at the same time.

## Goals

A goal is a batch of items, listed in its file or on a tracker, that the user hands the crew to work through without them. `crew goal start` launches its **goal lead**, a background session that writes and plan-reviews a brief for every item and marks each one `ready`. `crew tick` dispatches those as `MAX_WORKERS` and `MAX_IN_FLIGHT` allow. An item the goal lead shouldn't decide alone is **parked**: its task stays `draft`, with the question under Open questions for you.

| status | meaning | set by |
|---|---|---|
| `draft` | Goal section being written with the lead | `crew goal new` |
| `running` | the goal lead is working through it | `crew goal start` (or a `crew goal note` to a done goal) |
| `done` | every task merged or abandoned; goal lead and checkout removed | `crew goal done` (and so `crew tick`) |
| `abandoned` | stopped; goal lead and checkout removed, tasks left as they are | `crew goal abandon` |

Front-matter keys: `id`, `title`, `project`, `status`, `checkout` (the goal lead's working directory), `lead_name`, `lead_job`, `lead_session` (its session, like a worker's three identifiers), `unsent_notes` (your notes the goal lead hasn't been sent yet; written by `crew` only), `created`, `updated`. Sections: Goal (your words, including where the items come from), Items (written by the goal lead), Decisions (your notes from `crew goal note`, and the goal lead's own calls), Log.

The goal lead hears about two things, through `crew goal followup`, which `crew tick` runs once it's free: your notes, and new questions from its tasks' workers. A question it can't answer stays unchecked, with a "For the user" line in the task's Log, and reaches you through the lead like any worker question.

## Stacked tasks

A task whose `base` is another task's `branch` is stacked on that task, its parent. `crew new --on TASK` sets that up (and adds the parent to `depends_on`); `crew set TASK base <branch>` does the same for an existing task. There is no other marker.

- A stacked task can be dispatched once its parent's branch is on GitHub. Its other `depends_on` entries must still be merged.
- `base_sha` is the last commit of the parent's in the task's branch. `crew dispatch` records it and `crew sync` keeps it current; a restack cuts there.
- `crew sync` and `crew status` report a stacked task that needs a restack: its parent's branch has new commits, or its parent merged. `crew restack TASK` resumes the task's worker with the rebase to run. After a merge the worker also retargets the PR and sets `base` to the branch the parent merged into, which is what ends the stacking.
- Restack from the bottom of a stack up; `crew restack` refuses a task whose parent needs one first. It also refuses to send the same restack twice (`restack_sent`): if a worker didn't manage the last one, its Open questions and log say why. `--again` sends it anyway.

## How a worker is found again

`crew dispatch` records three identifiers for the worker. `crew followup` uses them to resume the same session, in the same worktree, with its full history. It resumes with no launch flags: the session keeps the agent, permission mode and settings it was started with, and passing them again would make Claude Code start a copy. A resumed worker therefore runs on the worker settings from when it was dispatched; `--fresh` picks up changed ones. Resuming a session whose process is still open (a finished worker keeps one for about an hour) would also start a copy, so `crew followup` stops that process first. If Claude Code starts a copy anyway, `crew followup` records the copy, so the next followup continues from it. If that session can't be resumed (for example, its transcript was cleaned up), `crew followup` starts a fresh worker that rebuilds context from the task file and the branch. `--fresh` forces that.

| identifier | example | what it's for |
|---|---|---|
| `worker_name` | `crew-<project>-<task-id>` | shows in agent view; the `SendMessage` target while the session is busy (mid-turn, or holding a prompt open). A copy gets a different name |
| `worker_job` | short ID | used by `claude attach`, `claude logs`, and `claude stop`; `MAX_WORKERS` counts these |
| `worker_session` | full UUID | used by `claude --resume` |

## Review rounds

`crew feedback TASK` pulls the PR comments that are newer than `feedback_since`. *Your* comments are appended as `### Round N`. Collaborators' comments (people with write access, not bots) go under Open questions as unchecked items, for you to decide on; nobody acts on them until you do. Anyone else's are ignored. Each comment and review summary is listed with its ID. Workers answer them with `crew reply`: an inline comment in its thread, and a conversation comment or review summary with a PR comment that quotes it (GitHub has no threads for those). `crew reply` refuses an ID that isn't on the task's own PR. It appends a hidden `<!-- crew -->` marker so later `crew feedback` runs don't read those replies back as new feedback (the replies are posted under your GitHub account).
