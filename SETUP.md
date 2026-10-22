# Agent crew: setup and usage

Six Claude Code agents, a ledger that holds all coordination state outside the repo, and a small CLI (`crew`) that ties them together. You work in two places: **GitHub**, where you review and merge, and **the lead**, which you talk to about everything else, at a terminal or on your phone.

Everything lives in one private git repo of yours, your **crew home**, cloned to `~/crew`: the crew's code, your settings, and the ledger. It's a copy of the public crew repo, [isaacjwilliams/crew](https://github.com/isaacjwilliams/crew), and pulls new versions from it. Any machine with a clone can run the crew, one at a time.

```
          you ──── talk ────▶  lead  (a session in ~/crew/lead, for every project; on the phone: crew)
           │                    │  writes briefs, dispatches, routes feedback
           │                    │  └─ plan-reviewer pass on each brief
           │                    ▼
           │           ~/crew   (your crew home: crew, config, and the ledger; pushed to your private repo)
           │                    │  crew dispatch / crew followup
           │                    ▼
           │      implementer workers   (background sessions, one per task,
           │                    │         each in ~/agent-work/<project>/<task>)
           │                    │  ├─ casual-observer pass before the PR
           │                    ▼  └─ push branch, open draft PR
           └── review/merge ── GitHub PRs
                                   ▲
       gardener (weekly timer) ────┘ reads merged commits → proposals in the ledger
       crew tick (every 3 minutes)   your PR comments → the worker; collaborators' → you;
                                     restacks, cleanups; a goal's ready tasks → workers
       goal lead (one per goal)      plans every item of a goal you started, without you
```

**How the lead talks to the right worker.** Every task file records its worker's session name, job ID, and session ID. "Address my comments on #42" becomes: find the task whose `pr` is #42, append your new comments to it as a review round, then resume *that* session in *that* worktree. The lead never has to remember anything. If the session no longer exists, a fresh worker picks up from the task file and the branch.

**What your collaborators see:** branches pushed under your `BRANCH_PREFIX` (a worker pushes after every commit, so a repo whose CI runs on every push runs it on those branches too), draft PRs, and (optionally) one-line replies to your own review comments, all under your GitHub account. Nothing else: no labels, no other comments, no files in the repo. `crew project add` only adds two lines to `.git/info/exclude`, which is local to your clone.

---

## 1. Requirements

- **Packages** (CachyOS): `sudo pacman -S --needed git github-cli fish curl`.
- **Linux**, on x86_64 or aarch64. macOS isn't supported yet. You don't need Go: `crew` is one binary, which `~/crew/bin/crew` downloads from the release for your home's version the first time it runs, into `~/crew/bin/release/`.
- **Claude Code 2.1.281 or later**: run `claude update`. The crew relies on background sessions (agent view), automatic `AGENTS.md` loading, and `--permission-prompts`.
- **GitHub CLI signed in**: run `gh auth login`. Workers push and open PRs as you.
- **A plan with auto mode** (Pro, Max, or Team) is recommended. The lead and workers use it by default, so they aren't stopped for every new shell command. See `WORKER_PERMISSION_MODE` and `LEAD_PERMISSION_MODE` below.

## 2. Install

### Your first machine

Everyone, you or a colleague, starts from the public repo and gets a private crew of their own. Clone it to `~/crew` and set it up:

```fish
git clone https://github.com/isaacjwilliams/crew ~/crew
~/crew/bin/crew setup
```

`crew setup` makes a private GitHub repo, `<you>/crew` (`--name` for another name, if you have a `crew` already), and turns the clone into yours: the private repo becomes `origin`, where your ledger is pushed, and the public one `upstream`, where new versions come from. Pushing to `upstream` is disabled, so your ledger can never reach the public repo. It writes your settings file, `config/crew.env`, from `templates/crew.env`, then runs `crew host up`, which puts this machine's files in place with `crew install` (below) and runs the crew here: it claims the lease and enables the timers and the lead's service.

### Another machine of yours

Clone your private repo, not the public one: `git clone <your private repo> ~/crew`. Then:

- to have it run the crew, from now on: `~/crew/bin/crew host up --take`. Use this when the first machine is down. For a planned move, run `crew host down` on the old machine first, then `crew host up` (no `--take`) here. See [Which machine runs the crew](#which-machine-runs-the-crew);
- to only read it, the ledger and the lead's briefs, say: `~/crew/bin/crew install`. That installs the agents and the fish files and leaves the timers off, and `crew status` there shows what the other machine is doing.

### What `crew install` does

| Installed to | What |
|---|---|
| `~/.claude/agents/{lead,implementer,casual-observer,plan-reviewer,gardener,goal-lead}.md` | Agent profiles (user-level, so they're project-agnostic and not in any repo) |
| `~/.config/fish/conf.d/agent-crew.fish` | Puts `crew` on your `PATH` |
| `~/.config/fish/functions/lead.fish` | The `lead` command |
| `~/.config/fish/completions/crew.fish` | Tab completion, including task IDs |
| `~/.config/systemd/user/crew-gardener.{service,timer}` | Weekly gardener |
| `~/.config/systemd/user/crew-tick.{service,timer}` | `crew tick` every 3 minutes |
| `~/.config/systemd/user/crew-lead.service` | The lead for your phone (see below) |

`crew install` leaves the units off; `crew host up` turns them on for the machine that runs the crew, and `crew host down` turns them off again.

It also sets `env.CREW_HOME` in `~/.claude/settings.json`, so the agents' hooks find the home, and asks whether to load `CLAUDE.md` and `AGENTS.md` together (see section 4). For a home somewhere other than `~/crew`, it points the fish and systemd files at it.

To upgrade, run `crew upgrade` on the machine that runs the crew. It moves your home to the public repo's newest release: its newest `v<version>` tag, not whatever is on its main branch. It downloads that release's binary first, and if the release's binaries aren't up yet, it says so and changes nothing. Then it merges the release into your home, installs it with the new version's `crew install`, pushes, restarts the lead's server, removes the old version's binary, and says which version you're on. Your ledger and settings are paths the public repo never has, so they never conflict. If an edit you made to crew's own files does, `crew upgrade` stops with the merge in progress and says how to finish or undo it. It refuses a home with uncommitted changes.

**Releases** are made in the public repo. Pushing a tag `v<version>` that matches its `VERSION` file runs a GitHub Actions workflow, which runs the tests, builds the Linux binaries and publishes them as that tag's release, with the tag's message as the release notes. A private home that pushes a tag builds nothing. If you run a fork of crew for others, change the release URL in `bin/crew` to your fork's.

Re-running `crew install` on its own is safe, and it's how changes to the agents, fish files and systemd units reach a machine.

- Files that changed are replaced; files the home no longer ships are removed.
- Anything replaced or removed is backed up to `~/.local/share/agent-crew/backups/<timestamp>/`.
- It changes nothing tracked in the home: your config, tasks and proposals stay as they are. It does write the lead's per-machine settings, `lead/.claude/settings.local.json`, which git ignores.
- `crew doctor` shows the home's version (its `VERSION` file), and checks that the binary in use is that version.

Options:

| Option | Effect |
|---|---|
| `--yes` | Non-interactive |
| `--no-fish`, `--no-services` | Skip those parts: the fish files, or the crew's services (systemd units) |
| `--skip-agents-md-setting` | Don't ask about loading `AGENTS.md` |

Open a new fish shell afterwards.

### The lead on your phone

`crew host up` also enables `crew-lead.service`, which runs `crew serve`: the lead as a Remote Control server named `crew`, on the machine that runs the crew. In the Claude app, open the Code tab and pick `crew` to talk to the lead from anywhere.

- The lead waits for you to speak first: on the phone it doesn't open with a briefing, as `lead` does in a terminal. Say "Status" for one.
- Sessions you start from the phone are throwaway: a session started that way hangs once the service restarts (after `crew upgrade`, say), so start a new one. The lead keeps everything in the ledger, so nothing is lost.
- Workers don't show on the phone, because background sessions don't. Ask the lead about them.

Two first-time prompts need a terminal on that machine, once. Until they're answered the service can't start the lead, and systemd retries it every 30 seconds:

1. Trust the lead's folder: `cd ~/crew/lead; and claude`, accept the trust dialog, `/exit`.
2. Enable Remote Control, if you never have on this machine: run `crew serve` yourself, answer its question with `y`, then Ctrl+C and `systemctl --user restart crew-lead.service`. If `systemctl --user status crew-lead` shows the service active after step 1, it's enabled already, and `crew serve` would be refused while the service runs.

### Which machine runs the crew

One machine runs the crew at a time: it dispatches workers, runs the timers, and pushes the ledger to your private repo as it changes. Other machines with a clone of the home can read it (`crew status`) but change nothing.

```fish
crew host               # which machine runs the crew, since when, and this machine's name
crew host up            # run it here: crew install, claim the lease, clone missing projects, enable the timers
crew host up --take     # take it over from another machine, say one that's down
crew host down          # hand over: stop the crew here and release the lease
```

`crew host down` refuses while a worker's branch has commits it hasn't pushed, so nothing is left behind; `--force` hands over anyway. A machine whose lease was taken stops at its next `crew tick`: it turns its timers off, stops its workers, and keeps anything origin didn't get on a branch `lost/<host>-<time>` in the home. The machine's name is `CREW_HOST`, or when that isn't set, `hostname -s` on Linux and `scutil --get LocalHostName` on a Mac (the Local hostname in System Settings → General → Sharing, which doesn't change with the network).

## 3. First-time configuration

1. **Edit your settings file**, `~/crew/config/crew.env`, which `crew setup` made from `templates/crew.env`, then commit and push it (`git -C ~/crew commit -am "My settings"; and git -C ~/crew push`). Anything you set there overrides `config/defaults.env`, which lists every setting with its default; leave that file as it is. At minimum:
   - Set `BRANCH_PREFIX`, e.g. `isaac/`. These branches land on the shared remote.
   - Check `MAX_WORKERS` (concurrent sessions) and `MAX_IN_FLIGHT` (dispatched but unmerged tasks, open PRs included; a stack of PRs counts once). `MAX_IN_FLIGHT` is the knob that keeps you from being buried in reviews. Start low; raise it once the loop feels trustworthy.
   - If the app needs untracked files like `.env.local` to run tests, list them in `WORKTREE_COPY`.
2. **Trust two folders once.** Background sessions check workspace trust before starting, and so does the lead:
   ```fish
   cd ~/agent-work; and claude     # accept, then /exit
   cd ~/crew/lead; and claude      # accept, then /exit
   ```
3. **Register your repo:**
   ```fish
   cd ~/code/webapp
   crew project add
   crew doctor
   ```
   `crew project add` records the repo's remote and default branch in `~/crew/projects/<owner>-<repo>/project.env`, where any setting can be overridden for that project. Your clone's path goes in `local.env` beside it, which isn't committed: each machine has its own clone. `crew doctor` checks versions, `gh` auth, agent files, trust, the AGENTS.md setting, and that each enabled unit can find the tools `crew` needs.
4. **If the repo uses [Worktrunk](https://worktrunk.dev)** for per-worktree setup (databases, services, dependency installs), see [Repos that use Worktrunk](#repos-that-use-worktrunk) below.
5. **Optional:** add branch protection on the default branch, if the repo's owner allows it. The agents are told never to merge or push there, but branch protection is the only thing that makes it impossible (see section 8).

### Repos that use Worktrunk

By default `crew dispatch` creates task worktrees with plain `git worktree add`, which runs none of the repo's Worktrunk hooks. To have it use `wt` instead:

1. Add this to `~/crew/projects/<owner>-<repo>/project.env`:
   ```bash
   WORKTREE_TOOL="worktrunk"
   ```
   `crew dispatch` then runs `wt switch --create`, so the repo's `pre-start` hooks run before the worker starts. `crew cleanup` runs `wt remove`, so its `pre-remove` hooks tear the resources down. Worktrees still live in `~/agent-work/<project>/<task>`, and `wt list` and `wt switch <branch>` work on them as usual.
2. **Approve the hooks once**, from the repo: `wt config approvals add`. `crew` never approves hooks for you. If a hook is new or has changed since you approved it, `crew dispatch` stops with a message and starts no worker.
3. **Put setup that the worker needs in `pre-start` hooks**, not `post-start`. `post-start` hooks run in the background, so the worker could start before they finish.

Hook output goes to `~/crew/logs/worktree-<project>-<task>.log`, not your terminal. Setup hooks can take minutes, and `crew dispatch` waits for them. The lead knows to allow for that, and `crew` doesn't hold the ledger lock meanwhile, so running workers aren't blocked. The gardener's checkout always uses plain git; it only reads code.

### Worker settings for one project

To give one project's workers extra settings, such as `allow` rules for its test runner or `env` variables, put them in `~/crew/projects/<owner>-<repo>/worker-settings.json`. For example:

```json
{
  "permissions": {
    "allow": ["Bash(bin/rspec *)", "Bash(bin/rails *)"]
  }
}
```

`crew` adds this file to `config/worker-settings.json` for that project's workers. It can only add: lists are appended and new keys added, but it can't change a value the shipped file sets or remove a `deny` rule.

## 4. AGENTS.md: how project conventions reach every agent

You don't have to wire anything. Claude Code 2.1.277 and later reads `AGENTS.md` automatically, and every crew agent but the lead runs inside the repo or one of its worktrees:

| Agent | Where it runs, so where AGENTS.md comes from |
|---|---|
| lead | `~/crew/lead`, outside every repo: it reads `AGENTS.md` itself, in its `crew checkout` copy |
| plan reviewer | a subagent of the lead: it reads `AGENTS.md` at the task's base through `git -C` |
| workers | their worktree, which contains the committed `AGENTS.md` |
| casual observer | as a subagent, which loads the same project instructions |
| gardener | a detached checkout of the default branch |

Subdirectory `AGENTS.md` files load when an agent reads files there.

The catch is **precedence**. By default Claude reads `AGENTS.md` only when there is no `CLAUDE.md`, `.claude/CLAUDE.md`, or `CLAUDE.local.md` in the working directory or above it. If the repo has both files, only `CLAUDE.md` loads. `crew install` offers to switch this to `claude-md-and-agents-md`, which loads both. You can also change it in any session with `/config` → **Project instructions**. It's stored in `~/.claude/settings.json` under `pluginConfigs."agents-md@builtin"`.

Two details:
- Creating a `CLAUDE.local.md` in the repo also stops `AGENTS.md` from loading under the default setting.
- To confirm what loaded, run `/memory` in a lead session and look for the `AGENTS.md` path.

**Your personal taste goes in your user-level files, not the shared AGENTS.md.** `~/.claude/CLAUDE.md` and `~/.claude/rules/*.md` load alongside `AGENTS.md` in every project and every agent. Use them for the conventions you distil from your review comments ("prefer X over Y", "new data fetching goes through Z"), which you'd otherwise repeat on every PR. The implementer has no coding standards of its own. Everything it knows about your taste comes from these files, so if you have none, it works from the repo's `AGENTS.md` alone.

## 5. Daily use

### Start work: talk to the lead

```fish
lead            # new session, from any directory; opens with a short briefing
lead -c         # continue the most recent lead conversation
lead -r         # pick an earlier conversation to continue
```

One lead covers every project you've registered. It runs in `~/crew/lead`, whose settings make the session the lead, so `cd ~/crew/lead; and claude` is a lead too. Say which project you mean when more than one could fit; the lead asks otherwise. It reads each project's code in its own copy, `~/agent-work/<project>/_lead`, which `crew checkout` keeps on the default branch, and reads the project's `AGENTS.md` there itself, since it isn't loaded for a session outside the repo. For the same reason it gets your user-level MCP servers but not a repo's own (`.mcp.json`); that repo's workers do.

**One conversation or many.** The lead keeps nothing important in its conversation. It opens a draft task as soon as you agree on a goal and writes each decision into it as you agree it, so any lead session can pick up where another left off ("let's continue 007"). Keep one long conversation if you like: Claude Code compacts it when it fills up, and after a compaction the lead rereads the task files rather than trusting its summary. Start a fresh `lead` when:

- you've just run `crew install`. A continued conversation keeps the instructions it started with until it's next compacted, so it won't know about changes to `lead.md`;
- `lead -c` opens the wrong conversation. It takes the most recent one in `~/crew/lead`. Use `lead -r` to choose.

Every lead session is named `lead`. If one is already running, Claude Code gives the new one a variant of that name.

A typical exchange:

> **you:** Saved searches should remember the filter panel state.
> **lead:** *(greps and reads the search module)* Filters live in `FilterPanel` local state and never reach `SearchQuery`. Proposal: add an optional `filters` field to `SearchQuery`, persist it in the existing `saved_searches.query` JSON column (no migration), and hydrate `FilterPanel` from it. One question: should the old saved searches without filters open with the panel cleared or with defaults?
> **you:** Defaults. Go.
> **lead:** Brief written as `007-saved-search-filters` and dispatched as worker `3f9c21aa`. It'll appear in agent view with a PR link once the draft is up.

The lead writes the brief in `~/crew/projects/<project>/tasks/007-saved-search-filters.md`. You can read or edit it any time; it's the contract the worker works from.

For anything bigger than this example, the lead shows you a summary before you say go. Its Goal quotes how you described the problem, and its structure line says where the new code lives and which alternative lost. While you read it, the plan-reviewer agent reads the brief cold, with the code but without the conversation. When it finishes, the lead tells you what it raised and how each point was settled. If you say go before then, the lead dispatches once the review is in, unless it raises something for you to decide. Check these hardest: workers and reviewers follow the brief and won't reopen its design. When a reviewer does question that design, the worker leaves the code alone and lists the point under "Findings the brief settled", and the lead passes it on with the PR.

### Watch the workers

```fish
claude agents   # agent view
```

Each worker is a row named `crew-<project>-<task>`. You'll see:

- live state;
- **Needs input** when a worker has a question or a permission prompt;
- a `#123` link once its PR exists.

Keys: `Space` peeks and lets you reply without leaving the list, `Enter` attaches, `←` detaches. Answering a worker directly here is fine; it records decisions in its task file.

### Let `crew tick` keep things moving

`crew tick` does everything that needs no decision from you, across all your projects:

- syncs PR states from GitHub;
- records your new review comments as a round and resumes the task's worker with it as soon as the worker is free: not mid-turn, and not holding a prompt open. A worker that ended its turn on a question for you is free; your answer is what it's waiting for. While a round waits, the tick says so;
- records collaborators' comments under the task's Open questions, for you;
- marks a task `needs-input` once its worker is idle and an Open question waits on you;
- restacks stacked tasks whose parent moved or merged, bottom-up, never sending the same restack twice;
- cleans up merged tasks;
- gives a fresh worker to a task in progress that moved here from another machine, since its old session stayed there;
- for a goal you started (below): dispatches its ready tasks as the caps allow, passes its goal lead your notes and its workers' questions, and closes the goal once every task is merged or abandoned.

It starts a model session only when it resumes a worker or a goal lead. `crew host up` turns its timer on, on the machine that runs the crew. To keep the timers and the lead's service running while you're logged out, and to read what a tick did:

```fish
loginctl enable-linger $USER     # once per machine
journalctl --user -u crew-tick   # one line per thing it did
```

The lead runs `crew tick` at the start of a session and whenever you ask how things are going, so it works without the timer too, only less promptly.

### Hand the crew a goal

For a batch of work you'd rather not plan item by item (a list, or a column on your tracker), give the lead a goal:

> **you:** Work through the Season column on the board. Leave anything about payments alone.
> **lead:** *(writes the goal file, quoting you)* Goal `001-season-column`: every card in Season, nothing touching payments. Start it?
> **you:** Go.

The lead runs `crew goal new` and `crew goal start`. A **goal lead** then does the lead's intake for every item on its own, in a background session named `crew-<project>-goal-<goal>`: a brief to the usual standard, a plan review, the findings settled, and `crew ready`. `crew tick` dispatches the ready tasks as `MAX_WORKERS` and `MAX_IN_FLIGHT` allow. You see the work as PRs, and review them as usual.

- **Parked items** are the decisions the goal lead leaves to you: anything irreversible (a migration, a public API change, a new dependency), anything outside the goal as you wrote it, an unclear item, or a plan-review finding it can't settle. Each stays a draft task with its question under Open questions, and the rest of the goal carries on. The lead tells you about them; your answer goes back with `crew goal note`.
- **Workers' questions** go to the goal lead first. Ones it shouldn't answer reach you through the lead, like any worker question.
- **Telling the goal lead something** ("pick up the cards added since", "use the new layout") is `crew goal note GOAL "…"`, usually through the lead. It reopens a finished goal too.
- **Stopping a goal** is `crew goal abandon GOAL`. Its tasks stay as they are.
- The items come from where your Goal says. For a tracker, the goal lead reads it with the tools its session has (your connectors, or the repo's MCP servers) and changes nothing there. How to reach a project's tracker belongs in that repo's `AGENTS.md` or in the goal itself.

`crew status` lists each goal with its tasks under it. The goal file, `projects/<project>/goals/NNN-<slug>.md`, records your words, the goal lead's list of items and their tasks, and your notes.

### Check status

Ask the lead "what's going on?", or from fish:

```fish
crew status        # current project (or all, outside a repo)
crew tick          # sync with GitHub and do whatever needs no decision (see below)
```

### Review and send feedback

Review in GitHub as you normally would: submit a review, or leave a PR comment. With the tick timer on (below), that's all: within a few minutes `crew tick` records your comments as "Round N" in the task and resumes the worker that wrote the PR, as soon as it's idle. Without the timer, tell the lead "I reviewed #42", or run `crew tick` yourself. The two steps it does are also commands of their own:

```fish
crew feedback '#42'    # YOUR new comments → "Round N"; collaborators' → Open questions, for you
crew followup '#42'    # resumes the same worker session on that round
```

The worker makes the changes as new commits (no force-push), pushes, replies to each of your comments with what it did (set `REPLY_TO_THREADS=no` to turn this off). An inline comment gets a reply in its thread; a conversation comment or review summary gets a PR comment that quotes it, and marks the task `pr-open` again.

Collaborators' comments (people with write access; not bots) aren't sent to the worker. `crew feedback` lists them under the task's Open questions, `crew tick` marks the task `needs-input`, and the lead asks you what to do about each one: have the worker act on it, reply yourself (or have the lead post your reply), or leave it. Comments from anyone else are ignored.

### After merging

`crew tick` notices the merge and cleans up. By hand:

```fish
crew sync; and crew cleanup --merged     # or just tell the lead
```

This stops the worker and removes its agent-view row, ends any background process still running in the task's worktree (a dev server the worker started, say; your own terminals there are left alone), then removes the worktree and its local branch. The task file stays in the ledger as history.

### Stacked tasks

When one piece of work builds on another that isn't merged yet, the second task can be stacked on the first: its branch starts from the first task's branch, and its PR targets that branch, so each PR shows only its own diff. Ask the lead to stack it, or:

```fish
crew new "Season admin screens" --branch season-admin --on 044   # based on 044's branch, depends_on 044
crew dispatch 045        # works as soon as 044's branch is on GitHub; 044 needn't be merged
crew status              # a stack is listed bottom first, each task under its parent
```

```
  044-separate-season-management     pr-open        #311   (open)
  └ 045-season-admin                 pr-open        #312   (draft)
    └ 046-season-picker              working        -
```

- **Trying a stack locally.** The top task's branch contains everything below it. Its worktree is `crew get 046 worktree`, or check out `crew get 046 branch` yourself.
- **When a lower PR changes** (a review round on 044, say), `crew sync` reports `045-…: needs a restack, 044-… moved`. `crew tick` then restacks 045 once its worker is idle: the worker rebases its own commits onto 044's new tip and force-pushes its branch with a lease. Once that's pushed, 046 is next. `crew restack 045` does the same by hand. Restacks go bottom-up, and the same restack is never sent twice (`--again` overrides that).
- **When you merge the bottom PR**, `crew sync` reports `needs a restack, 044-… merged`, and `crew tick` has 045's worker rebase onto the default branch, cutting away 044's original commits (which a squash merge replaced), retarget the PR, and record the new base. Wait for that before you review or merge 045: until then its PR still shows 044's commits. Merge bottom-up.
- **A restack force-pushes the task's branch.** If you have it checked out somewhere, reset to the pushed branch rather than pulling.
- **Let GitHub delete merged branches** ("Automatically delete head branches" in the repo settings) if you can: GitHub then retargets the next PR to the default branch itself. Don't delete an unmerged branch that has PRs stacked on it; GitHub closes them.
- **`MAX_IN_FLIGHT`** counts a stack once. `MAX_WORKERS` still counts every running worker.
- To stack a task that already exists, set its base to the parent's branch: `crew set 046 base (crew get 045 branch)`. Leave `depends_on` as it is.

## 6. The casual observer on its own

Workers run it automatically before opening a PR. You can also use it anywhere:

- **In any Claude Code session:** `@"casual-observer (agent)" review this branch against origin/main`
- **Headless, from fish:** `crew observe` (current checkout) or `crew observe 007` (a task's worktree). Add `--base origin/some-branch` to compare against another base.
- **On a collaborator's PR:** `claude --worktree "#123"`, then ask it to have the casual-observer review the change.

It runs on Sonnet at medium effort on purpose: a smaller model with a narrow context budget is a good stand-in for a teammate skimming the PR. Change `model:` in `~/.claude/agents/casual-observer.md` if you prefer.

## 7. The gardener

Run it by hand a few times before scheduling it, so you can tune how picky it is:

```fish
cd ~/code/webapp; and crew gardener     # reviews commits merged since the last run (first run: 14 days)
crew proposals                          # or ask the lead to summarize them
crew promote 2026-09-29-extract-retry   # turns a proposal into a draft task; the lead then runs intake
```

Proposals live in `~/crew/projects/<project>/proposals/`. The gardener writes at most three per run and skips anything already proposed, accepted, or rejected.

`crew host up` schedules it (weekly, Monday 06:00). To pause it, `systemctl --user disable --now crew-gardener.timer`; the next `crew host up` turns it on again.

```fish
journalctl --user -u crew-gardener    # run history; full logs in ~/crew/logs/
```

If your `origin` uses SSH with a passphrase-protected key, the timer won't have your ssh-agent. The gardener then warns and reviews your last-fetched `origin/<default>`. Either live with that, or use an HTTPS remote with `gh auth setup-git`.

## 8. Safety model

The crew enforces **where agents edit files** mechanically. For shell commands it gives the lead and workers the same trust as your own sessions in auto mode, and it relies on **instructions** for what they do on GitHub.

| Rule | Enforced by |
|---|---|
| Workers only change files in their own worktree (plus the ledger, temp dirs and tool caches) | `crew-guard` blocks Edit/Write anywhere else, for the worker and any subagent it spawns (the hook is in both `implementer.md` and `worker-settings.json`). Shell commands aren't confined: the implementer's instructions set the same limit, and auto mode's classifier stops risky commands |
| The gardener and `crew observe` don't change your code | They run only the read-only commands in `readonly-settings.json`; anything else is denied without a prompt. `crew-guard` keeps the gardener's edits to the ledger |
| The lead only edits the ledger | `crew-guard` on Edit/Write, for the lead and its subagents. Its shell commands run in auto mode (`LEAD_PERMISSION_MODE`) |
| On GitHub, workers only push their own branch (`--force-with-lease` allowed), open PRs and edit them | The implementer's instructions, in a bold "On GitHub, you may do exactly this" section. `deny` rules in `config/worker-settings.json` catch `gh pr merge/close/review` and `git push --force`/`-f` |
| Nobody but you merges, approves, or closes PRs | Agent instructions and those `deny` rules. Branch protection, if you can turn it on, makes it impossible |
| Goal leads only edit the ledger, and never dispatch | `crew-guard` on Edit/Write, as for the lead. Their dispatches go through `crew tick`, which runs `crew dispatch` |
| Your review capacity isn't exceeded | `crew dispatch` enforces `MAX_WORKERS` and `MAX_IN_FLIGHT`, for the lead and for `crew tick` alike |

The GitHub rules are instructions, not mechanical limits. Parsing shell commands to enforce them can't be made complete, so the crew doesn't try. The `deny` rules catch honest mistakes, not an agent that is trying to get around them. Every GitHub action happens under your account, so treat the crew like a junior teammate with your credentials.

Nothing runs in Claude Code's sandbox, and the network isn't restricted. Workers can reach any host and the local services your tests use (Postgres, Redis, docker). The sandbox was tried and dropped: on Linux it blocks `localhost`, which broke test suites that need a local database. See "Bash: no sandbox" in `ARCHITECTURE.md`.

## 9. Configuration reference (`config/crew.env`)

Set these in `config/crew.env`; the defaults below come from `config/defaults.env`.

| Key | Default | Meaning |
|---|---|---|
| `MAX_WORKERS` | 3 | Worker sessions running or waiting on you at once |
| `MAX_IN_FLIGHT` | 5 | Dispatched, unmerged tasks, including open PRs; a stack counts once |
| `BRANCH_PREFIX` | `agent/` | Prefix for task branches |
| `WORK_ROOT` | `~/agent-work` | Where worktrees live |
| `WORKER_PERMISSION_MODE` | `auto` | `auto`, `acceptEdits`, or `default` |
| `WORKER_AUTOCOMPACT_WINDOW` | `400k` | How full a worker's conversation gets before Claude Code compacts it: 100k to 1M tokens, or `auto` for Claude Code's default (about 967k on a 1M model). Applies to workers started after you change it |
| `LEAD_PERMISSION_MODE` | `auto` | For the `lead` function and goal leads: `auto`, `acceptEdits`, or `default` |
| `PRE_PR_REVIEW` | `yes` | Default for new tasks: casual-observer pass before the PR |
| `PR_MODE` | `draft` | Default for new tasks: `draft`, `ready`, or `none` (push only) |
| `REPLY_TO_THREADS` | `yes` | Workers reply to your review comments: in the thread, or quoting a conversation comment or review summary |
| `MAX_REVIEW_ROUNDS` | 4 | Warn after this many rounds (the plan may need a rethink) |
| `WORKTREE_COPY` | empty | Gitignored files copied into each new worktree |
| `WORKTREE_TOOL` | `git` | How task worktrees are created and removed: `git` or `worktrunk` (runs the repo's `wt` hooks). Usually set per project |
| `GARDENER_LOOKBACK` | `14 days ago` | First-run range for a project |
| `GARDENER_MAX_TURNS` | 150 | Turn limit for a gardener run |

Per-project overrides go in `projects/<project>/project.env`. `crew config` shows the effective values. Per-project additions to the worker settings go in `projects/<project>/worker-settings.json`.

Permission rules for each kind of session are in `lead/.claude/settings.json` (the lead and goal leads), `config/worker-settings.json`, and `config/readonly-settings.json`. Add `allow` rules there if a worker keeps stopping for a command you're happy to allow, such as your test runner under `default` mode.

To change agent behaviour, edit `~/.claude/agents/*.md`; running sessions pick up changes within seconds.

**MCP servers.** The lead and workers get every MCP server configured for the repo they run in: its `.mcp.json`, plus your local- and user-scope servers. A browser or an error tracker set up for one project is there for that project's crew and no other, with nothing to change in the crew. Their calls go through auto mode's classifier like shell commands. Your user-scope servers (claude.ai connectors, a payments API) reach every repo's workers too. To keep one project's workers off a server, add a `deny` rule such as `"mcp__claude_ai_Gmail"` to `projects/<project>/worker-settings.json`, or `"mcp__*"` for all of them.

## 10. Troubleshooting

- **A command says crew runs on another machine.** This machine doesn't hold the lease, so it only reads. Run `crew host` to see who does, and `crew host up --take` to move the crew here.
- **A session on the phone stopped answering.** Sessions started from the phone hang once `crew-lead.service` restarts (after `crew upgrade`, or a crash). Start a new one: the lead picks up from the ledger.
- **The phone doesn't list `crew`.** The lead service runs only on the machine that runs the crew. On that machine, `systemctl --user status crew-lead` shows why it isn't running; `crew doctor` checks the rest. The two first-time prompts in [The lead on your phone](#the-lead-on-your-phone) are the usual cause.
- **`crew upgrade` stopped on a conflict.** An edit you made to crew's own files (an agent prompt, say) conflicts with the new version. Resolve the files it lists, `git -C ~/crew commit`, and run `crew upgrade` again; or `git -C ~/crew merge --abort` to stay on the old version. While the merge is open, git won't take the crew's ledger commits: each change stays in the files, `crew` warns, and the next commit after the merge takes it. Don't leave it open long.
- **`crew tick` says it stood down.** Another machine took the lease. This one's timers are off and its workers stopped. If it says it kept work on a `lost/…` branch, those are ledger changes origin never got: compare them with `git -C ~/crew log -p lost/…` and copy over what matters on the machine that runs the crew.

- **`crew dispatch` says it couldn't start the worker.** It prints Claude Code's own output.
  - A trust error means `~/agent-work` hasn't been trusted yet; run `claude` once there.
  - A message about being inside another Claude Code session shouldn't happen, because `crew` strips `CLAUDECODE` before launching. If you see it, run the dispatch from a plain fish shell to confirm.
- **Workers keep sitting in Needs input.** They're waiting on permission prompts. Use `WORKER_PERMISSION_MODE=auto`, or add `allow` rules to `worker-settings.json`.
- **`crew dispatch` says `wt` couldn't create the worktree.** It shows the end of the hook log; the full output is in `~/crew/logs/worktree-<project>-<task>.log`. If the hooks need approval, run `wt config approvals add` in the repo. If a hook failed partway, fix the cause, remove the half-made worktree with `wt remove ~/agent-work/<project>/<task>`, and dispatch again. `crew dispatch` refuses to reuse a worktree it never finished setting up.
- **`crew dispatch` says `wt` put the branch somewhere else.** The branch already has a worktree outside `~/agent-work` (see `wt list`). Remove that worktree, or dispatch a task with a different branch.
- **`crew cleanup` (or the tick's log) says a worker is still running; skipping.** Cleanup stops a task's worker before removing its worktree, and that worker's process didn't close within 10 seconds. The worktree stays, and the next tick tries again. To finish it now, stop the session in `claude agents`, then run `crew cleanup TASK`.
- **A goal lead shows as `blocked`** in `crew status` or agent view. Its session needs you, most likely for a permission prompt (a tracker's MCP tool, say). Answer it with `claude attach <job>`. To stop that prompt coming back, add an `allow` rule for the tool to `lead/.claude/settings.json`.
- **`crew tick` says a goal's input waits for its goal lead.** The goal lead is mid-turn, often while it works through the goal's items at the start. Your note is recorded under the goal's Decisions; the tick sends it once that turn ends.
- **`crew followup` says the worker is busy.** It's mid-turn, or its process holds a prompt open, such as a permission request. A worker that ended its turn, even on a question for you, isn't busy: `crew followup` stops its process if it's still open, then resumes it. That includes a worker shown as "idle, background task running": its turn ended, but a shell it started, such as a dev server, is still running, and keeps running through the stop and the resume. A review round needs nothing from you: `crew tick` sends it once the worker is free. For anything else, let it finish, answer it in agent view, or have the lead `SendMessage` it by its session name.
- **`crew restack` says the task has no `base_sha`.** The task was dispatched before `crew` recorded one, and its parent's branch is already gone from `origin`, so `crew sync` couldn't work it out. Find the last commit of the parent's in the task's branch (`git log` in its worktree) and run `crew set TASK base_sha <sha>`.
- **`crew restack` says the branch looks restacked already.** The worker rebased but didn't record the new base. If the PR and branch look right, run the `crew set TASK base …` it suggests.
- **`crew followup` says there's nothing new.** Run `crew feedback` first, or pass `--note "..."`. This stops a worker from redoing a round it already handled.
- **`crew followup` says it's starting a fresh worker.** The original session couldn't be resumed, or its job failed straight away. Usually its transcript is gone: transcripts are deleted after `cleanupPeriodDays` (30 by default). The fresh worker rebuilds context from the task file and the branch. Use `--fresh` to force this.
- **`crew followup` says Claude Code started a copy of the session.** Claude Code couldn't continue the session in place; its `note:` line says why. Usually the worker's process was still open and `claude stop` didn't close it in time. `crew` records the copy, so later followups continue from it; close the old session in `claude agents`.
- **`crew dispatch` or `crew followup` says it can't confirm the job started.** The worker's job didn't report its session within 30 seconds. Look it up in `claude agents`. If it's running, find its session ID there and record it with `crew set TASK worker_session <id>`. If it isn't running, run `crew followup TASK --fresh`.
- **The lead or a worker ignores a change you just installed.** A resumed conversation (`lead -c`, `crew followup`) keeps the agent instructions it started with until it's compacted, and a resumed worker also keeps the worker settings it was dispatched with. Start a fresh `lead`, or run `/compact` in it; for a worker, `crew followup --fresh`.
- **A worker's context keeps growing over review rounds.** Each `crew followup` continues the same conversation, so it carries the earlier rounds. Claude Code compacts a worker once it reaches `WORKER_AUTOCOMPACT_WINDOW` (400k tokens by default), and the worker then rereads its task file. For a clean start on the next round, use `crew followup --fresh`. Workers dispatched before you installed this setting keep compacting at about 967k until then.
- **`crew doctor` says a settings file doesn't run crew-guard.** The `hooks` block of `lead/.claude/settings.json` or `config/worker-settings.json` in your home has gone, perhaps in an edit or a merge. Restore it from git (`git -C ~/crew diff`). Until then the guard still covers the lead and workers themselves, but not the subagents they spawn.
- **A timer's log says `'gh' is required but not installed`** (or `claude`). A systemd service doesn't read your shell config, so it only has the `PATH` set in its unit. The units already look in mise's shims (`~/.local/share/mise/shims`). Run `crew doctor` to see what it can't find, then add that directory with `systemctl --user edit crew-tick.service`:

  ```ini
  [Service]
  Environment=PATH=%h/crew/bin:/path/to/the/tool:%h/.local/bin:/usr/local/bin:/usr/bin:/bin
  ```

  `crew install` never touches that drop-in.
- **The agents don't seem to follow AGENTS.md.** See section 4: usually a `CLAUDE.md` or `CLAUDE.local.md` is taking precedence.
- **Hooks don't seem to fire.** Run `claude --debug`. The guard blocks any edit whose path it can't work out, and blocks every edit when `bin/crew` can't get its binary.
- **`crew` says it couldn't download crew `<version>`.** `bin/crew` fetches the binary for your home's `VERSION` from that version's release the first time it runs. Check that the machine is online and that the release exists on GitHub with a binary for your machine (`uname -s`, `uname -m`). After a `crew upgrade`, the binary is already there.
- **`crew` says a settings file has an unexpected line**, naming the file and line. crew reads `config/crew.env`, `project.env` and `local.env` as `KEY=value` lines (quotes, `$VAR`, `${VAR}` and a trailing `# comment` are fine) without running them, so anything else, like `$(…)` or `export`, is refused. Rewrite that line as a plain value.
- **`crew status` shows no worker states.** `claude agents --json` is part of a research-preview feature. If its output format changes, `crew` degrades to ledger-only status. The fix goes in `agents` in `cmd/crew/sessions.go`.
- **Something looks wrong in the ledger.** It's a git repo: `git -C ~/crew log -p`.

## 11. What was and wasn't verified

**Verified:**
- The agent files pass `claude plugin validate`, and every CLI flag `crew` uses was checked against `claude --help` on 2.1.283. That check caught `--add-dir` being variadic, so it is never placed right before a prompt.
- `crew` passes `go vet` for Linux and macOS and its Go unit tests (front matter, settings files, the guard's table, resolution, statuses against `LEDGER.md`); `bin/crew`, `bin/crew-guard` and the test suites pass shellcheck.
- A separate reviewer agent audited the bundle. The bugs it found were fixed and added to the regression suites below.
- `crew guard`: the write-guard allow/block table for worker, lead and gardener paths, including through the hook in the lead and worker settings files. Checks that no settings file turns the sandbox on, that the lead and workers default to auto mode, that they have no tool allowlist hiding MCP servers, and that the worker's `allow`/`deny` rules match its instructions.
- `crew`: end-to-end checks in a crew home made from a copy of the repo, against scratch git repos, with stub `claude`, `gh` and `wt` binaries. They cover:
  - branch names chosen with `--branch` (validated, prefixed, made unique), ready checks, dispatch, and dependency gating;
  - worker-side updates from inside a worktree;
  - feedback rounds, which skip the worker's own replies and comments in unsubmitted reviews, and put collaborators' comments under Open questions instead;
  - `crew tick`: recording a round while the worker is busy and sending it once it's free, including to a worker that asked you a question, marking a task waiting on you, restacking once and only once, cleaning up merged tasks, and never running twice at once;
  - followup: refusing while a worker is busy, continuing one that ended its turn on a question, never resending an old round, resuming, and falling back to a fresh worker;
  - replies: to thread roots, quoting conversation comments and review summaries, and refusing comments from other PRs; sync, cleanup (ending a background process left running in the worktree, and only that), the gardener, and promoting proposals;
  - `WORKTREE_TOOL=worktrunk`: dispatch and cleanup through `wt`, stopping on unapproved hooks, the ledger unlocked while hooks run;
  - per-project worker settings: merged in, and unable to override a shipped value or drop a `deny` rule;
  - goals: starting a goal lead in its own checkout, tasks joining a goal, `crew tick` dispatching a goal's ready tasks (quietly holding one back for a cap), sending the goal lead your notes and its workers' questions only once, and closing, reopening and abandoning a goal;
  - stacked tasks: `--on`, dispatch once the parent's branch is pushed, a stack counting once in flight, sync reporting a moved or merged parent, and `crew restack` through a real squash merge and rebases of a three-task stack;
  - the home: settings layered from `defaults.env`, `crew.env` and `project.env`, the ledger pushed to a bare origin in the background (rebased over commits from elsewhere), agents' own edits committed by the hook, `crew checkout`, and tasks that moved here getting their worktrees back and fresh workers.
- Checks of a machine and its home, in fake `HOME`s: `crew install` (a fresh install, an unchanged re-run, an installed agent edited in place, new and dropped files, skipped components, a home outside `~/crew`); the lease between two machines (claims, `--take`, a lost lease, the old machine standing down, a handover that has to rebase); `crew serve`; `crew setup` against a fake public repo; `crew upgrade` to a release tag, a release whose binary isn't up yet, and a conflict; and `bin/crew` downloading a missing binary, failing to, and fetching another version.
- The fish files were checked with fish 3.7.

**Not verified:** the live checks listed under "Assumptions to re-check" in `ARCHITECTURE.md` (what the lead loads, a real failover), a project's `worker-settings.json` reaching a live `--bg` worker (it's passed as a JSON string to `--settings`), `crew dispatch` with a real repo's Worktrunk hooks (the `wt` flags and JSON output were checked against `wt` 0.78 in a scratch repo), a live worker carrying out a `crew restack` (the rebases in the tests are run by the test itself), GitHub retargeting a stacked PR when its parent's branch is deleted, and live model runs of the agents, goal leads among them. I didn't want to spend your quota from this workspace, and the background-session supervisor needs your machine. Before handing a worker real backlog work, do a smoke test: a tiny, low-stakes task (a typo or a log message) through the whole loop, including one review round.

## 12. Uninstall

```fish
crew host down                       # releases the lease and turns the units off; if it refuses
                                     # over unpushed work, push it, or use --force to drop it
rm ~/.claude/agents/{lead,implementer,casual-observer,plan-reviewer,gardener,goal-lead}.md
rm ~/.config/fish/conf.d/agent-crew.fish ~/.config/fish/functions/lead.fish ~/.config/fish/completions/crew.fish
rm ~/.config/systemd/user/crew-{gardener,tick}.{service,timer} ~/.config/systemd/user/crew-lead.service
# Remove env.CREW_HOME from ~/.claude/settings.json.
# ~/crew and ~/agent-work hold your history and any unmerged work: remove them deliberately.
# Your private repo on GitHub keeps the ledger until you delete it.
```
