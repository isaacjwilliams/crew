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
  templates/             task.md, goal.md, proposal.md, review-comment.md, ci-comment.md and pr-body.md
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
    paused               there while the project is paused (see Pausing)
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
| `pr-open` | PR is up and waiting for your review | worker, with `crew hand-over` the first time, which refuses until every acceptance criterion has evidence |
| `addressing-feedback` | worker handling a review round | `crew followup` |
| `merged` | PR merged | `crew sync` (and so `crew tick`) |
| `done` | a spike's report is in (see Kinds of task) | `crew hand-over` |
| `abandoned` | dropped | you or the lead via `crew set` |

"In flight" means `working`, `needs-input`, `addressing-feedback`, or `pr-open`. "Finished" means `merged`, `done` or `abandoned`; `crew cleanup --merged` and `crew tick` remove the worktree and local branch of a `merged` or `done` task. `MAX_WORKERS` caps how many worker sessions can be running at once, and a goal's `max_in_flight` caps how many of its tasks can be in flight at once (see Goals).

## Kinds of task

A task's `kind` says what it makes, and so what `crew ready` asks of its brief and what `crew hand-over` checks. A task without one is a build. `crew new --kind <kind>` sets it.

| kind | makes | its brief needs | `crew hand-over` checks, then sets |
|---|---|---|---|
| `build` | a PR | Goal, Structure, Plan, Acceptance criteria, and a `risk` | evidence for every acceptance criterion (and a security review if high risk); `pr-open` |
| `spike` | a report, from a path tried on a branch nobody merges | Goal, Plan (what to try) and Acceptance criteria (what would answer the question); no `risk` | `## Report` starts with `Answer: <one line>`; `done` |

A **spike** answers a question a draft's plan rests on. The draft lists those questions under `## Still to learn`, one `- [ ]` item each, and `crew ready` refuses it while any is unchecked. `crew new --kind spike --informs <draft>` ties a spike to the draft (`informs`), and several spikes may inform one draft. A spike is dispatched like any task, and counts as in flight, toward `MAX_WORKERS` and its goal's `max_in_flight`, while it runs. Its worker commits locally and never pushes: the code is thrown away, and the report in the ledger is the record. It writes `## Report`, before `## Automated reviews` (or before `## Log`): first the line `Answer: <one line>`, then what it tried, the evidence (commands and outputs), what it means for the plan, and surprises. `crew hand-over` then sets `done`. A goal's done spike reaches its goal lead through `crew goal followup`, once (`report_sent`); outside a goal, `crew status` shows each draft's open items and its spikes on its row (`2 to learn; 041 done, 042 working`). Whoever owns the draft writes the spikes' answers side by side under the item, ticks it, and finishes the plan.

## Risk tiers

A task's `risk` says what kind of look its PR needs from you before you merge it. The lead sets it at intake, from what the plan touches, and `crew ready` refuses a task without one.

| risk | look | when the lead sets it |
|---|---|---|
| `low` | approve from the evidence | the plan adds no structure: docs, copy, tests, a fix inside existing code |
| `medium` | read the diff | the plan adds or changes structure |
| `high` | read the diff; security review ran | the plan touches a sensitive path, or does something irreversible: a migration, a public API, a new dependency, a change to a design principle |

A project lists its sensitive paths in `SENSITIVE_PATHS`, in its `project.env`: git pathspecs, such as `db/migrate` or `:(glob)app/**/auth*`, space-separated, without quotes of their own. When a task's branch changes a file that matches one, crew raises its `risk` to `high` and logs `risk → high: touches <file>`: at `crew hand-over`, and at every `crew sync` while the task is in flight, so a commit after the hand-over raises it too. Both fetch first and look at the branch both here and on origin. Crew never lowers a tier, and `crew hand-over` refuses when it can't check, for a pathspec git rejects, say. A high-risk worker runs `/security-review` before it hands over and records what came of it as a `### Security review` entry under `## Evidence`; `crew hand-over` refuses a high-risk task without one. `crew status` says each open PR's look, `high: no security review yet` for a task raised after its hand-over, and `no risk tier: read the diff` for one marked ready before tiers existed, and lists the project's open PRs on one line, cheapest look first.

## Front-matter keys (task files)

`id`, `title`, `project`, `status`, `kind` (`build` or `spike`; empty means build; set by `crew new --kind`; see Kinds of task), `informs` (a spike's draft: the task whose question it answers; set by `crew new --informs`), `report_sent` (`yes` once a goal's done spike's report has gone to the goal lead; written by `crew` only), `goal` (the goal the task belongs to, if any; set by `crew new --goal`), `base` (the branch the task starts from and its PR targets), `base_sha` (written by `crew` only; see Stacked tasks), `restack_sent` (the commit the last `crew restack` asked the worker to rebase onto; written by `crew` only), `branch`, `depends_on` (task ids, space or comma separated), `priority` (a whole number, higher first, empty means 0; orders `crew tick`'s dispatch of running goals' ready tasks; set with `crew set`), `risk` (`low`, `medium` or `high`, the look the PR needs; set by the lead, raised to `high` by `crew`; a spike has none; see Risk tiers), `pre_pr_review` (yes/no), `pr_mode` (draft/ready/none), `worktree`, `pr`, `pr_state`, `pr_updated` (when GitHub last saw a change to the PR, from `crew sync`; written by `crew` only), `ci_state` (the checks on the PR's head commit: `running` while any hasn't finished, then `failing` or `passing`, and empty when it has none; from `crew sync`, and only `crew tick`'s filter for running `crew ci-fix`; written by `crew` only), `worker_name`, `worker_job`, `worker_session`, `worker_fingerprint` (what the worker loaded when it started; written by `crew` only, see below), `review_rounds`, `last_round_sent` (the newest round a worker was sent), `interrupted` (when a pause caught the worker mid-turn; written by `crew pause`, and cleared by the followup that tells the worker to carry on), `feedback_since` (UTC), `questions_routed` (a checksum of the worker's open questions last sent to the goal lead; written by `crew` only), `created`, `updated`.

Front matter is flat `key: value`. Edit it with `crew set` rather than by hand when a session might be writing at the same time.

## Goals

A goal is a batch of items, listed in its file or on a tracker, that the user hands the crew to work through without them. `crew goal start` launches its **goal lead**, a background session that writes and plan-reviews a brief for every item and marks each one `ready`. `crew tick` dispatches those as `MAX_WORKERS` and the goal's `max_in_flight` allow, highest `priority` first, then by project and ID, across every running goal. Every task of the goal that's in flight counts toward `max_in_flight`, each task of a stack too. An item the goal lead shouldn't decide alone is **parked**: its task stays `draft`, with the question under Open questions for you.

| status | meaning | set by |
|---|---|---|
| `draft` | Goal section being written with the lead | `crew goal new` |
| `running` | the goal lead is working through it | `crew goal start` (or a `crew goal note` to a done goal) |
| `done` | every task finished (merged, done or abandoned); goal lead and checkout removed | `crew goal done` (and so `crew tick`) |
| `abandoned` | stopped; goal lead and checkout removed, tasks left as they are | `crew goal abandon` |

Front-matter keys: `id`, `title`, `project`, `status`, `checkout` (the goal lead's working directory), `lead_name`, `lead_job`, `lead_session` (its session, like a worker's three identifiers), `lead_fingerprint` (what it loaded when it started, like a worker's; written by `crew` only), `unsent_notes` (your notes the goal lead hasn't been sent yet; written by `crew` only), `interrupted` (as a task's, for the goal lead; cleared by `crew goal followup`), `max_in_flight` (how many of its tasks may be in flight at once; empty means `GOAL_MAX_IN_FLIGHT`; set by `crew goal new --max-in-flight` and `crew goal cap`), `created`, `updated`. Sections: Goal (your words, including where the items come from), Items (written by the goal lead), Decisions (your notes from `crew goal note`, and the goal lead's own calls), Log.

The goal lead hears about three things, through `crew goal followup`, which `crew tick` runs once it's free: your notes, new questions from its tasks' workers, and its spikes' reports, each once. A question it can't answer stays unchecked, with a "For the user" line in the task's Log, and reaches you through the lead like any worker question.

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

## Pausing

`crew pause [--project P|--all] [--force]` writes `projects/<project>/paused`, a file of front matter only: `by` (`crew pause`, `crew pause --force` or `crew upgrade`), `at` and `host`. While it's there, nothing starts a session for the project: `crew dispatch`, `crew followup`, `crew restack`, `crew goal start` and `crew goal followup` refuse with exit 4, and `crew tick` sends its workers and goal leads nothing (it still records feedback, red CI and questions, and cleans up). `crew gardener` skips it. A pause leaves an existing mark alone, unless `crew upgrade` wrote it, so your own pause outlasts an upgrade.

A session that's mid-turn when the pause starts gets `interrupted` set on its task or goal file, is told to record where it is and end its turn, and is stopped if it's still mid-turn after `WRAP_UP_SECONDS`. One idle then that starts a turn during the pause gets `interrupted` from the hook that tells it to stop. `--force` stops every worker and goal lead of the project at once instead, even one whose task or goal is finished. Every session a pause stops gets `interrupted` too, unless its task or goal is finished. `crew resume [--project P|--all]` removes the file, and the next `crew tick` follows up each worker and goal lead whose file has `interrupted`, telling it to carry on.

## Stacked tasks

A task whose `base` is another task's `branch` is stacked on that task, its parent. `crew new --on TASK` sets that up (and adds the parent to `depends_on`); `crew set TASK base <branch>` does the same for an existing task. There is no other marker.

- A stacked task can be dispatched once its parent's branch is on GitHub. Its other `depends_on` entries must still be merged.
- `base_sha` is the last commit of the parent's in the task's branch. `crew dispatch` records it and `crew sync` keeps it current; a restack cuts there.
- `crew sync` and `crew status` report a stacked task that needs a restack: its parent's branch has new commits, or its parent merged. `crew restack TASK` resumes the task's worker with the rebase to run. After a merge the worker also retargets the PR and sets `base` to the branch the parent merged into, which is what ends the stacking.
- Restack from the bottom of a stack up; `crew restack` refuses a task whose parent needs one first. It also refuses to send the same restack twice (`restack_sent`): if a worker didn't manage the last one, its Open questions and log say why. `--again` sends it anyway.

## How a worker is found again

`crew dispatch` records three identifiers for the worker, and a fingerprint of what it loaded. `crew followup` uses them to resume the same session, in the same worktree, with its full history. It resumes with no launch flags: the session keeps the agent, permission mode and settings it was started with, and passing them again would make Claude Code start a copy. So when what a fresh worker would load has changed since this one started (its agent file or a subagent's, its settings or its launch flags, after an upgrade say), `crew followup` starts a fresh worker instead, and records the new fingerprint. So does a task with no `worker_fingerprint`, dispatched before crew recorded one. `--fresh` starts one whatever the fingerprint says. Resuming a session whose process is still open (a finished worker keeps one for about an hour) would also start a copy, so `crew followup` stops that process first. If Claude Code starts a copy anyway, `crew followup` records the copy, so the next followup continues from it. If that session can't be resumed (for example, its transcript was cleaned up), `crew followup` starts a fresh worker that rebuilds context from the task file and the branch. `--fresh` forces that.

| identifier | example | what it's for |
|---|---|---|
| `worker_name` | `crew-<project>-<task-id>` | shows in agent view; the `SendMessage` target while the session is busy (mid-turn, or holding a prompt open). A copy gets a different name |
| `worker_job` | short ID | used by `claude attach`, `claude logs`, and `claude stop`; `MAX_WORKERS` counts these |
| `worker_session` | full UUID | used by `claude --resume` |
| `worker_fingerprint` | 12 hex characters | a hash of the agent files, settings and launch flags the worker started with; a followup that would load something else starts fresh |

## Decisions

A task's `## Decisions` holds one dated line per decision about the task: your answers and instructions, and the leads' and the worker's own calls. Each line ends with whose call it was: `(user)` when it records your own words or answer, otherwise `(lead)` or `(worker)`. Only a `(user)` line counts as yours: a worker's automated review gives its reviewer those lines as your decisions, and no others.

## Evidence

A task's `## Evidence`, just before `## Automated reviews`, is where its worker shows that the change works: one `### <n>.` entry for each top-level item of `## Acceptance criteria`, in order, holding the command that shows it and its output (or, where only a look shows it, what the worker saw and how; or, for a check only you can make, what to try). The worker records it before opening its PR, builds the PR body's Evidence from it, and keeps it true after automated review and review rounds. `crew hand-over`, the worker's first move to `pr-open`, refuses until every criterion has an entry with something under its heading: it counts them, and doesn't judge them. So that each criterion has a number, `crew ready` refuses criteria that aren't `- ` items. Later returns to `pr-open`, after a review round or a restack, are plain `crew set`s.

## Automated reviews

After opening its PR, a worker reviews it with its `pr-review` skill, up to `AUTO_REVIEWS` times: the first *pass* of automated review. When a review round asks for another (below), it runs another pass, of up to `AUTO_REVIEWS` reviews and at least one. A pass is the reviews after the last `Summary posted` line, and numbers carry on across passes. The worker records each review in the task file, under `## Automated reviews` (just before `## Review rounds`), as `### Automated review N`. The record holds the loop's whole state, so a worker resumed or compacted in the middle carries on from it:

- the range reviewed: `<from sha>..<HEAD sha>`, what it starts from (`since <base branch>` for a review that read the whole PR, or `since review N` for one that read only the commits since review N's head) and the fork point (`fork point <sha>`). The first review reads the whole PR; a later one reads only the new commits, unless the branch was rebased or its base moved. The reviewers (the Claude reviewer, and Codex when `CODEX_REVIEW` is on; each marked `failed` when it didn't report), how many rounds they had between them, and each one's derived intent and reading of the stated intent;
- every finding a reviewer raised, as a `####` heading with its severity, and lines for who raised it (`both`, `Codex` or `Claude reviewer`), whether it's `new` or a `repeat of review N #k` (only a reviewer marks a repeat; the worker may reject the mark, never add one), the finding (quoted), each round's replies to it (quoted), the worker's verdict and evidence, the agreed severity, and the outcome. Besides the rubric's four, an agreed severity can be `pre-existing: <severity>` (every reviewer that holds it tagged it `pre-existing`, in its report or a round, and the worker found the same failure at the fork point), `withdrawn` (its raiser took it back in a round, and no reviewer holds it), `split` (one reviewer holds it and the other refutes it; the worker settles it), `flipped: <severity>, was <earlier> in review N` (a repeat that an earlier review settled as not a defect, and a reviewer now holds at must fix or should fix), `disputed` (the worker disagrees, with evidence, and no reviewer does) or `uncertain`. The worker fills in the outcome once it has acted:
  - `fixed in <sha>`: a must fix or should fix, or a fix-later or nitpick finding in the PR's lines; with `(<n> instances)` when it fixed more than one instance of the finding's class, and `; left for the user: also at the fork point, <path:line>, …` when it found old instances no finding covers;
  - `proposal <file name>`: a fix-later finding outside the PR's lines;
  - `recorded`: a nitpick outside the PR's lines, or a pre-existing nitpick;
  - `withdrawn in round <n>`;
  - `not a defect: <evidence>`: a split it set aside;
  - `left for the user`: a finding it disputes or can't settle, or one the brief settled;
  - `left for the user: pre-existing, <the fix's size>; proposal <file name>`: a defect that already failed the same way at the fork point;
  - `left for the user: verdict changed since review N`: a flipped finding;
  - `left for the user: still open from review N`: a repeat of a finding an earlier review left for the user, which the user hasn't decided since;
  - `left for the user: <the cost>`: a must fix that only a deploy step or the docs could close. The cost says what fails, for whom, and for how long;
  - `left for the user: <the fix's cost>`: a fix-later finding in the PR's lines whose fix is too big for the PR. The cost says the fix's size and what it touches;
- each reviewer's conclusion and what they couldn't check;
- `Summary posted`, under the newest review, once the worker has posted the pass's summary comment with `crew review-summary`. That comment is filled in from `templates/review-comment.md` and this record alone. It carries `<!-- crew -->`, so `crew feedback` skips it, and `<!-- crew review-summary: review N -->`, N the newest review, so a second `crew review-summary` for the same review edits it instead of posting another.

A pass that has run all its reviews, and still needed fixes for the last one, didn't converge. Its summary proposes smaller pieces instead, the PR isn't marked ready, and the worker asks under Open questions whether to split the branch into those pieces (`split as proposed`, `review it anyway` or `take it as it is`). A worker asks the same question before it opens a PR when its branch is past `MAX_PR_LINES`. The answer goes under Decisions, like any other.

## Review rounds

`crew feedback TASK` pulls the PR comments that are newer than `feedback_since`. *Your* comments are appended as `### Round N`. Collaborators' comments (people with write access, not bots) go under Open questions as unchecked items, for you to decide on; nobody acts on them until you do. Anyone else's are ignored. Each comment and review summary is listed with its ID. Workers react to them with `crew react` (👀 when they pick a round up, 🚀 before changing code for a comment), and answer them with `crew reply`: an inline comment in its thread, and a conversation comment or review summary with a PR comment that quotes it (GitHub has no threads for those). `crew reply` refuses an ID that isn't on the task's own PR. It appends a hidden `<!-- crew -->` marker so later `crew feedback` runs don't read those replies back as new feedback (the replies are posted under your GitHub account).

A round can also ask for another pass of automated review, with the line `- [ ] Automated review requested (<source>)`, last in the round: one line for each request, and one pass answers them all. The source is `comment <id>`, for your PR comment whose whole text is `crew review` (which the round lists in place of the comment), or `crew review`, for a round of its own that the `crew review TASK` command appends. The worker runs the pass after the round's comments, and ticks the line; the pass's summary comment answers it. A round that only asks for a pass counts toward `MAX_REVIEW_ROUNDS` like any other.

When CI goes red on a PR its worker has handed to you (`pr-open`), `crew tick` runs `crew ci-fix TASK`, which appends a round of its own holding one line: `- [ ] CI failed on <short sha>: <check>, <check>`. It does so once every check on that head commit has finished, and once for each head: the line is the record, and no key keeps it. The worker deals with it before any comments in the same round, posts a comment with `crew ci-summary` saying why CI was red and what it did, and ticks the line. That comment carries `<!-- crew -->` and `<!-- crew ci-summary: <short sha> -->`, so a second `crew ci-summary` for the same head edits it. The worker names the head (`crew ci-summary TASK SHA FILE`), which a CI line must report. A round that only reports red CI doesn't count toward `MAX_REVIEW_ROUNDS`. Once a task has had `MAX_CI_FIXES` of these lines, red CI on a new head goes under Open questions instead, as `- [ ] CI is still red on <short sha> after <n> CI rounds (MAX_CI_FIXES=<n>): <checks>. Look at it yourself, or tell the worker to try again.`, and `crew tick` marks the task `needs-input`. That question is yours even in a goal's task: the goal lead isn't sent it. The lead ticks it once you've answered, and sets the task back to `pr-open` if the worker isn't resumed.
