# Agent ledger

The coordination state for the crew: the `lead`, `implementer` workers, `casual-observer`, and `gardener`. It lives in the crew home (`~/crew`, a clone of the crew repo), under `projects/`, outside every repository you work on, so none of it shows up for collaborators. The home is a git repo: `crew` commits the ledger after every mutating command, so `git log -- projects` there is the history of who decided what. Those commits take only the ledger, never the home's code or config. Each one is pushed to the home's `origin`, your private repo, in the background a moment later, and `crew tick` pushes anything that didn't make it.

## Layout

```
~/crew/
  bin/crew               the CLI's launcher (on PATH through crew install's link in ~/.local/bin)
  bin/crew-guard         PreToolUse hook the agents use
  bin/lead               the lead command, which runs crew lead (linked into ~/.local/bin too)
  bin/release/           the CLI's binary for this machine, from its release (not committed)
  config/defaults.env    every setting, its default and what it does (from upstream)
  config/crew.env        your own settings, over the defaults (caps, branch prefix, PR mode, ...)
  config/*-settings.json permission rules passed to sessions with --settings
  lead/.mcp.json         every project's MCP servers, for the leads; written by crew (not committed)
  templates/             task.md, goal.md, proposal.md, review-comment.md and ci-comment.md
  projects/<project>/
    project.env          REMOTE, DEFAULT_BRANCH, per-project overrides (e.g. WORKTREE_TOOL)
    local.env            REPO: this machine's clone of the project (not committed)
    worker-settings.json optional: settings added to this project's workers (add-only)
    mcp.json             optional: MCP servers for this project's sessions, secrets as ${NAME} (see SETUP.md)
    mcp.local.json       written by crew from mcp.json, values filled in; never edit (not committed)
    tasks/NNN-<name>.md  one file per task; <name> is the branch name without BRANCH_PREFIX
    goals/NNN-<slug>.md  one file per goal: a batch of items a goal lead works through
    proposals/*.md       ideas worth keeping that aren't tasks yet (see Proposals)
    memory.md            facts about the project the user approved (see Project memory)
    memory-suggestions.md  facts suggested for memory.md, waiting on the user
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
| `needs-input` | waiting on you: an unchecked item under Open questions (a worker's question, a collaborator's PR comment, or CI still red after `MAX_CI_FIXES` CI rounds). In a goal's task, the goal lead sees a worker's question first | worker, or `crew tick` once the worker is idle |
| `pr-open` | PR is up and waiting for your review | worker |
| `addressing-feedback` | worker handling a review round | `crew followup` |
| `merged` | PR merged | `crew sync` (and so `crew tick`) |
| `abandoned` | dropped | you or the lead via `crew set` |

"In flight" means `working`, `needs-input`, `addressing-feedback`, or `pr-open`. `MAX_WORKERS` caps how many worker sessions can be running at once, and a goal's `max_in_flight` caps how many of its tasks can be in flight at once (see Goals).

## Front-matter keys (task files)

`id`, `title`, `project`, `status`, `goal` (the goal the task belongs to, if any; set by `crew new --goal`), `base` (the branch the task starts from and its PR targets), `base_sha` (written by `crew` only; see Stacked tasks), `restack_sent` (the commit the last `crew restack` asked the worker to rebase onto; written by `crew` only), `branch`, `depends_on` (task ids, space or comma separated), `priority` (a whole number, higher first, empty means 0; orders `crew tick`'s dispatch of running goals' ready tasks; set with `crew set`), `pre_pr_review` (yes/no), `pr_mode` (draft/ready/none), `worktree`, `pr`, `pr_state`, `pr_updated` (when GitHub last saw a change to the PR, from `crew sync`; written by `crew` only), `ci_state` (the checks on the PR's head commit: `running` while any hasn't finished, then `failing` or `passing`, and empty when it has none; from `crew sync`, and only `crew tick`'s filter for running `crew ci-fix`; written by `crew` only), `worker_name`, `worker_job`, `worker_session`, `review_rounds`, `last_round_sent` (the newest round a worker was sent), `feedback_since` (UTC), `questions_routed` (a checksum of the worker's open questions last sent to the goal lead; written by `crew` only), `created`, `updated`.

Front matter is flat `key: value`. Edit it with `crew set` rather than by hand when a session might be writing at the same time.

## Goals

A goal is a batch of items, listed in its file or on a tracker, that the user hands the crew to work through without them. `crew goal start` launches its **goal lead**, a background session that writes and plan-reviews a brief for every item and marks each one `ready`. `crew tick` dispatches those as `MAX_WORKERS` and the goal's `max_in_flight` allow, highest `priority` first, then by project and ID, across every running goal. Every task of the goal that's in flight counts toward `max_in_flight`, each task of a stack too. An item the goal lead shouldn't decide alone is **parked**: its task stays `draft`, with the question under Open questions for you.

| status | meaning | set by |
|---|---|---|
| `draft` | Goal section being written with the lead | `crew goal new` |
| `running` | the goal lead is working through it | `crew goal start` (or a `crew goal note` to a done goal) |
| `done` | every task merged or abandoned; goal lead and checkout removed | `crew goal done` (and so `crew tick`) |
| `abandoned` | stopped; goal lead and checkout removed, tasks left as they are | `crew goal abandon` |

Front-matter keys: `id`, `title`, `project`, `status`, `checkout` (the goal lead's working directory), `lead_name`, `lead_job`, `lead_session` (its session, like a worker's three identifiers), `unsent_notes` (your notes the goal lead hasn't been sent yet; written by `crew` only), `max_in_flight` (how many of its tasks may be in flight at once; empty means `GOAL_MAX_IN_FLIGHT`; set by `crew goal new --max-in-flight` and `crew goal cap`), `created`, `updated`. Sections: Goal (your words, including where the items come from), Items (written by the goal lead), Decisions (your notes from `crew goal note`, and the goal lead's own calls), Log.

The goal lead hears about two things, through `crew goal followup`, which `crew tick` runs once it's free: your notes, and new questions from its tasks' workers. A question it can't answer stays unchecked, with a "For the user" line in the task's Log, and reaches you through the lead like any worker question.

## Proposals

A proposal is an idea worth keeping that isn't ready to be a task, in `proposals/<YYYY-MM-DD>-<slug>.md`. The gardener writes refactor proposals on its schedule; the user, a lead or a worker writes one with `crew propose "<title>" --why "<a paragraph>"`. A proposal stays out of the task list, so it doesn't look like work in progress. The lead raises an open one at intake when it touches the task's area, and `crew promote` turns one into a draft task.

| status | meaning | set by |
|---|---|---|
| `open` | kept, not acted on yet | `crew propose`, or the gardener |
| `deferred` | set aside for now; can still be promoted | you or the lead via `crew set` |
| `accepted` | promoted; `task` names the draft task it became | `crew promote` |
| `rejected` | not wanted; the Log says why | you or the lead via `crew set` |

Front-matter keys: `id`, `title`, `project`, `status`, `source` (who wrote it: `gardener`, `user`, `lead` or `worker:<task id>`), `revisit_when` (optional: one line naming the condition that makes it worth acting on, such as "a second pricing rule beyond the tax rate varies by place"), `size` (S, M or L, when known), `task` (set by `crew promote`), `created`, `updated`. Sections: Friction observed, Evidence, Why it matters, Proposal, First step, Risks and coordination, Log. The gardener fills them all. For the others only Why it matters is needed, which is where `crew propose` puts its paragraph; the rest can stay empty.

## Project memory

`memory.md` holds durable facts about a project that the user approved and every agent working on it should know: mostly settled domain decisions that outlive the task that made them, and private working facts collaborators shouldn't see. It isn't status, work (tasks and proposals), conventions collaborators follow too (the repo's AGENTS.md) or the user's taste across projects (their `~/.claude/CLAUDE.md`). Agents load all of it at session start through a SessionStart hook, `crew memory --hook`; `crew memory --project <p>` prints it anywhere. Neither file has front matter.

- **Suggestions.** Anyone suggests a fact with `crew remember "<fact>" --why "<reason>" [--source S]`, which appends a line to `memory-suggestions.md` and creates the file, with a heading, the first time. `source` is as for proposals: `lead` (the default), `user`, `gardener` or `worker:<task id>`. Only `crew remember` adds lines; only the lead ticks them.

  ```
  - [ ] **Dashboard columns are unlimited** The user chose it over a cap of 12. (worker:042-dashboard-columns, 2026-11-09)
  ```

  An unchecked item waits on the user, like an open question, and `crew status` counts them. The lead settles each with the user and ticks it with the outcome: `- [x] … → kept` or `- [x] … → declined: <reason>`.
- **Entries.** Only the lead writes `memory.md`, and only on the user's say-so; `crew guard` blocks workers' and the gardener's edits of it. It's the heading `# Project memory: <project>`, then one `- ` item per fact. An approved suggestion's text after the checkbox moves into it unchanged: `- **<fact>** <reason>. (<source>, <date>)`. Keep it under 9,000 characters (`wc -m`): every agent loads all of it at session start, and Claude Code passes on at most 10,000 characters of a hook's output. Past that, `crew memory --hook` prints only its header and an instruction to read the file.

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

## Decisions

A task's `## Decisions` holds one dated line per decision about the task: your answers and instructions, and the leads' and the worker's own calls. Each line ends with whose call it was: `(user)` when it records your own words or answer, otherwise `(lead)` or `(worker)`. Only a `(user)` line counts as yours: a worker's automated review gives its reviewer those lines as your decisions, and no others.

## Automated reviews

After opening its PR, a worker reviews it with its `pr-review` skill, up to `AUTO_REVIEWS` times: the first *pass* of automated review. When a review round asks for another (below), it runs another pass, of up to `AUTO_REVIEWS` reviews and at least one. A pass is the reviews after the last `Summary posted` line, and numbers carry on across passes. The worker records each review in the task file, under `## Automated reviews` (just before `## Review rounds`), as `### Automated review N`. The record holds the loop's whole state, so a worker resumed or compacted in the middle carries on from it:

- the range reviewed, the reviewers (the Claude reviewer, and Codex when `CODEX_REVIEW` is on; each marked `failed` when it didn't report), how many rounds they had between them, and each one's derived intent and reading of the stated intent;
- every finding a reviewer raised, as a `####` heading with its severity, and lines for who raised it (`both`, `Codex` or `Claude reviewer`), the finding (quoted), each round's replies to it (quoted), the worker's verdict and evidence, the agreed severity, and the outcome. Besides the rubric's four, an agreed severity can be `withdrawn` (its raiser took it back in a round, and no reviewer holds it), `split` (one reviewer holds it and the other refutes it; the worker settles it), `disputed` (the worker disagrees, with evidence, and no reviewer does) or `uncertain`. The worker fills in the outcome once it has acted: `fixed in <sha>`, `proposal <file name>`, `recorded` (a nitpick outside the PR's lines), `withdrawn in round <n>`, `not a defect: <evidence>` (a split it set aside) or `left for the user`;
- each reviewer's conclusion and what they couldn't check;
- `Summary posted`, under the newest review, once the worker has posted the pass's summary comment with `crew review-summary`. That comment is filled in from `templates/review-comment.md` and this record alone. It carries `<!-- crew -->`, so `crew feedback` skips it, and `<!-- crew review-summary: review N -->`, N the newest review, so a second `crew review-summary` for the same review edits it instead of posting another.

## Review rounds

`crew feedback TASK` pulls the PR comments that are newer than `feedback_since`. *Your* comments are appended as `### Round N`. Collaborators' comments (people with write access, not bots) go under Open questions as unchecked items, for you to decide on; nobody acts on them until you do. Anyone else's are ignored. Each comment and review summary is listed with its ID. Workers react to them with `crew react` (👀 when they pick a round up, 🚀 before changing code for a comment), and answer them with `crew reply`: an inline comment in its thread, and a conversation comment or review summary with a PR comment that quotes it (GitHub has no threads for those). `crew reply` refuses an ID that isn't on the task's own PR. It appends a hidden `<!-- crew -->` marker so later `crew feedback` runs don't read those replies back as new feedback (the replies are posted under your GitHub account).

A round can also ask for another pass of automated review, with the line `- [ ] Automated review requested (<source>)`, last in the round: one line for each request, and one pass answers them all. The source is `comment <id>`, for your PR comment whose whole text is `crew review` (which the round lists in place of the comment), or `crew review`, for a round of its own that the `crew review TASK` command appends. The worker runs the pass after the round's comments, and ticks the line; the pass's summary comment answers it. A round that only asks for a pass counts toward `MAX_REVIEW_ROUNDS` like any other.

When CI goes red on a PR its worker has handed to you (`pr-open`), `crew tick` runs `crew ci-fix TASK`, which appends a round of its own holding one line: `- [ ] CI failed on <short sha>: <check>, <check>`. It does so once every check on that head commit has finished, and once for each head: the line is the record, and no key keeps it. The worker deals with it before any comments in the same round, posts a comment with `crew ci-summary` saying why CI was red and what it did, and ticks the line. That comment carries `<!-- crew -->` and `<!-- crew ci-summary: <short sha> -->`, so a second `crew ci-summary` for the same head edits it. The worker names the head (`crew ci-summary TASK SHA FILE`), which a CI line must report. A round that only reports red CI doesn't count toward `MAX_REVIEW_ROUNDS`. Once a task has had `MAX_CI_FIXES` of these lines, red CI on a new head goes under Open questions instead, as `- [ ] CI is still red on <short sha> after <n> CI rounds (MAX_CI_FIXES=<n>): <checks>. Look at it yourself, or tell the worker to try again.`, and `crew tick` marks the task `needs-input`. That question is yours even in a goal's task: the goal lead isn't sent it. The lead ticks it once you've answered, and sets the task back to `pr-open` if the worker isn't resumed.
