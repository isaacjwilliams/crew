# Agent crew: setup and usage

Eight Claude Code agents, a ledger that holds all coordination state outside the repo, and a small CLI (`crew`) that ties them together. You work in two places: **GitHub**, where you review and merge, and **the lead**, which you talk to about everything else, at a terminal or on your phone.

Everything lives in one private git repo of yours, your **crew home**, cloned to `~/crew`: the crew's code, your settings, and the ledger. It's a copy of the public crew repo, [isaacjwilliams/crew](https://github.com/isaacjwilliams/crew), and pulls new versions from it. Any machine with a clone can run the crew, one at a time.

```
          you ──── talk ────▶  lead  (a session in ~/crew/lead, for every project; on the phone: crew)
           │                    │  writes briefs, dispatches, routes feedback
           │                    │  └─ brief-reviewer pass on each brief
           │                    ▼
           │           ~/crew   (your crew home: crew, config, and the ledger; pushed to your private repo)
           │                    │  crew dispatch / crew followup
           │                    ▼
           │      implementer workers   (background sessions, one per task,
           │                    │         each in ~/agent-work/<project>/<task>)
           │                    │  ├─ tests that pin old behaviour first, a /simplify pass once checks pass
           │                    │  ├─ casual-observer pass before the PR (both off when pre_pr_review is no)
           │                    │  ├─ evidence for each criterion, push branch, open draft PR
           │                    ▼  └─ automated review of the PR, fixes, a summary comment
           └── review/merge ── GitHub PRs
                                   ▲
       gardener (weekly timer) ────┘ reads merged commits → proposals in the ledger
       misses (with the gardener)    your comments on merged PRs → proposals in the ledger
       crew tick (every 3 minutes)   your PR comments → the worker; collaborators' → you;
                                     restacks, cleanups; a goal's ready tasks → workers
       goal lead (one per goal)      plans every item of a goal you started, without you
```

**How the lead talks to the right worker.** Every task file records its worker's session name, job ID, and session ID. "Address my comments on #42" becomes: find the task whose `pr` is #42, append your new comments to it as a review round, then resume *that* session in *that* worktree. The lead never has to remember anything. If the session no longer exists, a fresh worker picks up from the task file and the branch.

**What your collaborators see:** branches pushed under your `BRANCH_PREFIX` (a worker pushes after every commit, so a repo whose CI runs on every push runs it on those branches too), draft PRs, one comment per PR summing up its automated review, 👀 and 🚀 reactions to your own review comments, and (optionally) one-line replies to them, all under your GitHub account. Nothing else: no labels, no other comments, no files in the repo. `crew project add` only adds two lines to `.git/info/exclude`, which is local to your clone.

---

## 1. Requirements

- **Linux** on x86_64 or aarch64, or **macOS 13 Ventura or later** on Apple silicon or Intel (Claude Code needs 13; crew's own binary runs on 12). Linux is the reference; see [On a Mac](#on-a-mac) for what differs there.
- **Packages:** on CachyOS, `sudo pacman -S --needed git github-cli curl`; on a Mac, with [Homebrew](https://brew.sh), `brew install git gh` (macOS comes with curl). crew needs git 2.40 or later, which `crew doctor` checks. Any shell will do. If you use fish, crew installs tab completion for it.
- **No Go.** `crew` is one binary, which `~/crew/bin/crew` downloads from the release for your home's version the first time it runs, into `~/crew/bin/release/`.
- **Claude Code 2.1.293 or later**: run `claude update`. The crew relies on background sessions (agent view), automatic `AGENTS.md` loading, `--permission-prompts`, and a `haiku` model that runs in auto mode.
- **GitHub CLI signed in**: run `gh auth login`. Workers push and open PRs as you.
- **Codex** is optional: with it, automated review gets a second reviewer. See [Codex in automated review](#codex-in-automated-review).
- **A plan with auto mode** (Pro, Max, or Team) is recommended. The lead and workers use it by default, so they aren't stopped for every new shell command. See `WORKER_PERMISSION_MODE` and `LEAD_PERMISSION_MODE` below.

## 2. Install

### Your first machine

Everyone, you or a colleague, starts from the public repo and gets a private crew of their own. Clone it to `~/crew` and set it up:

```sh
git clone https://github.com/isaacjwilliams/crew ~/crew
~/crew/bin/crew setup
```

`crew setup` makes a private GitHub repo, `<you>/crew` (`--name` for another name, if you have a `crew` already), and turns the clone into yours: the private repo becomes `origin`, where your ledger is pushed, and the public one `upstream`, where new versions come from. Pushing to `upstream` is disabled, so your ledger can never reach the public repo. It writes your settings file, `config/crew.env`, from `templates/crew.env`, then runs `crew host up`, which puts this machine's files in place with `crew install` (below) and runs the crew here: it claims the lease and enables the timers and the lead's service. Before it enables them, it opens Claude Code in `~/agent-work`, where workers run, for you to trust that folder: accept the trust dialog, then type `/exit`. It does this once, and only when it has a terminal.

Then make sure `~/.local/bin` is on your `PATH` (`crew install` says what to add if it isn't), and run `lead`. The first time, it asks you to trust its own folder, and offers to add your first repo.

### Another machine of yours

Clone your private repo, not the public one: `git clone <your private repo> ~/crew`. Then:

- to have it run the crew, from now on: `~/crew/bin/crew host up --take`. Like `crew setup`, it has you trust `~/agent-work` on this machine first. Use this when the first machine is down. For a planned move, run `crew host down` on the old machine first, then `crew host up` (no `--take`) here. See [Which machine runs the crew](#which-machine-runs-the-crew);
- to only read it, the ledger and the lead's briefs, say: `~/crew/bin/crew install`. That installs the agents, the links and the fish completions and leaves the timers off, and `crew status` there shows what the other machine is doing.

### What `crew install` does

| Installed to | What |
|---|---|
| `~/.claude/agents/{lead,implementer,casual-observer,brief-reviewer,reviewer,gardener,misses,goal-lead}.md` | Agent profiles (user-level, so they're project-agnostic and not in any repo) |
| `~/.local/bin/crew`, `~/.local/bin/lead` | Links to the home's `bin/crew` and `bin/lead`, so the two commands are on your `PATH` |
| `~/.config/fish/completions/{crew,lead}.fish` | Tab completion in fish, including task IDs |
| Linux: `~/.config/systemd/user/crew-gardener.{service,timer}`<br>Mac: `~/Library/LaunchAgents/crew-gardener.plist` | Weekly gardener and misses agent |
| Linux: `~/.config/systemd/user/crew-tick.{service,timer}`<br>Mac: `~/Library/LaunchAgents/crew-tick.plist` | `crew tick` every 3 minutes |
| Linux: `~/.config/systemd/user/crew-lead.service`<br>Mac: `~/Library/LaunchAgents/crew-lead.plist` | The lead for your phone (see below) |

These are the crew's **services**: systemd user units on Linux, launchd jobs on a Mac. `crew install` leaves them off; `crew host up` turns them on for the machine that runs the crew, and `crew host down` turns them off again.

It also sets `env.CREW_HOME` in `~/.claude/settings.json`, so the agents' hooks find the home, and asks whether to load `CLAUDE.md` and `AGENTS.md` together (see section 4). For a home somewhere other than `~/crew`, it points the links and systemd units at it. launchd expands nothing, so on a Mac it always writes the home and your home directory into the jobs it installs.

To upgrade, run `crew upgrade` on the machine that runs the crew. It moves your home to the public repo's newest release: its newest `v<version>` tag, not whatever is on its main branch. It downloads that release's binary first, and if the release's binaries aren't up yet, it says so and changes nothing. It refuses a home with uncommitted changes.

Then it shows the two versions and the workers and goal leads that are working now, and asks `Upgrade now? [y/N]`. Without a terminal (when a lead runs it, say) it can't ask, so it stops there: `crew upgrade --yes` goes ahead. `--yes` also answers yes to the new version's `crew install` questions. Once you say yes, it pauses every project (see [Pausing](#pausing)): sessions that are mid-turn get up to `WRAP_UP_SECONDS` (3 minutes) to note where they are, and are stopped after that. Then it merges the release into your home, installs it with the new version's `crew install`, lifts the pause, pushes, restarts the lead's server, removes the old version's binary, and says which version you're on. A project you paused yourself stays paused. If a project is resumed or added during the wait, or an idle session starts working, it stops before merging and lifts the pause; run it again. If the install fails, the crew stays paused until `crew upgrade`, run again, finishes it; a release your home has already merged is installed without pausing anything. Workers and goal leads pick up the new version's instructions and settings on their own: the next time crew has something for one whose instructions or settings changed, it starts it fresh, and it carries on from its task or goal file; one whose didn't change keeps its conversation. A `lead` you have open tells you, on your next message, when it should be restarted to get them. Start a new `lead` then, not `lead -c`: a continued conversation keeps the instructions it started with, and isn't told so. Your ledger and settings are paths the public repo never has, so they never conflict. If an edit you made to crew's own files does, `crew upgrade` stops with the merge in progress and says how to finish or undo it; the crew stays paused until you do.

**Releases** are made in the public repo. Pushing a tag `v<version>` that matches its `VERSION` file runs a GitHub Actions workflow, which runs the tests (the Go ones on macOS too), builds the binaries for Linux and macOS and publishes them as that tag's release, titled `crew <version>`, with the tag's message as the release notes. The tag must be annotated (`git tag -a`); the workflow refuses a lightweight one. A private home that pushes a tag builds nothing. If you run a fork of crew for others, change the release URL in `bin/crew` to your fork's.

Re-running `crew install` on its own is safe, and it's how changes to the agents, links, completions and services reach a machine.

- Files that changed are replaced; files the home no longer ships are removed.
- Anything replaced or removed is backed up to `~/.local/share/agent-crew/backups/<timestamp>/`.
- It changes nothing tracked in the home: your config, tasks and proposals stay as they are. It does write the lead's per-machine settings, `lead/.claude/settings.local.json`, which git ignores.
- `crew doctor` shows the home's version (its `VERSION` file), and checks that the binary in use is that version.

Options:

| Option | Effect |
|---|---|
| `--yes` | Non-interactive |
| `--no-fish`, `--no-services` | Skip those parts: the fish completions, or the crew's services (systemd units, or launchd jobs on a Mac) |
| `--skip-agents-md-setting` | Don't ask about loading `AGENTS.md` |

`crew` and `lead` are found through `~/.local/bin`, so it has to be on your `PATH`. If it isn't, install and `crew doctor` say what to add to your shell's startup file: `fish_add_path ~/.local/bin` in fish, or `export PATH="$HOME/.local/bin:$PATH"` in bash or zsh.

### The lead on your phone

`crew host up` also enables the `crew-lead` service, which runs `crew serve`: the lead as a Remote Control server named `crew`, on the machine that runs the crew. In the Claude app, open the Code tab and pick `crew` to talk to the lead from anywhere.

- The lead waits for you to speak first: on the phone it doesn't open with a briefing, as `lead` does in a terminal. Say "Status" for one.
- Sessions you start from the phone are throwaway: a session started that way hangs once the service restarts (after `crew upgrade`, say), so start a new one. The lead keeps everything in the ledger, so nothing is lost.
- Workers don't show on the phone, because background sessions don't. Ask the lead about them.

Two first-time prompts need a terminal on that machine, once. Until they're answered the service can't start the lead, and it's tried again every 30 seconds:

1. Trust the lead's folder: run `lead` once and accept the trust dialog.
2. Enable Remote Control, if you never have on this machine: run `crew serve` yourself, answer its question with `y`, then Ctrl+C and restart the service: `systemctl --user restart crew-lead.service` on Linux, `launchctl kickstart -k gui/$(id -u)/crew-lead` on a Mac. If the service shows as running after step 1 (`systemctl --user status crew-lead`, or on a Mac `launchctl print gui/$(id -u)/crew-lead`, whose `state` says `running`), it's enabled already, and `crew serve` would be refused while the service runs.

### On a Mac

The crew runs the same way on a Mac, with these differences:

- **The services are launchd jobs** (LaunchAgents), not systemd units: `crew-tick`, `crew-gardener` and `crew-lead`, in `~/Library/LaunchAgents/`. `launchctl print gui/$(id -u)/crew-tick` shows one. They write their output to `~/crew/logs/crew-tick.log`, `crew-gardener.log` and `crew-lead.log`.
- **They run only while you're logged in, and not while the Mac sleeps.** macOS has no `enable-linger`. Ticks missed during sleep become one when it wakes, and a gardener run missed then happens on waking. This matters most for the lead on your phone: it's there only while the Mac is awake and you're logged in. To keep it reachable, set the Mac not to sleep while on power, in System Settings.
- **A job's `PATH`** lists the usual places for crew's tools: the home's `bin`, `~/.local/bin`, mise's shims, Homebrew's (`/opt/homebrew/bin`, `/usr/local/bin`) and the system's. launchd has no drop-ins like `systemctl --user edit`, and `crew install` replaces an installed plist, so to add a directory, edit the job's plist in your home (`~/crew/launchd/crew-tick.plist`, say), commit it, and run `crew install`. `crew upgrade` merges your edit like any other; if a release changes the same line, it stops on the conflict for you to resolve.
- **The machine's name** is its Local hostname (System Settings → General → Sharing), which doesn't change with the network.
- **A path in another letter case** (`~/Crew/projects/…` for `~/crew`) names the same file on a Mac's usual disk, but the write guard compares paths as written, so it blocks an agent's edit to the ledger spelled that way. It never allows an edit it should block.

### Which machine runs the crew

One machine runs the crew at a time: it dispatches workers, runs the timers, and pushes the ledger to your private repo as it changes. Other machines with a clone of the home can read it (`crew status`) but change nothing.

```sh
crew host               # which machine runs the crew, since when, and this machine's name
crew host up            # run it here: crew install, claim the lease, clone missing projects, enable the timers
crew host up --take     # take it over from another machine, say one that's down
crew host down          # hand over: stop the crew here and release the lease
```

`crew host down` refuses while a worker's branch has commits it hasn't pushed, so nothing is left behind; `--force` hands over anyway. A machine whose lease was taken stops at its next `crew tick`: it turns its timers off, stops its workers, and keeps anything origin didn't get on a branch `lost/<host>-<time>` in the home. The machine's name is `CREW_HOST`, or when that isn't set, `hostname -s` on Linux and `scutil --get LocalHostName` on a Mac (the Local hostname in System Settings → General → Sharing, which doesn't change with the network).

## 3. First-time configuration

1. **Edit your settings file**, `~/crew/config/crew.env`, which `crew setup` made from `templates/crew.env`, then commit and push it (`git -C ~/crew commit -am "My settings" && git -C ~/crew push`). Anything you set there overrides `config/defaults.env`, which lists every setting with its default; leave that file as it is. Worth a look:
   - `BRANCH_PREFIX` (default `agent/`): change it, to `isaac/` say, if you want your own prefix on the branches that land on the shared remote.
   - Check `MAX_WORKERS` (concurrent sessions) and `GOAL_MAX_IN_FLIGHT` (how many of a goal's tasks may be dispatched but unmerged at once, open PRs included). A goal can set its own; see [Goals](#goals). Start low; raise them once the loop feels trustworthy.
   - If the app needs untracked files like `.env.local` to run tests, list them in `WORKTREE_COPY`.
2. **Trust two folders once.** Background sessions check workspace trust before starting, and so does the lead. `crew host up` (which `crew setup` runs) has you trust `~/agent-work`, and the first `lead` asks you to trust its own folder. If setup ran without a terminal, `crew doctor` flags `~/agent-work`; trust it yourself:
   ```sh
   cd ~/agent-work && claude     # accept, then /exit
   ```
3. **Register your repo.** Ask the lead to add it, as the first `lead` offers to, or do it yourself:
   ```sh
   cd ~/code/webapp
   crew project add
   crew doctor
   ```
   `crew project add` records the repo's remote and default branch in `~/crew/projects/<owner>-<repo>/project.env`, where any setting can be overridden for that project. Your clone's path goes in `local.env` beside it, which isn't committed: each machine has its own clone. `crew doctor` checks versions, `gh` auth, agent files, the `crew` and `lead` links, trust, the AGENTS.md setting, and that each enabled service can find the tools `crew` needs.
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

`crew` adds this file to `config/worker-settings.json` for that project's workers. It can only add: lists are appended and new keys added, but it can't change a value the shipped file sets or remove a `deny` rule. It can't change `CREW_PROJECT` or `CREW_HOME` in `env` either: crew sets those for every worker.

### MCP servers for one project

An MCP server you need for one project, such as its tracker, goes in `~/crew/projects/<owner>-<repo>/mcp.json`, not in your user settings or the repo's `.mcp.json`. That project's workers, goal leads and project lead get it, and so does a lead for every project; no session for another project does. The file is shaped like a `.mcp.json`. For Trello's own server:

```json
{"mcpServers": {"trello": {"type": "http", "url": "https://mcp.trello.com/v1"}}}
```

- **Sign in once.** A server like Trello's signs in with OAuth. Start `lead --project <project>`, run `/mcp`, and sign in to it there. Claude Code keeps the login, and the project's background workers use it.
- **Keys stay out of the file.** A local server that takes an API key gets it from a setting: put the key in the project's `local.env` (`TRELLO_TOKEN="…"`, never committed) and write `${TRELLO_TOKEN}` where it goes in `mcp.json`. Any of the project's settings works, but only the `${NAME}` form, and only crew's settings files, not your shell's environment. Put the value itself in `local.env`: a `$VAR` there would come from the environment of whatever runs crew, which differs between the timer and your shell.
- **crew writes what sessions load.** From `mcp.json`, crew writes `mcp.local.json` beside it, with the values filled in, and `~/crew/lead/.mcp.json` with every project's servers. Both are git-ignored, readable only by you, and rewritten each time, so don't edit them. Each server is named `<project>_<server>`, so its tools are `mcp__<project>_trello__…`; use that name in permission rules and in the project's `AGENTS.md`.
- **Edits take effect** at the next `crew tick` (within minutes, with the timer on), the next `lead`, or now with `crew mcp sync`, which also reports a problem such as an unset `${NAME}`. A running session keeps what it started with; a worker resumed with `crew followup` gets the rewritten file, even one that started before the project had an `mcp.json`.
- **Only you edit it.** Every lead starts these servers without asking you, so `crew-guard` stops every agent, the lead included, from editing `mcp.json` or `mcp.local.json`. An agent that thinks a server should change asks you.
- **A broken `mcp.json`** stops the project's dispatches, with an error naming the file and the setting, until you fix it. Other projects carry on, and `crew doctor` reports it.

### What crew's sessions load of yours

Without a word from crew, a lead, goal lead or worker would load everything your own sessions load. Every plugin, skill, MCP server and connector takes room in each request it makes, mostly for tools the role never calls. So the sessions crew starts, from `lead`, `crew dispatch` and the rest, go without the parts of your setup that their role doesn't use:

- the plugins your `~/.claude/settings.json` enables;
- your skills in `~/.claude/skills`, your command files in `~/.claude/commands`, and the skills synced from claude.ai;
- the skills Claude Code ships that the role doesn't use (workers keep `simplify` and `security-review`, leads keep the artifact skills);
- your user-scope MCP servers (in `~/.claude.json`);
- your claude.ai connectors;
- the Artifact tool, for goal leads and workers. Leads keep it.

What a session gets from crew or from the repo stays: crew's own skills and agents, the repo's agents, skills, plugins and servers, and the project's `mcp.json` servers. Claude Code turns things off by name, so anything of yours that shares a name with one of these stays too. crew turns things off only in the settings it starts each session with, so nothing under `~/.claude` changes and your own sessions load what they always did. That includes a `claude` you start in `~/crew/lead` yourself, and the phone lead: they load all of your setup.

To keep something of yours for one project's sessions, name it in `KEEP_LOADED` in `~/crew/projects/<owner>-<repo>/project.env`: a plugin by its id or name, a skill or a server by its name, or `claude.ai` for all the connectors (crew can't turn them off one by one). For a project whose workers test payments in a browser:

```sh
KEEP_LOADED="stripe@claude-plugins-official chrome-devtools"
```

Its workers and goal leads keep them, and so does its project lead and a lead for every project. A session that's running keeps what it started with. When what a session would load changes (you edit the list, or install a plugin or skill), the next time crew sends a worker or goal lead something it starts a fresh session, and a lead tells you to start a new one.

## 4. AGENTS.md: how project conventions reach every agent

You don't have to wire anything. Claude Code 2.1.277 and later reads `AGENTS.md` automatically, and every crew agent but the lead runs inside the repo or one of its worktrees:

| Agent | Where it runs, so where AGENTS.md comes from |
|---|---|
| lead | `~/crew/lead`, outside every repo: it reads `AGENTS.md` itself, in its `crew checkout` copy |
| brief reviewer | a subagent of the lead: it reads `AGENTS.md` at the task's base through `git -C` |
| workers | their worktree, which contains the committed `AGENTS.md` |
| casual observer | as a subagent, which loads the same project instructions |
| gardener | a detached checkout of the default branch |

Subdirectory `AGENTS.md` files load when an agent reads files there.

The catch is **precedence**. By default Claude reads `AGENTS.md` only when there is no `CLAUDE.md`, `.claude/CLAUDE.md`, or `CLAUDE.local.md` in the working directory or above it. If the repo has both files, only `CLAUDE.md` loads. `crew install` offers to switch this to `claude-md-and-agents-md`, which loads both. You can also change it in any session with `/config` → **Project instructions**. It's stored in `~/.claude/settings.json` under `pluginConfigs."agents-md@builtin"`.

Two details:
- Creating a `CLAUDE.local.md` in the repo also stops `AGENTS.md` from loading under the default setting.
- To confirm what loaded, run `/memory` in a lead session and look for the `AGENTS.md` path.

**Your personal taste goes in your user-level files, not the shared AGENTS.md.** `~/.claude/CLAUDE.md` and `~/.claude/rules/*.md` load alongside `AGENTS.md` in every project and every agent. Use them for the conventions you distil from your review comments ("prefer X over Y", "new data fetching goes through Z"), which you'd otherwise repeat on every PR. The implementer has no coding standards of its own. Everything it knows about your taste comes from these files, so if you have none, it works from the repo's `AGENTS.md` alone.

**Facts about one project that aren't conventions go in its project memory.** That's settled decisions that outlive the task that made them ("dashboard columns are unlimited") and private working facts your collaborators shouldn't see ("PR screenshots go in Dropbox"). It lives in the ledger, in `~/crew/projects/<project>/memory.md`, and every crew agent working on the project loads all of it when its session starts, except the casual observer, which is meant to know little. Agents suggest facts with `crew remember`; you approve them. The lead raises each suggestion with you and, if you keep it, moves it into `memory.md`. `crew status` counts the ones waiting. To add a fact yourself, tell the lead "remember that …". `crew memory` prints a project's memory. Where it disagrees with the code, the code wins, and the agent suggests a correction. Claude Code's own auto memory ranks below both.

## 5. Daily use

### Start work: talk to the lead

```sh
lead            # new session; opens with a short briefing
lead "<request>" # the same, starting on your request after a briefing of what needs you
lead --project isaac-webapp   # a lead for one project, from any directory
lead -c         # continue the most recent lead conversation
lead -r         # pick an earlier conversation to continue
```

**One project or all of them.** Run `lead` in a project's repo, or name the project with `--project`, and the lead works on that project: its briefing, `crew status`, `crew list`, `crew proposals` and new tasks are that project's, and a task number like 7 means that project's task 7. Its briefing still mentions anything waiting on you in the other projects, in a line each. Run `lead` anywhere else, or with `--project all`, and it covers every project you've registered; say which one you mean when more than one could fit, and the lead asks otherwise. `lead` passes the choice to the session as `CREW_PROJECT`, which `crew` reads; give `lead -c` the same `--project` as the conversation you continue.

The lead runs in `~/crew/lead`, whose settings make the session the lead, so `cd ~/crew/lead && claude` is a lead too, for every project. It reads each project's code in its own copy, `~/agent-work/<project>/_lead`, which `crew checkout` keeps on the default branch, and reads the project's `AGENTS.md` there itself, since it isn't loaded for a session outside the repo. For the same reason it doesn't get a repo's own MCP servers (`.mcp.json`); that repo's workers do. It also gets the servers each project declares for crew in its `mcp.json` (see [MCP servers for one project](#mcp-servers-for-one-project)): a lead for one project only that project's, and a lead for every project all of them.

**One conversation or many.** The lead keeps nothing important in its conversation. It opens a draft task as soon as you agree on a goal and writes each decision into it as you agree it, so any lead session can pick up where another left off ("let's continue 007"). Keep one long conversation if you like: Claude Code compacts it when it fills up, and after a compaction the lead rereads the task files rather than trusting its summary. Start a fresh `lead` when:

- you've just run `crew install`. A continued conversation keeps the instructions it started with until it's next compacted, so it won't know about changes to `lead.md`;
- `lead -c` opens the wrong conversation. It takes the most recent one in `~/crew/lead`. Use `lead -r` to choose.

A lead session is named `lead`, or `lead-<project>` for one project. If one is already running, Claude Code gives the new one a variant of that name.

**Several at once.** At your desk, run a lead per thread of work, each in its own terminal tab: one investigating a bug while another shapes a brief and a third settles brief-review findings. They share the ledger, so each sees what the others recorded. Each lead rereads a task file before changing it, and leaves alone a draft you haven't named in its conversation, since another lead may be shaping it. `MAX_WORKERS` counts across every lead and project, so raise it in `config/crew.env` if it holds back work you're ready to review.

A typical exchange:

> **you:** Saved searches should remember the filter panel state.
> **lead:** *(greps and reads the search module)* Filters live in `FilterPanel` local state and never reach `SearchQuery`, which saved searches store as JSON. Objective: a saved search reopens with the filters it was saved with. Gates: one saved with filters set reopens with exactly those; one saved before the change still opens. One question: should those older ones open with the panel cleared or with defaults?
> **you:** Defaults. Go.
> **lead:** Brief written as `007-saved-search-filters` and dispatched as worker `3f9c21aa`. It'll appear in agent view with a PR link once the draft is up.

The lead writes the brief in `~/crew/projects/<project>/tasks/007-saved-search-filters.md`. You can read or edit it any time; it's the contract the worker works from.

For anything bigger than this example, the lead shows you a summary before you say go. Its Goal quotes how you described the problem, then states the objective: one outcome a single task can reach. Then come the gates that will show it's met, the facts a worker would most likely miss, and an estimate of the PR's size. A gate measures the outcome ("p95 under 50ms"), never a way to it ("results are cached"). The brief says nothing about how to build it: that's the worker's call, held to your project's rules (`AGENTS.md`, your `~/.claude/CLAUDE.md`) and checked by automated review before you see the PR. While you read it, the brief-reviewer agent reads the brief cold, with the code but without the conversation. It checks that the objective is still your problem, that each gate measures it, that a wrong result couldn't pass them all, and what facts or traps a worker would need. It never proposes a design: when it finds a cheap wrong way to pass, its remedy is a sharper measure, not a gate on the method. When it finishes, the lead tells you what it raised and how each point was settled. If you say go before then, the lead dispatches once the review is in, unless it raises something for you to decide. The lead splits a task past `MAX_PR_LINES`, or one holding two concerns, into smaller tasks before it shows you the summary, and the brief reviewer checks the estimate against the code: a task it finds too big comes back as a finding with a proposed split. The worker checks the real size again before it opens a PR. Check the objective and its gates hardest: they're what the worker builds to. Since the design is the worker's, it judges review findings about the design like any other: it fixes the ones that are right, asks you about one whose fix would break a gate, go out of scope or go against a project rule, and lists the ones it declines, each with its reason, which the lead passes on with the PR.

### Rewriting briefs that still have a plan

Briefs once said how to build the change, under Structure, Plan and Constraints and conventions. Now they don't, so `crew ready` refuses a brief with anything under those sections, and `crew dispatch` refuses a task that was marked ready with one, whether you or `crew tick` runs it. A spike keeps its Plan. To bring your unfinished briefs over, start a new `lead` (not `lead -c`, which keeps the old instructions) and send it this:

> Briefs now set an objective and the gates that show it's met, and leave the design to the worker. Rewrite every unfinished brief that still says how, so `crew ready` takes it again:
>
> 1. Find them: every task in every project that is draft or ready, isn't a spike, and has anything under `## Structure`, `## Plan` or `## Constraints and conventions`.
> 2. Set each ready one back to draft first (`crew set <task> status draft`), so nothing dispatches it half rewritten.
> 3. Rewrite each to the brief standard in your `lead.md`. Check the old brief's facts against the code as it is now: some will have moved on. Keep the Goal's quote, and restate the objective under it. Turn what each old section was for into a gate under Acceptance criteria (something that must hold, such as "started runs never move"), a fact under Context (where code lives, a trap, a decision of mine) or a line under Out of scope (something not to touch). Drop what only said how: the structure, the steps, names for new code, conventions the project's rules already state. Rewrite any acceptance criterion that only one design could pass as what that design was for. Move the size estimate from Structure's `Size:` line to a `## Size` section after Acceptance criteria, as `About <n> lines: <areas>`. Then delete the Structure, Plan and Constraints and conventions sections, headings and all: the template keeps a Plan heading only for spikes. If `crew ready` would still want something, a risk tier say, propose it.
> 4. Show me each brief's before and after: the old sections beside the new objective, gates, facts and size. Mark nothing ready until I've said go for it, then run `crew ready` on each one I approve.

### Watch the workers

```sh
claude agents   # agent view
```

Each worker is a row named `crew-<project>-<task>`. You'll see:

- live state;
- **Needs input** when a worker has a question or a permission prompt;
- a `#123` link once its PR exists.

Keys: `Space` peeks and lets you reply without leaving the list, `Enter` attaches, `←` detaches. Talking to a worker directly here is fine: it records what you decide or ask for under its task's Decisions, so the lead sees it too.

From any terminal, `crew attach TASK` attaches to a task's worker (`crew attach '#123'` works too). When crew resumes that worker with a review round or a note, the attach ends.

### Let `crew tick` keep things moving

`crew tick` does everything that needs no decision from you, across all your projects:

- syncs PR states from GitHub;
- records your new review comments as a round and resumes the task's worker with it as soon as the worker is free: not mid-turn, and not holding a prompt open. A worker that ended its turn on a question for you is free; your answer is what it's waiting for;
- records collaborators' comments under the task's Open questions, for you;
- marks a task `needs-input` once its worker is idle and an Open question waits on you;
- nudges a worker whose turn ended while its task still says it's being worked on, with no question asked, once its transcript has been quiet for 8 minutes: it resumes the worker once to carry on. If the worker stalls again with no new commit and no change of status, or its worktree isn't on this machine, the tick asks the lead under the task's Open questions instead. `crew nudge TASK` does the same by hand;
- restacks stacked tasks whose parent moved or merged, bottom-up, and has a task's worker retarget its PR when the PR targets a branch other than the task's base, never sending the same restack twice. A restack runs in a fresh session on Sonnet, which takes over as the task's worker: it costs far less than resuming the worker's long conversation. The next review round starts a fresh session on the task's own model;
- cleans up merged tasks and done spikes;
- gives a fresh worker to a task in progress that moved here from another machine, since its old session stayed there;
- for a goal you started (below): dispatches its ready tasks as the caps allow, passes its goal lead your notes, its workers' questions and its spikes' reports, and closes the goal once every task is finished (merged, done or abandoned).

It starts a model session only when it resumes a worker or a goal lead. `crew host up` turns its timer on, on the machine that runs the crew. On Linux, to keep the timers and the lead's service running while you're logged out, and to read what a tick did:

```sh
loginctl enable-linger $USER     # once per machine
journalctl --user -u crew-tick   # one line per thing it did
```

On a Mac the jobs run only while you're logged in (see [On a Mac](#on-a-mac)), and a tick's lines go to its log: `tail -f ~/crew/logs/crew-tick.log`.

The lead runs `crew tick` at the start of a session and whenever you ask how things are going, so it works without the timer too, only less promptly.

Whatever a tick does, and whatever it holds back, it says so with a line: a round waiting for a busy worker, a restack waiting for the one below it, a dispatch held by a cap or a dependency, a paused project's work. Your lead hears those lines with your next message, along with what workers did meanwhile (a PR handed over to you, a question for you), so it knows what crew did since you last spoke without running `crew tick` or being asked. It hears each line once, and something held back once until the reason changes. A new session starts with what happens from then on. A lead for one project hears only that project; the lead on your phone hears every project, like a plain `lead`, with each line's project before it. The lines stay on the machine that runs the crew for 30 days, in `~/crew/news/`: a file a day, and `held-back`.

### Hand the crew a goal

For a batch of work you'd rather not plan item by item (a list, or a column on your tracker), give the lead a goal:

> **you:** Work through the Season column on the board. Leave anything about payments alone.
> **lead:** *(writes the goal file, quoting you)* Goal `001-season-column`: every card in Season, nothing touching payments. Start it?
> **you:** Go.

The lead runs `crew goal new` and `crew goal start`. A **goal lead** then does the lead's intake for every item on its own, in a background session named `crew-<project>-goal-<goal>`: a brief to the usual standard, a brief review, the findings settled, and `crew ready`. `crew tick` dispatches the ready tasks as `MAX_WORKERS` and the goal's `max_in_flight` allow. You see the work as PRs, and review them as usual.

- **How far a goal runs ahead of you** is its `max_in_flight`: how many of its tasks may be dispatched and unmerged at once, each task of a stack included. Once it's reached, the goal's other ready tasks wait until one merges, so a goal that took a wrong turn has only a few PRs to undo. It's `GOAL_MAX_IN_FLIGHT` (3) unless you give the goal its own, when you start it ("at most 2 at a time") or later with `crew goal cap GOAL N`.
- **Putting a task next** ("do the standings card next") is its `priority`, a whole number: higher goes first, and a task without one is 0. Ask the lead, which runs `crew set TASK priority N` with N above the other ready tasks'. `crew tick` dispatches the ready tasks of every running goal, and those whose go-ahead is met, highest priority first, then by project and ID, so the next free worker goes to the task you put next. It doesn't get round a cap or a dependency: while its goal is at `max_in_flight`, or it waits on another task, the tasks after it go ahead. `crew status` shows the priority of a ready task that `crew tick` dispatches (a goal's, or one with a go-ahead) beside it when it isn't 0, and lists a running goal's ready tasks in the order they'll go.

- **Parked items** are the decisions the goal lead leaves to you: anything irreversible (a migration, a public API change, a new dependency), anything outside the goal as you wrote it, an unclear item, or a brief-review finding it can't settle. Each stays a draft task with its question under Open questions, and the rest of the goal carries on. The lead tells you about them; your answer goes back with `crew goal note`.
- **Workers' questions** go to the goal lead first. Ones it shouldn't answer reach you through the lead, like any worker question.
- **Telling the goal lead something** ("pick up the cards added since", "use the new layout") is `crew goal note GOAL "…"`, usually through the lead. It reopens a finished goal too.
- **Stopping a goal** is `crew goal abandon GOAL`. Its tasks stay as they are.
- The items come from where your Goal says. For a tracker, the goal lead reads it with the tools its session has (your connectors, the repo's MCP servers, or the project's own `mcp.json` servers) and changes nothing there. How to reach a project's tracker belongs in that repo's `AGENTS.md` or in the goal itself.

`crew status` lists each goal with its tasks under it, grouped by where they stand:

```
  goal 002-do-next-column            running   lead: idle   4 of 37 task(s) merged, 3/3 in flight: full
    in flight
      146-passthrough-run-sources    pr-open   worker: done     #2303
      147-remove-course-leader-board pr-open   worker: done     #2305
      149-historical-stage-topology  working   worker: working  #2309
    next, in dispatch order
      1  174-authorize-cable-channel ready                      -      priority 2
      2  164-course-standings-extern ready                      -      priority 1
      3  150-todo-doc-triage         ready                      -
      4  151-recaptcha-create-assess ready                      -
      5  153-drop-stale-secondary-sa ready                      -
      … (5 more)
    waiting on other tasks
         158-division-results-versio ready                      -      priority 2  after 146
         160-retire-course-leader-bo ready                      -      after 11 tasks
        └ 165-remove-course-leader-b ready                      -
    parked
         154-final-invoice-on-cancel draft                      -      1 open question(s)
```

(The columns are narrower here than in a terminal.) `next` is the order `crew tick` will dispatch them in: the first 5, and any later one with a question for you in its stack. A goal that isn't running yet lists them under `ready`, unnumbered. A task waiting on others names them (`after 146, 149`, or `after 11 tasks`), and always names one that was abandoned, since that one will never merge. `parked` lists the drafts with a question for you, and `planning` the ones the goal lead is still working on. `full` on a goal's line means it's at its `max_in_flight`: its other ready tasks wait until enough of its tasks merge or are abandoned to bring it under. The `workers running` line says `full` when no project has a worker slot free, and each project without one says `workers full` on its own line, with its own limit. A cap set to something that isn't a whole number shows as `?`, with what's wrong on the project's or goal's line, or on the rows of a done or abandoned goal's ready tasks. The goal file, `projects/<project>/goals/NNN-<slug>.md`, records your words, the goal lead's list of items and their tasks, and your notes.

### Check status

Ask the lead "what's going on?", or from a shell:

```sh
crew status        # the project whose repo you're in, or all of them elsewhere (--project P, --all)
crew tick          # sync with GitHub and do whatever needs no decision (see below)
```

In a terminal, `crew status` stays open and keeps itself up to date. It shows the same lines as the text, as a tree: ↑↓ move, ← closes the line you're on (or goes up to the one it's under), → opens it, and enter or space does either. Drafts with nothing to ask, and a running goal's next tasks after the fifth, start closed. It builds the report again every 3 seconds, so a change to the ledger shows within seconds. It lists the workers and checks the branches for restacks every 15 seconds, reads your PRs on GitHub every 30 seconds and, on a machine that doesn't run the crew, pulls the ledger every minute; `r` does all of them now. On a task, `o` opens its PR in your browser, and `n` lets you type a note to its worker: enter sends it as your own words (`crew followup TASK --note "…" --from-user`), Esc drops it. A worker that's busy takes a note once it's idle, and when crew doesn't send one, `n` on its task brings it back. The bar at the bottom shows which of the two the line under the cursor takes, and the line above it says what happened, or why a key did nothing. On a machine that doesn't run the crew, the view can still open PRs, but not send notes. q quits. `crew status --plain`, or `crew status` piped or run by a script, prints the text and exits.

Outside a goal, a ready task says `dispatch by hand`, unless it has a go-ahead: `crew tick` dispatches a goal's tasks and those whose go-ahead is met, so you or the lead run `crew dispatch` for the rest. A go-ahead waits for another task: tell the lead "dispatch 222 once 221 is pr-open", and it records `crew set 222 go_ahead "221 pr-open"`. The task then says `go-ahead once 221 is pr-open`, and the first `crew tick` after 221 gets there dispatches 222, as `MAX_WORKERS` and its dependencies allow, and the lead hears it did. A go-ahead can wait for `pr-open`, `merged` or `done`. One waiting on a task that's since been abandoned is never met, and says so. It's withdrawn with `crew set 222 go_ahead`, and ends when 222 leaves `ready`. Drafts with nothing to ask or learn are counted in one line (`crew list --project <project> --status draft` lists them); a draft with open questions is listed, since it waits on you, and so is one with something still to learn, with its spikes (see [Spikes](#spikes-try-a-path-before-planning-on-it)). `--done` adds merged and abandoned tasks, and done spikes.

### How much of your time it took

```sh
crew stats         # tasks merged in the last 30 days (--since YYYY-MM-DD; --project P, --all)
```

One row for each merged task:

- **kind**: build, fix or refine.
- **review rounds** and **comments**: the rounds of feedback you sent, and the comments in them. A round that only reports red CI or a conflict with the base isn't counted.
- **PR waited on you**: from each time the task went to `pr-open` until its status next changed, when you sent a round or the PR merged.
- **questions waited on you**: from each time it went to `needs-input` until it left it. In a goal's task, from when the goal lead left a worker's question for you, or a collaborator's comment or red CI came to you directly.
- **automated reviews** and **clean after**: how many reviews ran, and the one that first came back clean (nothing fixed at must fix or should fix, and the Claude reviewer reported), or `never`.
- **findings fixed**, **set aside** and **left for you**: what the worker did with the reviews' findings. Set aside means it judged one not a defect: a split it settled that way, or one it disputed, which is left for you as well. A finding that later reviews repeat while it waits for your decision counts once, in each column.
- **later fixes** and **reverts**: the fix tasks that merged naming this task in `fixes`, and the reverts of its PR, whenever they came.

Then the totals, and the median waits.

**By risk tier** groups the same tasks by `risk`: low, medium, high, then those with no risk tier. For each tier it gives how many merged, the median of their review rounds, their comments per task, and the later fixes and reverts since the date that trace to tasks of that tier. A low-risk PR should need only a look at its evidence, so when low-risk PRs drew more comments per task than medium ones, the report says so: the tiers the lead sets, or the `SENSITIVE_PATHS` that raise them, may be off. Read those PRs' comments for what they had in common.

**Later fixes and reverts** lists each fix that merged and each revert made since the date, with the task it traces to and that task's tier, however long before the date that task merged. The last line counts those fixes by `missed_by`:

```
Later fixes and reverts since 2026-11-05:
  051-round-totals fixes 012-invoice-totals (low risk), missed by tests
  revert of #35 → 029-export (medium risk)
  missed by: tests 1
```

A fix traces to its task through `fixes`, or to an unknown task when that's `unknown`. A revert is a commit on the default branch whose message says `This reverts commit <sha>`, as `git revert` and GitHub's revert button write, where that commit is a PR's merge commit (`Merge pull request #35 from …`) or squash commit (`… (#35)`). It traces to the task whose `pr` is that PR, or to an unknown task when none is. Undoing a revert, by reverting it or the PR that made it, isn't counted, and each commit counts as reverted once.

It reads the ledger, and for reverts the default branch in the project's clone, which it fetches first. It changes nothing else, so it works on any machine; without the project's clone, or with a shallow one, it reports the rest and says `reverts: not checked`.

### What each step cost

```sh
crew costs         # the last 7 days (--from YYYY-MM-DD, --to YYYY-MM-DD; both days included)
```

What each step of crew's process cost over those days, at Anthropic's API rates, read from this machine's Claude Code transcripts for every project, and what each step costs a task. Run it on the weeks before and after a change to see whether a step got cheaper.

```
                                                      median
  step                         total  share  tasks  per task
  Brief review                   $97   5.8%     57     $0.74
  Worker sessions             $1,016    61%     73     $8.93
  Exhale                        $104   6.2%     45     $1.60
  …
  Lead sessions                 $160   9.6%
  Lead's agents                  $33     2%
  …
  All                         $1,675   100%     73       $13
```

- **Steps**: a task's brief review, its worker's own sessions, then the agents the worker started, each its own step: `/simplify`'s are the exhale, the casual-observer is the pre-PR review, `/security-review`'s are the security review, the reviewer is the automated review, and any other agent is named by its type (`general-purpose`, say). Then what's no task's: the lead's sessions and the agents it started (other than brief reviews), the goal leads' the same way, the gardener's sessions (the misses agent's too), `crew review-eval`'s reviewer reruns, and the Claude Code sessions in any other folder: the user's own, and `crew review-eval`'s judge, which runs in the temp directory.
- **total** and **share**: what was spent in those days, counted when each message was sent, and its part of all spend.
- **median per task** and **tasks**: what a task that merged or was done in those days spent on the step, all of it, whenever it was sent, and how many tasks that is: the tasks that ran the step. A brief review counts toward its task, whichever lead ran it. The All row's median is of what each task spent in all. A step that's no task's has no median.

Below the table it says which tasks the medians count, and how many it left out. A median counts a task only when this machine's transcripts hold all of its spend, and `crew costs` can tell two ways they might not:

- **Claude Code deleted its older sessions.** Claude Code deletes a session once it has been idle for `cleanupPeriodDays` (30 by default), so the transcripts are complete from when the oldest session it would delete was last written. It keeps a session started or last continued in Claude Desktop or Cowork at any age, and an agent's transcript goes with its session, so those don't date the transcripts. A task begun before then is left out.
- **Another machine ran the crew while the task was under way**, as the `host` file's history shows. That machine's sessions stay on it, so the task is left out.

It can't see any other way spend goes missing. A brief review run by a lead on a machine that doesn't hold the lease is in that machine's transcripts, so its task's medians miss it. A task with a message at a model there's no rate for is left out too, and so is one with nothing spent in this machine's transcripts: its sessions are somewhere else.

Then it says from when the transcripts are complete, if the range starts earlier, gives the tokens of any model it has no rate for, which it doesn't price, and the rates it used. Codex isn't counted. It changes nothing, and reads only this machine's transcripts: run it where the crew runs.

### What a worker's PR looks like

Each PR is short and has the same shape, so you can tell at a glance whether to merge it, look at its evidence, or read the code closely: the objective it meets, in a sentence, then **Risk** (what could break, for whom, and how far it reaches), **How to revert** (and anything a revert alone doesn't undo), **Evidence**, and Notes for reviewers only when there's a trade-off or an open question. A stacked PR first names the PR it's stacked on. The template is `templates/pr-body.md` in your crew home.

**Evidence** shows that the change meets the objective, rather than saying so: for each gate (acceptance criterion), the command the worker ran and its output, or, where only a look would show it, what it saw and how. A check only you can make, a live smoke test say, is listed with what to try when the brief starts its gate `User check:`, or you've made it yours since. A worker that can't run any other check asks you instead, and `crew hand-over` refuses evidence that leaves one to you. Each item names the commit it ran on, and the worker records the same entries in the task file, under `## Evidence`. The worker can't hand the PR over to you (`pr-open`) until every criterion has an entry and a PR item, each from a commit that holds the branch's own work as it is now: `crew hand-over` checks where the evidence ran, though it doesn't judge what it shows. So after any fix, the worker runs all of it again. A restack, or a merge of the base without conflicts, leaves the branch's own work as it was, and its evidence stands. It's the only way into `pr-open` (`crew set` refuses it), so the same checks run each time the PR comes back to you, after a review round or a restack.

### Screenshots in PRs

A screenshot never goes on the task branch, where it would sit in your repo's history. Give a project an evidence repo instead: a GitHub repo of its own (private is fine), with at least one commit on its default branch, that the people who review the project's PRs can read. Then name it in `projects/<project>/project.env`, as a remote URL this machine can push to:

```bash
EVIDENCE_REPO="git@github.com:your-org/pr_evidence.git"
```

A worker publishes its screenshots, and any other file a reviewer should open, with `crew evidence <task> <file>...`. crew commits them under a folder named for the task on the repo's default branch, pushes, and prints a Markdown line for each, which the worker puts in the PR's Evidence:

```
![search-filters.png](https://github.com/your-org/pr_evidence/blob/<commit>/012-saved-search-filters/search-filters.png?raw=true)
[query-plan.txt](https://github.com/your-org/pr_evidence/blob/<commit>/012-saved-search-filters/query-plan.txt)
```

An image shows on the PR, for anyone who can read the evidence repo; another file is a link. Each link names the commit, so publishing a file again under the same name doesn't change what an earlier PR shows. crew keeps its clone of the repo in `~/agent-work/<project>/_evidence`, and never deletes or rewrites anything in the repo, so links in merged PRs keep working. Without `EVIDENCE_REPO`, workers describe their screenshots in the PR instead.

### Risk tiers: what look each PR needs

Every task has a risk tier, which says how closely you need to look at its PR. The lead sets it at intake, from what meeting the objective will touch, and names it in the summary it shows you:

| Tier | The look it needs | When |
|---|---|---|
| `low` | approve from the evidence | the change adds no structure: docs, copy, tests, a fix inside existing code |
| `medium` | read the diff | the change adds or changes structure |
| `high` | read the diff; security review ran | the change touches a sensitive path, or does something irreversible: a migration, a public API, a new dependency, a change to a design principle |

A high-risk task's worker also runs Claude Code's `/security-review` before handing the PR over, and records what it found beside the rest of its evidence. Each PR's Risk section opens with its tier and look.

**The worker's model and automated review.** The lead makes more choices at intake, and names them in its summary. A task runs its worker on Sonnet (`model: sonnet`) unless the worker has to design structure, that is, decide what to add or change in the code's shape (a new command, file or data shape). Fixes and changes inside existing code, tests, docs, copy, and bumps or renames the gates spell out run on Sonnet; work that adds or changes structure runs on Claude Code's default model. The risk tier doesn't decide it. Goal leads apply the same rule. A task whose gates check everything a reviewer would can have its automated review turned off (`auto_review: no`) and its worker's pre-PR passes (`/simplify` and the casual observer) too (`pre_pr_review: no`). `crew status` shows the model and the automated review on the task's row: `model sonnet`, `automated review off`; the pre-PR passes show only in the lead's summary and the task's `pre_pr_review`. Changing a task's model after dispatch takes effect the next time crew has something for its worker: it starts the worker fresh on the new model, and says so.

**The worker's effort.** A task can also name the effort its worker runs at (`crew set <task> effort medium`; `low`, `medium`, `high`, `xhigh` or `max`). Without one, a worker runs at your own setting. It works as the model does: `crew status` shows `effort medium` on the task's row, and changing or clearing it starts the worker fresh at its next followup. A restack session runs at your own setting, and the worker that follows it at the task's effort.

**Sensitive paths.** List the paths where a change always needs your close look, in the project's `projects/<project>/project.env`, as git pathspecs:

```sh
SENSITIVE_PATHS="db/migrate config/credentials :(glob)app/**/auth*"
```

When a task's branch changes a file that matches one, crew raises its tier to `high` and logs which file: when the worker hands over, and at every `crew sync` after that, so a fix made in a review round counts too. Crew never lowers a tier. A raise also turns the task's automated review back on if the lead turned it off, and says so in the same log line, and the worker's hand-over then waits for a review (as every hand-over does while automated review is on); the worker's model stays as it was. A task raised after its hand-over says `not yet: no security review yet` in `crew status`, and `(no security review yet)` on its `to review:` line, until its worker has run one; the lead sends the worker back to run it. If crew can't check, for a pathspec git rejects say, the hand-over is refused until you fix `SENSITIVE_PATHS`.

**In `crew status`**, each open PR's row says whether you can merge it now, and the look it needs:

```
  041-season-export   pr-open   worker: done   #41   merge now  low: approve from the evidence
  038-season-admin    pr-open   worker: done   #38   not yet: CI running; draft  medium: read the diff
```

`merge now` means GitHub has the PR open, ready, clean against its base, its checks passing, no changes requested and nothing its branch rules still want, and crew has nothing more to do on it: its worker is idle, its automated review is recorded, it needs no restack and has no open question. Otherwise `not yet:` names what stands in the way, what's likely to clear soonest first. Status reads each PR from GitHub as it runs; when it can't, it goes by the last `crew sync` and says `(couldn't read GitHub; as of the last sync)`. On a machine that doesn't run the crew, it can't see the workers, so it never says `merge now` there. A PR merged or closed on GitHub that sync hasn't caught up with says `merged on GitHub` or `closed on GitHub`.

Each project ends with the PRs waiting on you, cheapest look first, then by number:

```
  to review: #41 low, #38 medium, #35 high
```

Clear the ones that need only the evidence first, which also frees whatever is stacked on them. A task marked ready before tiers existed shows `no risk tier: read the diff` and comes last.

### Automated review, before yours

Once a worker opens its PR, it reviews it before you do. Its `pr-review` skill (shipped in the crew home, so the crew's sessions load it and your other Claude Code sessions don't) has an independent reviewer agent read the PR cold, with only your words about the change: your request as the task's Goal quotes it, your decisions (the lines under Decisions marked `(user)`), the PR text and the commit messages. A reviewer names each finding's kind of defect and every place it occurs, so one fix covers them all. The worker then checks each finding against the code. It fixes the ones it agrees must or should be fixed, and leaves the ones it disputes or can't settle for you. A suggested improvement in lines the PR changed is made in the PR when it's small, and left for you with its size when it isn't; one outside those lines is written up as a proposal. A defect that was already there when the PR's branch was made doesn't hold the PR back: the worker leaves it alone and tells you how big the fix is, so you can ask for it in this PR. A must-fix finding is closed only by a code change, never by a deploy step or a note in the PR; when that's the only remedy, it's left for you with its cost. Then it reviews again, until a review comes back clean or `AUTO_REVIEWS` reviews have run (3 by default; none for a task whose automated review the lead turned off). The first review reads the whole PR; each later one reads only the commits since the last finished review, or the whole PR again after a rebase. A later reviewer also gets the earlier reviews' findings and what became of them, but only after it has written its own, so its first read stays fresh. It doesn't raise again what an earlier review set aside unless it has new evidence. When reviews disagree anyway, a finding one review set aside and the next says should be fixed, the worker leaves it for you rather than settle it. Every finding and what became of it is recorded in the task file, under `## Automated reviews`, with what each reviewer's run cost: its tokens, as the reviewer's model reports them, how long it took, and the model and effort it ran at.

**When it's too big to settle.** A PR too big for one review rarely comes back clean: each round of fixes brings new findings. So a worker checks its branch's size once, when its checks first pass and before it opens a PR: past `MAX_PR_LINES` (1000 lines added plus removed by default), it doesn't open one. And when the last of `AUTO_REVIEWS` reviews still needed fixes, the review didn't converge: the worker posts its summary but doesn't mark the PR ready, and doesn't review again until you say so. When the branch is too big, or holds two concerns, it proposes smaller pieces to replace the branch, each with the commits and files it carries over, and asks you: split as proposed, review it anyway (at the end of a pass, another pass), or take it as it is. Otherwise it asks only the last two. In a goal, the goal lead answers, and opens the pieces as tasks if it takes the split.

Last, the worker posts one comment on the PR saying what automated review fixed, what it left for later, and what it left for you. Then it hands the PR over: the task turns `pr-open` once every acceptance criterion has evidence from the branch as it is now, a high-risk task has its security review, and automated review has run (unless `AUTO_REVIEWS` is 0 for the task). With `PR_MODE=ready` the PR stays a draft until that hand-over succeeds, and is marked ready right after it. The lead's report of the PR repeats what was left for you.

**Another pass, after your review.** Once the worker has made the changes you asked for, you can have automated review check them: comment `crew review` on the PR (the whole comment, in any case), or ask the lead, which runs `crew review <task>`. Either way the request joins your review comments as a line in a review round, so it waits for a busy worker like any round. The worker addresses the round's comments first, then runs a new pass: up to `AUTO_REVIEWS` more reviews, at least one even when `AUTO_REVIEWS` is 0, each reading the commits since the last finished review, or the whole PR again after a rebase. The reviewers get your review comments as part of what you asked for. The pass ends with a new summary comment of its own, and the earlier ones stay as they were. Only your own `crew review` counts: a collaborator's goes under Open questions, like their other comments.

### Codex in automated review

If you use OpenAI's Codex, each automated review can run it alongside the reviewer agent, so two reviewers from different model families read the PR. To turn it on for every project:

1. Install the [Codex CLI](https://github.com/openai/codex) and sign in: `codex login`.
2. Add `CODEX_REVIEW=yes` to `config/crew.env`. A project can still turn it off with `CODEX_REVIEW=no` in its `project.env`.
3. Run `crew doctor`. With Codex on, it requires `codex` installed, signed in and on the crew's services' `PATH`, since a signed-out Codex would otherwise leave every review to the reviewer agent alone.

Codex gets exactly what the reviewer agent gets, your words and the change, and the two don't see each other's work until both have reported. Then they answer each other's findings for up to `AUTO_REVIEW_REBUTTALS` rounds (2 by default): each confirms, refutes, withdraws or changes the severity of what the other raised. The worker referees. It may add evidence from the code to a round, and it decides each finding at the end, from the highest severity a reviewer still holds. When one reviewer still refutes a finding, the worker settles it rather than leaving it for you: it fixes it, or sets it aside with evidence that the refuter is right, and the PR comment lists what it set aside.

Codex runs read-only, with approvals off, on every call (`crew codex`), and with none of the MCP servers your Codex config declares, so one that can't start never stalls a review (a server declared in a system-wide Codex config still starts). Each review runs in a temporary Codex home: your `~/.codex/config.toml` without its `mcp_servers` and `projects` entries, and your sign-in. Your model provider settings still apply. Two things don't carry over: a sign-in kept in the OS keyring (`cli_auth_credentials_store` of `keyring` or `auto`), which Codex files under the home's path, and relative file paths in the config, which should be absolute. Your Codex `AGENTS.md`, skills, memories and plugins don't reach a review, and nor does a repo's own `.codex/config.toml`. A `deny` rule in workers' settings catches them running `codex` directly. It reviews with `CODEX_MODEL` at `CODEX_MODEL_EFFORT`. If Codex fails or stalls, the review carries on with the reviewer agent alone, and the record and the PR comment say Codex didn't report. Settings are read at each review, so a change applies to the next one, even on a task that's already running.

### Review and send feedback

Review in GitHub as you normally would: submit a review, or leave a PR comment. With the tick timer on (below), that's all: within a few minutes `crew tick` records your comments as "Round N" in the task and resumes the worker that wrote the PR, as soon as it's idle. Without the timer, tell the lead "I reviewed #42", or run `crew tick` yourself. The two steps it does are also commands of their own:

```sh
crew feedback '#42'    # YOUR new comments → "Round N"; collaborators' → Open questions, for you
crew followup '#42'    # resumes the same worker session on the rounds it hasn't had
crew review '#42'      # asks for another pass of automated review, as a round of its own
```

When the worker picks up a round, it reacts 👀 to each of your comments in it, and then 🚀 to each one it's about to change code for. A comment it answers without a change, a question say, keeps only its 👀. A `crew review` comment gets 👀 when it's picked up and 🚀 when its pass starts.

The worker makes the changes as new commits (no force-push), pushes, replies to each of your comments with what it did (set `REPLY_TO_THREADS=no` to turn this off), and marks the task `pr-open` again. An inline comment gets a reply in its thread; a conversation comment or review summary gets a PR comment that quotes it.

Collaborators' comments (people with write access; not bots) aren't sent to the worker. `crew feedback` lists them under the task's Open questions, `crew tick` marks the task `needs-input`, and the lead asks you what to do about each one: have the worker act on it, reply yourself (or have the lead post your reply), or leave it. Comments from anyone else are ignored.

### Red CI

Workers run the project's checks before they push, but a PR can still go red on CI: a job that only runs there, another platform, a different environment. Once a worker has handed its PR to you (`pr-open`), `crew tick` notices when every check on the PR's latest commit has finished and one failed, and sends the worker the failure as a review round of its own, once for each commit. You don't have to ask. Red CI before the hand-over is the worker's own business, and crew leaves it alone.

The worker finds out why. If its branch caused the failure, it fixes it and pushes. If not (a flaky test, a problem with the CI service, the base branch already red), it changes no code and reruns the failed jobs once. If it can't rerun them, or they fail again, it hands the PR back to you without a question: its comment says CI needs someone to rerun it or to fix the base, and the task's row in `crew status` shows the red CI. Either way it posts one comment on the PR saying which checks failed, why, and what it did, so your collaborators can see why the extra commits are there. In a round that also has your comments, it fixes CI first.

A task gets `MAX_CI_FIXES` of these rounds that its branch caused (2 by default). After that, red CI on a new commit waits on you instead: the task turns `needs-input`, with the question under Open questions; look into it yourself, or tell the worker to try again. `MAX_CI_FIXES=0` turns this off. Rounds that only report red CI don't count toward `MAX_REVIEW_ROUNDS`.

**CI other than GitHub Actions.** Workers read GitHub Actions logs and rerun its failed jobs with `gh run`, which their settings allow. For any other CI, say in the project's `AGENTS.md` or its crew memory how to read the logs and rerun failed jobs, and allow those commands in `projects/<project>/worker-settings.json`. Without that, a worker reproduces the failure locally from the CI config in the repo, and leaves the rerun to you.

### Conflicts with the base

As you merge PRs, another open PR that touches the same files can stop merging cleanly: GitHub shows it as conflicting. Once a worker has handed its PR to you (`pr-open`), `crew tick` notices and sends the worker a review round of its own saying so, once for each commit of the base branch. You don't have to ask. The worker merges the base branch into its own, resolves the conflicts, runs the checks and pushes. It doesn't rebase or force-push, so the commits you may have reviewed stay as they were, and the merge commit shows how it was resolved. It posts no comment. If a conflict means the objective or a gate no longer fits, the worker asks you.

Before the hand-over, a conflict is the worker's own business: it rebases onto the base when it conflicts. A stacked task's conflicts are handled by a restack instead (see "Stacked tasks"). Rounds that only report a conflict don't count toward `MAX_REVIEW_ROUNDS`.

### Pausing

To stop work on a project without losing any (when your usage is running low, say), tell the lead, or run:

```sh
crew pause --project <p>      # --all for every project; without either, the lead's or this repo's project
crew resume --project <p>     # let it go on
```

While a project is paused, nothing starts a session for it: `crew tick` sends its workers and goal leads nothing and dispatches none of its tasks (it still records your comments, red CI and conflicts), the gardener skips it, and `crew dispatch`, `crew followup`, `crew nudge`, `crew goal start` and `crew review-eval` refuse. Workers and goal leads that are mid-turn are told to note in their Log where they are and end their turn; any still mid-turn after `WRAP_UP_SECONDS` (3 minutes) are stopped, unless you resume first. Idle ones are left alone. If one won't stop, `crew pause` names it and fails, so you can stop it in `claude agents`. `crew pause --force` stops every worker and goal lead of the project at once, busy or idle, with no time to wrap up, even one still running for a finished task. Your own `lead` keeps working either way. `crew status` marks the project paused, and the tasks a pause interrupted.

Nothing is lost: worktrees, branches and conversations stay. When crew stops a session, it also ends what that session left running in the background in its worktree, such as a dev server or a Codex review, as cleanup does; `--force` does that for sessions that had already closed, too. A background subagent the session started may still run on: Claude Code carries those over to the session's next process. After `crew resume`, the next tick tells each interrupted worker and goal lead to carry on from its Log.

### After merging

`crew tick` notices the merge and cleans up. By hand:

```sh
crew sync && crew cleanup --merged     # or just tell the lead
```

This stops the worker and removes its agent-view row, ends any background process still running in the task's worktree (a dev server the worker started, say; your own terminals there are left alone), then removes the worktree and its local branch. The task file stays in the ledger as history.

### Stacked tasks

When one piece of work builds on another that isn't merged yet, the second task can be stacked on the first: its branch starts from the first task's branch, and its PR targets that branch, so each PR shows only its own diff. Ask the lead to stack it, or:

```sh
crew new "Season admin screens" --branch season-admin --on 044   # based on 044's branch, depends_on 044
crew dispatch 045        # works as soon as 044's branch is on GitHub; 044 needn't be merged
crew status              # a stack is listed bottom first, each task under its parent; a ready one whose
                         #   parent is dispatched is listed on its own, "on <parent>", since it can go next
```

```
  044-separate-season-management     pr-open        #311   (open)
    └ 045-season-admin               pr-open        #312   (draft)
      └ 046-season-picker            working        -
```

- **Trying a stack locally.** The top task's branch contains everything below it. Its worktree is `crew get 046 worktree`, or check out `crew get 046 branch` yourself.
- **When a lower PR changes** (a review round on 044, say), `crew sync` reports `045-…: needs a restack, 044-… moved`. `crew tick` then restacks 045 once its worker is idle: a fresh worker on Sonnet rebases 045's own commits onto 044's new tip and force-pushes its branch with a lease. Once that's pushed, 046 is next. `crew restack 045` does the same by hand, once 045 is `pr-open`. If 044 moves or merges while 045's worker is still on its first run, 045's own worker rebases instead: it checks with `crew check-base` before each costly step (its reviews and its evidence), so they read the branch it will hand over, and `crew hand-over` won't let it hand over until its branch holds 044's latest commit. Both give it the commands, so it carries on without a question or a restack from you. Restacks go bottom-up, and the same restack is never sent twice (`--again` overrides that).
- **When you merge the bottom PR**, `crew sync` reports `needs a restack, 044-… merged`, and `crew tick` has 045's worker rebase onto the default branch, cutting away 044's original commits (which a squash merge replaced), retarget the PR, and record the new base. Wait for that before you review or merge 045: until then its PR still shows 044's commits. Merge bottom-up.
- **Moving a task onto another task's branch** (when the task below it is split, say) is `crew set 045 base <branch>`. Once that branch is on GitHub, `crew sync` reports `045-…: PR targets <old branch>, not <branch>`, and `crew tick` has 045's worker retarget its PR, with a rebase if its branch needs one. A task moved onto a branch that isn't another task's (`main`, when its parent was abandoned) gets a retarget only when its branch starts on that branch; otherwise `crew restack 045` says so, and its worker needs a note from you.
- **A restack force-pushes the task's branch.** If you have it checked out somewhere, reset to the pushed branch rather than pulling.
- **Let GitHub delete merged branches** ("Automatically delete head branches" in the repo settings) if you can: GitHub then retargets the next PR to the default branch itself. Don't delete an unmerged branch that has PRs stacked on it; GitHub closes them.
- **A goal's `max_in_flight`** counts every task of a stack, as `MAX_WORKERS` counts every running worker.
- To stack a task that already exists, set its base to the parent's branch: `crew set 046 base $(crew get 045 branch)`. Leave `depends_on` as it is.

### Spikes: try a path before planning on it

Some objectives rest on something nobody has checked: whether an API pages, whether a query can be fast enough at all. Rather than guess, the lead lists each question under the draft's `## Still to learn`, and `crew ready` refuses the draft until every item is ticked. A **spike** answers one by trying it: its worker tries the path on a branch nobody merges, never pushes it, and writes a report in its task file that starts with `Answer:`. It opens no PR, so there's nothing for you to review.

```sh
crew new "Try cursor paging" --kind spike --informs 040   # 040 is the draft whose question it answers
crew ready 041 && crew dispatch 041     # a spike's brief needs Goal, Plan (what to try, a time box)
                                        #   and Acceptance criteria (what would answer it); no Size or risk
crew status                             # 040's row: "2 to learn; 041 working, 042 draft"
```

- **The lead asks you first**, as for any task, but runs no brief review on a spike: it adds nothing anyone merges. Several spikes can answer one question, one per approach.
- **When a spike reports**, its worker runs `crew hand-over`, which checks the report starts with its answer and nothing is left uncommitted, and sets it `done`. `crew tick` then removes its worktree and local branch. Read the answer with `crew show 041`, or ask the lead, which puts the spikes' answers side by side under the question, ticks it, and finishes the brief from them.
- **In a goal**, the goal lead opens and readies spikes without parking, and `crew tick` dispatches them like its other tasks and sends the goal lead each report once. A running spike counts toward the goal's `max_in_flight`, and a done one doesn't.
- **A spike stays out of stacks**: its branch never reaches GitHub, so it can't be stacked, and nothing can stack on it: `crew new --on` and `crew set … base` refuse either. A task that `depends_on` a spike waits until it's done.

### Fixes: trace a defect to where it came from

A **fix** is only for a defect live in production: released or deployed, and affecting users now. A bug in unreleased work, one found in review, or polish is a **refine**, which works just like a build. Keeping them apart means each fix marks a defect that got past every check, and its brief says which:

```sh
crew new "Round invoice totals to the cent" --kind fix --fixes 012   # 012 is the merged task that introduced it
crew set 050 missed_by tests            # the check that should have caught it
crew ready 050                          # refuses a fix without fixes and missed_by
```

- **`fixes`** is the merged task whose change introduced the defect, or `unknown` when it predates the ledger or can't be traced (the brief says why). The lead finds it from the code's history.
- **`missed_by`** is one of `brief-review`, `tests`, `pre-pr-review`, `security-review`, `automated-review`, `ci` or `user-review`.
- **The worker proves the defect first**: it commits a test that reproduces it and fails, before the fix commit, which makes it pass. The PR's evidence shows both runs, so you can see the test failed before the fix.

## 6. The casual observer on its own

Workers run it automatically before opening a PR. You can also use it anywhere:

- **In any Claude Code session:** `@"casual-observer (agent)" review this branch against origin/main`
- **Headless, from a shell:** `crew observe` (current checkout) or `crew observe 007` (a task's worktree). Add `--base origin/some-branch` to compare against another base.
- **On a collaborator's PR:** `claude --worktree "#123"`, then ask it to have the casual-observer review the change.

It runs on Sonnet at medium effort on purpose: a smaller model with a narrow context budget is a good stand-in for a teammate skimming the PR. Change `model:` in `~/.claude/agents/casual-observer.md` if you prefer.

## 7. The gardener

Run it by hand a few times before scheduling it, so you can tune how picky it is:

```sh
crew gardener --project isaac-webapp   # reviews commits merged since the last run (first run: 14 days)
crew proposals                          # or ask the lead to summarize them
crew promote 2026-09-29-extract-retry   # turns a proposal into a draft task; the lead then runs intake
```

Proposals live in `~/crew/projects/<project>/proposals/`. The gardener writes at most three per run and skips anything already proposed, accepted, or rejected.

The same command then runs the misses agent. It reads the tasks merged since its last run where you commented on the PR after automated review had been through it (first run: from the last commit before those 14 days, as for the gardener), sorts each comment as a miss or not, and proposes a fix at the source for the misses: the repo's AGENTS.md, a lint rule, your `~/.claude/CLAUDE.md` or crew's review rubric, at most three per run. A proposal for your `CLAUDE.md` or the rubric starts `Target: <file>`: it's a change for you to make by hand, not a task, and once you've made it the lead marks the proposal accepted. When no merged task has such a comment, it starts no session.

`crew host up` schedules it (weekly, Monday 06:00). To pause it, `systemctl --user disable --now crew-gardener.timer`, or on a Mac `launchctl bootout gui/$(id -u)/crew-gardener && launchctl disable gui/$(id -u)/crew-gardener`; the next `crew host up` turns it on again.

```sh
journalctl --user -u crew-gardener    # run history; full logs in ~/crew/logs/
tail ~/crew/logs/crew-gardener.log    # on a Mac
```

If your `origin` uses SSH with a passphrase-protected key, the Linux timer won't have your ssh-agent. (A Mac's jobs run in your login session, so they should reach it; that's still to be checked live.) The gardener then warns and reviews your last-fetched `origin/<default>`. Either live with that, or use an HTTPS remote with `gh auth setup-git`.

Proposals aren't only the gardener's. They hold any idea worth keeping that isn't ready to be a task, so it doesn't sit in `crew status` as a draft. Tell the lead "keep this for later: …" and it writes one down; workers do the same with the follow-ups they find. When you ask for one of those follow-ups in the worker's PR, it closes the proposal once the work is done. `crew proposals` shows who wrote each one and, when it's set, what would make it worth acting on. The lead mentions an open proposal when you start a task it touches.

```sh
crew propose "Give each tax jurisdiction a PricingStrategy" --project isaac-webapp --source user \
  --why "Tax inclusion, currency and receipt fields all vary by place; a strategy would keep each place's rules in one class." \
  --revisit-when "a second pricing rule beyond the tax rate varies by place"
```

### Testing review on past misses

The misses agent fixes the cause of a miss; the review eval tells you whether review now catches it. Run it after you change crew's review rubric, the reviewer's prompt (`agents/reviewer.md`) or its model:

```sh
crew review-eval --project isaac-webapp --limit 2   # a cheap trial: the first two cases
crew review-eval --project isaac-webapp             # every case
crew review-eval --task 042                         # only one task's cases
crew review-eval --project isaac-webapp --rubric ~/code/crew/.claude/skills/pr-review/rubric.md
```

Each case is something you found on a merged task's PR after automated review had passed it: a comment of yours in a review round after an automated review, or a fix task naming the task. crew finds them in the ledger every time, so new cases join as tasks merge. For each one it reruns the reviewer on the PR as that review saw it, with only what existed then, and has a separate session judge whether the report raised what you found: **found**, **missed**, or **not a review finding** (a question or praise, a change of mind, something the brief or your decisions settled).

The report goes to `~/crew/projects/<project>/review-eval/` and is committed. It names the rubric and reviewer prompt it used, each with a short hash, and the Claude Code version; then the totals, and one line per case with the finding the judge matched. Once there's a full run before it, it compares verdicts with the newest full run on the cases both have ("2 now found, 1 now missed, of 14 shared cases") and says which of the rubric, the reviewer's prompt and Claude Code changed. The briefings, reviewer reports and judgements are in `~/crew/logs/`, in the directory the report names, so you can check a verdict.

A run narrowed by `--limit` or `--task` is a trial: it says so in its report, and the next run compares with the last full run instead. It costs a review for each reviewed range, plus a short judgement per case, so crew never runs it on its own. A paused project's eval waits until you resume it, and one project runs one eval at a time. If the project is paused during a run, or the rubric or the reviewer's prompt changes (as `crew upgrade` does), the run stops starting sessions, marks the rest `not run` and says it was cut short; like a trial, it's never what the next run compares with. `--rubric PATH` tests a rubric from another checkout before you release it; the reviewer's prompt is always the installed one, in `~/.claude/agents/`.

## 8. Safety model

The crew enforces **where agents edit files** mechanically. For shell commands it gives the lead and workers the same trust as your own sessions in auto mode, and it relies on **instructions** for what they do on GitHub.

| Rule | Enforced by |
|---|---|
| Workers only change files in their own worktree (plus the ledger, temp dirs and tool caches) | `crew-guard` blocks Edit/Write anywhere else, for the worker and any subagent it spawns (the hook is in both `implementer.md` and `worker-settings.json`). Shell commands aren't confined: the implementer's instructions set the same limit, and auto mode's classifier stops risky commands |
| The gardener, the misses agent and `crew observe` don't change your code | They run only the read-only commands in `gardener-settings.json` and `readonly-settings.json` (the gardener may also suggest project memory with `crew remember`); anything else is denied without a prompt. `crew-guard` keeps the gardener's edits to the ledger |
| Only you change a project's MCP servers | `crew-guard` blocks every agent's Edit/Write of a project's `mcp.json` and `mcp.local.json`, the lead's included: every lead starts those servers without asking |
| Only the lead writes a project's memory, when you approve | `crew-guard` blocks workers' and the gardener's Edit/Write of `memory.md`; they suggest with `crew remember`. A goal lead shares the lead's guard role, so for it this is an instruction |
| The lead only edits the ledger | `crew-guard` on Edit/Write, for the lead and its subagents. Its shell commands run in auto mode (`LEAD_PERMISSION_MODE`) |
| On GitHub, workers only push their own branch (`--force-with-lease` allowed, with the commit it expects named), open PRs, edit them, post an automated-review summary on each for every pass of automated review, mark it ready after its first hand-over, and publish evidence files for their own task to the project's evidence repo with `crew evidence` | The implementer's instructions, in a bold "On GitHub, you may do exactly this" section. `deny` rules in `config/worker-settings.json` catch `gh pr merge/close/review` and `git push --force`/`-f` |
| Codex only reads when it reviews | `crew codex` runs Codex every time with the read-only sandbox and approvals off, and none of the MCP servers your Codex config declares, fixed in code. A `deny` rule in `config/worker-settings.json` catches workers running `codex` directly |
| Nobody but you merges, approves, or closes PRs | Agent instructions and those `deny` rules. Branch protection, if you can turn it on, makes it impossible |
| Goal leads only edit the ledger, and never dispatch | `crew-guard` on Edit/Write, as for the lead. Their dispatches go through `crew tick`, which runs `crew dispatch` |
| Your review capacity isn't exceeded | `crew dispatch` enforces `MAX_WORKERS`, and a goal's `max_in_flight` for its tasks, for the lead and for `crew tick` alike |

The GitHub rules are instructions, not mechanical limits. Parsing shell commands to enforce them can't be made complete, so the crew doesn't try. The `deny` rules catch honest mistakes, not an agent that is trying to get around them. Every GitHub action happens under your account, so treat the crew like a junior teammate with your credentials.

Nothing runs in Claude Code's sandbox, and the network isn't restricted. Workers can reach any host and the local services your tests use (Postgres, Redis, docker). The sandbox was tried and dropped: on Linux it blocks `localhost`, which broke test suites that need a local database. See "Bash: no sandbox" in `ARCHITECTURE.md`.

## 9. Configuration reference (`config/crew.env`)

Set these in `config/crew.env`; the defaults below come from `config/defaults.env`.

| Key | Default | Meaning |
|---|---|---|
| `MAX_WORKERS` | 3 | Worker sessions running or waiting on you at once |
| `GOAL_MAX_IN_FLIGHT` | 3 | A goal's dispatched, unmerged tasks, including open PRs and each task of a stack, for a goal that doesn't set its own `max_in_flight` |
| `BRANCH_PREFIX` | `agent/` | Prefix for task branches |
| `WORK_ROOT` | `~/agent-work` | Where worktrees live |
| `WORKER_PERMISSION_MODE` | `auto` | `auto`, `acceptEdits`, or `default` |
| `WORKER_AUTOCOMPACT_WINDOW` | `400k` | How full a worker's conversation gets before Claude Code compacts it: 100k to 1M tokens, or `auto` for Claude Code's default (about 967k on a 1M model). Applies to workers started after you change it |
| `LEAD_PERMISSION_MODE` | `auto` | For `lead` and goal leads: `auto`, `acceptEdits`, or `default` |
| `PRE_PR_REVIEW` | `yes` | Default for new tasks: the worker's `/simplify` and casual-observer passes before the PR (`no` skips both) |
| `PR_MODE` | `draft` | Default for new tasks: `draft`, `ready` (a draft until its first hand-over, then marked ready), or `none` (push only) |
| `REPLY_TO_THREADS` | `yes` | Workers reply to your review comments: in the thread, or quoting a conversation comment or review summary |
| `MAX_REVIEW_ROUNDS` | 4 | Warn after this many rounds of your review (the plan may need a rethink); rounds that only report red CI or a conflict don't count |
| `MAX_CI_FIXES` | 2 | How many times a worker is sent red CI on its PR that its branch caused; past it, the task waits on you. `0` turns CI handling off |
| `AUTO_REVIEWS` | 3 | Most automated reviews a worker runs on its PR before it's yours; `0` turns automated review off. Often set per project, and the lead can turn it off for a task (`auto_review: no`) |
| `MAX_PR_LINES` | 1000 | Lines added plus removed past which the lead splits a task at intake, and a worker asks whether to split its branch, before it opens a PR or when its automated review doesn't settle; `0` sets no limit for any of them |
| `CODEX_REVIEW` | `no` | `yes` adds Codex to automated review, beside the reviewer agent (see [Codex in automated review](#codex-in-automated-review)) |
| `CODEX_MODEL` | `gpt-6-astra` | The model Codex reviews with |
| `CODEX_MODEL_EFFORT` | `xhigh` | Codex's reasoning effort (`model_reasoning_effort`) |
| `AUTO_REVIEW_REBUTTALS` | 2 | Rounds between Codex and the reviewer agent in each review: `0`, `1` or `2`. With 1, a refutation made in round 1 can't be answered: round 1 is when each first sees the other's findings, so the answer would come in round 2 |
| `SENSITIVE_PATHS` | empty | Git pathspecs, space-separated: a task whose branch changes a matching file becomes high risk (see [Risk tiers](#risk-tiers-what-look-each-pr-needs)). Set per project |
| `EVIDENCE_REPO` | empty | A GitHub repo, as a git remote URL like `REMOTE`, that workers publish screenshots and other evidence to, to show them in their PRs (see [Screenshots in PRs](#screenshots-in-prs)). Set per project |
| `KEEP_LOADED` | empty | Plugins, skills and MCP servers of yours that a project's sessions keep, space-separated, and `claude.ai` for your connectors (see [What crew's sessions load of yours](#what-crews-sessions-load-of-yours)). Set per project |
| `WORKTREE_COPY` | empty | Gitignored files copied into each new worktree |
| `WORKTREE_TOOL` | `git` | How task worktrees are created and removed: `git` or `worktrunk` (runs the repo's `wt` hooks). Usually set per project |
| `GARDENER_LOOKBACK` | `14 days ago` | First-run range for a project |
| `GARDENER_MAX_TURNS` | 150 | Turn limit for a gardener run |
| `WRAP_UP_SECONDS` | 180 | How long `crew pause` and `crew upgrade` give workers and goal leads mid-turn to note where they are and end their turn, before they stop them. Crew-wide: `config/crew.env` only |

Per-project overrides go in `projects/<project>/project.env`. `crew config` shows the effective values, and `crew config --task <task>` those a task's worker gets. Per-project additions to the worker settings go in `projects/<project>/worker-settings.json`.

Permission rules for each kind of session are in `lead/.claude/settings.json` (the lead and goal leads), `config/worker-settings.json`, `config/gardener-settings.json` and `config/readonly-settings.json` (`crew observe`). Add `allow` rules there if a worker keeps stopping for a command you're happy to allow, such as your test runner under `default` mode.

To change agent behaviour, edit `~/.claude/agents/*.md`; running sessions pick up changes within seconds.

**MCP servers.** Workers and goal leads get every MCP server configured for the repo they run in: its `.mcp.json`, plus your local-scope servers. A browser or an error tracker set up for one project is there for that project's crew and no other, with nothing to change in the crew. The lead doesn't run in a repo, so it gets none of a repo's own servers. Servers that a project's lead should have too, such as its tracker, go in the project's `mcp.json` (see [MCP servers for one project](#mcp-servers-for-one-project)). Their calls go through auto mode's classifier like shell commands. Your user-scope servers and claude.ai connectors reach a project's sessions only when its `KEEP_LOADED` names them (see [What crew's sessions load of yours](#what-crews-sessions-load-of-yours)). To keep one project's workers off a server of the repo's, add a `deny` rule such as `"mcp__sentry"` to `projects/<project>/worker-settings.json`, or `"mcp__*"` for all of them.

## 10. Troubleshooting

- **A command says crew runs on another machine.** This machine doesn't hold the lease, so it only reads. Run `crew host` to see who does, and `crew host up --take` to move the crew here.
- **A session on the phone stopped answering.** Sessions started from the phone hang once the `crew-lead` service restarts (after `crew upgrade`, or a crash). Start a new one: the lead picks up from the ledger.
- **The phone doesn't list `crew`.** The lead service runs only on the machine that runs the crew, and on a Mac only while it's awake and you're logged in. On that machine, `systemctl --user status crew-lead` shows why it isn't running, or on a Mac `launchctl print gui/$(id -u)/crew-lead` and `~/crew/logs/crew-lead.log`; `crew doctor` checks the rest. The two first-time prompts in [The lead on your phone](#the-lead-on-your-phone) are the usual cause.
- **`crew upgrade` stopped on a conflict.** An edit you made to crew's own files (an agent prompt, say) conflicts with the new version. Resolve the files it lists, `git -C ~/crew commit`, and run `crew upgrade` again, which lifts the pause; or `git -C ~/crew merge --abort` to stay on the old version, and `crew resume --project <p>` for each project it names (not `--all`, which would lift your own pauses too). The crew stays paused until you do one or the other. While the merge is open, git won't take the crew's ledger commits: each change stays in the files, `crew` warns, and the next commit after the merge takes it. Don't leave it open long.
- **A task can't go back to `pr-open` after an upgrade.** Every move to `pr-open` goes through `crew hand-over`, which checks the task each time. A task dispatched before your home had `crew hand-over` has no `## Evidence` entries, so its next return, after a review round, a restack or a question you answered, is refused until its worker records evidence for each acceptance criterion; the lead resumes it with a note saying so. A task whose acceptance criteria aren't `- ` items is refused too, and that one is the lead's to fix: it rewrites them as `- ` items, keeping their words, and hands over again. A task with a PR and no recorded automated review is refused too, while `AUTO_REVIEWS` is above 0 for it, even one opened while it was 0: its worker runs a pass, or the lead lets a task that isn't high risk through with `crew set <task> auto_review no`. Until then the task stays where it was, and its PR on GitHub is unchanged.
- **`crew tick` says it stood down.** Another machine took the lease. This one's timers are off and its workers stopped. If it says it kept work on a `lost/…` branch, those are ledger changes origin never got: compare them with `git -C ~/crew log -p lost/…` and copy over what matters on the machine that runs the crew.

- **`crew dispatch` says it couldn't start the worker.** It prints Claude Code's own output.
  - A trust error means `~/agent-work` hasn't been trusted yet; run `claude` once there.
  - A message about being inside another Claude Code session shouldn't happen, because `crew` strips `CLAUDECODE` before launching. If you see it, run the dispatch from a plain shell, outside Claude Code, to confirm.
- **Workers keep sitting in Needs input.** They're waiting on permission prompts. Use `WORKER_PERMISSION_MODE=auto`, or add `allow` rules to `worker-settings.json`.
- **`crew dispatch` says `wt` couldn't create the worktree.** It shows the end of the hook log; the full output is in `~/crew/logs/worktree-<project>-<task>.log`. If the hooks need approval, run `wt config approvals add` in the repo. If a hook failed partway, fix the cause, remove the half-made worktree with `wt remove ~/agent-work/<project>/<task>`, and dispatch again. `crew dispatch` refuses to reuse a worktree it never finished setting up.
- **`crew dispatch` says `wt` put the branch somewhere else.** The branch already has a worktree outside `~/agent-work` (see `wt list`). Remove that worktree, or dispatch a task with a different branch.
- **`crew cleanup` (or the tick's news) says a worktree stays because a worker is still running.** Cleanup stops a task's worker before removing its worktree, and that worker's process didn't close within 10 seconds. The worktree stays, and the next tick tries again. To finish it now, stop the session in `claude agents`, then run `crew cleanup TASK`.
- **A goal lead shows as `blocked`** in `crew status` or agent view. Its session needs you, most likely for a permission prompt (a tracker's MCP tool, say). Answer it with `claude attach <job>`. To stop that prompt coming back, add an `allow` rule for the tool to `lead/.claude/settings.json`.
- **`crew tick` says a goal's input waits for its goal lead.** The goal lead is mid-turn, often while it works through the goal's items at the start. Your note is recorded under the goal's Decisions; the tick sends it once that turn ends.
- **`crew followup` says the worker is busy.** It's mid-turn, perhaps on a followup sent a moment before, or its process holds a prompt open, such as a permission request. A worker that ended its turn, even on a question for you, isn't busy: `crew followup` stops its process if it's still open, then resumes it. That includes a worker shown as "idle, background task running": its turn ended, but a shell it started, such as a dev server, is still running, and keeps running through the stop and the resume. A review round needs nothing from you: `crew tick` sends it once the worker is free. For anything else, let it finish, answer it in agent view, or have the lead `SendMessage` it by its session name.
- **`crew restack` says the task has no `base_sha`.** The task was dispatched before `crew` recorded one, and its parent's branch is already gone from `origin`, so `crew sync` couldn't work it out. Find the last commit of the parent's in the task's branch (`git log` in its worktree) and run `crew set TASK base_sha <sha>`.
- **`crew restack` says the branch looks restacked already.** The worker rebased but didn't record the new base. If the PR and branch look right, run the `crew set TASK base …` it suggests.
- **`crew followup` says there's nothing new.** Run `crew feedback` first, or pass `--note "..."`. This stops a worker from redoing a round it already handled.
- **`crew followup` says it's starting a fresh worker.** The original session couldn't be resumed, or its job failed straight away. Usually its transcript is gone: transcripts are deleted after `cleanupPeriodDays` (30 by default). The fresh worker rebuilds context from the task file and the branch. Use `--fresh` to force this.
- **`crew followup`, `crew goal note` or `crew tick` says a job is still running, so crew sent it nothing.** `crew` resumes a session only once its process has closed: resuming it while it's open would start a copy. Either the job had just started a turn, perhaps on input sent a moment before, or it was idle but didn't close when `crew` stopped it. A review round, a restack or a goal note stays in the ledger, and `crew tick` sends it once the process closes. A `--note` you gave wasn't sent: send it again then. If `claude agents` shows the job idle and it stays open, stop it there.
- **`crew followup` says Claude Code started a copy of the session.** Claude Code couldn't continue the session in place; its `note:` line says why. `crew` resumes only a session whose process it has closed, so the process opened again in between. `crew` records the copy, so later followups continue from it; close the old session in `claude agents`.
- **`crew dispatch` or `crew followup` says it can't confirm the job started.** The worker's job didn't report its session within 30 seconds. Look it up in `claude agents`. If it's running, find its session ID there and record it with `crew set TASK worker_session <id>`. If it isn't running, run `crew followup TASK --fresh`.
- **The lead or a worker ignores a change you just installed.** A resumed conversation (`lead -c`, `crew followup`) keeps the agent instructions it started with until it's compacted, and a resumed worker also keeps the worker settings it was dispatched with. Start a fresh `lead`, or run `/compact` in it; for a worker, `crew followup --fresh`.
- **A worker's context keeps growing over review rounds.** Each `crew followup` continues the same conversation, so it carries the earlier rounds. Claude Code compacts a worker once it reaches `WORKER_AUTOCOMPACT_WINDOW` (400k tokens by default), and the worker then rereads its task file. For a clean start on the next round, use `crew followup --fresh`. Workers dispatched before you installed this setting keep compacting at about 967k until then.
- **`crew doctor` says a settings file doesn't run crew-guard.** The `hooks` block of `lead/.claude/settings.json` or `config/worker-settings.json` in your home has gone, perhaps in an edit or a merge. Restore it from git (`git -C ~/crew diff`). Until then the guard still covers the lead and workers themselves, but not the subagents they spawn.
- **A timer's log says `'gh' is required but not installed`** (or `claude`). A service doesn't read your shell config, so it only has the `PATH` set in its unit (or, on a Mac, its plist). The units already look in mise's shims (`~/.local/share/mise/shims`). Run `crew doctor` to see what it can't find, then add that directory with `systemctl --user edit crew-tick.service`:

  ```ini
  [Service]
  Environment=PATH=%h/crew/bin:/path/to/the/tool:%h/.local/bin:/usr/local/bin:/usr/bin:/bin
  ```

  `crew install` never touches that drop-in. On a Mac, add the directory to `PATH` in the job's plist in your home instead (`~/crew/launchd/crew-tick.plist`), commit it and run `crew install`; `crew doctor` names the file.
- **The agents don't seem to follow AGENTS.md.** See section 4: usually a `CLAUDE.md` or `CLAUDE.local.md` is taking precedence.
- **Hooks don't seem to fire.** Run `claude --debug`. The guard blocks any edit whose path it can't work out, and blocks every edit when `bin/crew` can't get its binary.
- **`crew` says it couldn't download crew `<version>`.** `bin/crew` fetches the binary for your home's `VERSION` from that version's release the first time it runs. Check that the machine is online and that the release exists on GitHub with a binary for your machine (`uname -s`, `uname -m`). After a `crew upgrade`, the binary is already there.
- **`crew` says a settings file has an unexpected line**, naming the file and line. crew reads `config/crew.env`, `project.env` and `local.env` as `KEY=value` lines (quotes, `$VAR`, `${VAR}` and a trailing `# comment` are fine) without running them, so anything else, like `$(…)` or `export`, is refused. Rewrite that line as a plain value.
- **`crew status` shows no worker states.** `claude agents --json` is part of a research-preview feature. If its output format changes, `crew` degrades to ledger-only status. The fix goes in `agents` in `cmd/crew/sessions.go`.
- **Something looks wrong in the ledger.** It's a git repo: `git -C ~/crew log -p`.

## 11. What was and wasn't verified

**Verified:**
- The agent files pass `claude plugin validate`, and every CLI flag `crew` uses was checked against `claude --help` on 2.1.283. That check caught `--add-dir` being variadic, so it is never placed right before a prompt.
- `crew` passes `go vet` for Linux and macOS and its Go unit tests (front matter, settings files, the guard's table, resolution, statuses against `LEDGER.md`, each launchd job against its systemd unit, and reading `ps` and `lsof` output), which the release workflow also runs on macOS; `bin/crew`, `bin/crew-guard` and the test suites pass shellcheck.
- A separate reviewer agent audited the bundle. The bugs it found were fixed and added to the regression suites below.
- `crew guard`: the write-guard allow/block table for worker, lead and gardener paths, including through the hook in the lead and worker settings files. Checks that no settings file turns the sandbox on, that the lead and workers default to auto mode, that they have no tool allowlist hiding MCP servers, and that the worker's `allow`/`deny` rules match its instructions.
- `crew`: end-to-end checks in a crew home made from a copy of the repo, against scratch git repos, with stub `claude`, `gh` and `wt` binaries. They cover:
  - branch names chosen with `--branch` (validated, prefixed, made unique), ready checks, dispatch, and dependency gating;
  - worker-side updates from inside a worktree;
  - feedback rounds, which skip the worker's own replies and comments in unsubmitted reviews, and put collaborators' comments under Open questions instead;
  - requests for another pass of automated review: your `crew review` comment and `crew review`, each a line in a round, and one summary comment per pass;
  - `crew tick`: recording a round while the worker is busy and sending it once it's free, with any rounds before it, including to a worker that asked you a question, marking a task waiting on you, restacking once and only once, cleaning up merged tasks, and never running twice at once;
  - followup: refusing while a worker is busy, continuing one that ended its turn on a question, never resending an old round, resuming, and falling back to a fresh worker;
  - replies: to thread roots, quoting conversation comments and review summaries, and refusing comments from other PRs; sync, cleanup (ending a background process left running in the worktree, and only that), the gardener, and promoting proposals;
  - `crew evidence`: refusing without `EVIDENCE_REPO`, publishing a task's files in one commit under its folder and printing links pinned to it, and two tasks publishing at once;
  - `WORKTREE_TOOL=worktrunk`: dispatch and cleanup through `wt`, stopping on unapproved hooks, the ledger unlocked while hooks run;
  - project memory: suggestions written and counted in `crew status`, the memory printed with its header, and the SessionStart hook's output for each kind of session, and its exit 0 whatever it's given;
  - per-project worker settings: merged in, and unable to override a shipped value or drop a `deny` rule;
  - each worker, goal lead and gardener getting its own project and the home in its settings' `env`, whatever project the command that started it was for;
  - per-project MCP servers: workers and goal leads started with the project's `mcp.local.json`, filled in from `local.env`, git-ignored and private, and resumed without flags after it's rebuilt; a broken `mcp.json` stopping only its project's dispatches;
  - goals: starting a goal lead in its own checkout, tasks joining a goal, `crew tick` dispatching a goal's ready tasks (holding one back for a cap, saying why, a goal's `max_in_flight` among them, counting every task of a stack), sending the goal lead your notes and its workers' questions only once, and closing, reopening and abandoning a goal;
  - stacked tasks: `--on`, dispatch once the parent's branch is pushed, sync reporting a moved or merged parent, `crew restack` through a real squash merge and rebases of a three-task stack, and a PR that targets a branch other than its task's base retargeted once, alone or with a rebase;
  - the home: settings layered from `defaults.env`, `crew.env` and `project.env`, the ledger pushed to a bare origin in the background (rebased over commits from elsewhere), agents' own edits committed by the hook, `crew checkout`, and tasks that moved here getting their worktrees back and fresh workers.
- Checks of a machine and its home, in fake `HOME`s: `crew install` (a fresh install, an unchanged re-run, an installed agent edited in place, a stray file where a link goes, new and dropped files, the fish files an older install shipped, skipped components, a home outside `~/crew`); the lease between two machines (claims, `--take`, a lost lease, the old machine standing down, a handover that has to rebase); `crew serve`; `lead`, run in bash through its link: its project scope, and the MCP servers it syncs, then switches off for one project; `crew setup` against a fake public repo; `crew upgrade` to a release tag, a release whose binary isn't up yet, its confirmation, the pause it makes and lifts, and a conflict; and `bin/crew` downloading a missing binary, failing to, and fetching another version.
- The fish completions were checked with fish 3.7.

**Not verified:** the live checks listed under "Assumptions to re-check" in `ARCHITECTURE.md` (what the lead loads, project memory reaching sessions through the SessionStart hook, a real failover, everything on a Mac: the shell suites run only on Linux), the settings crew passes to a live `--bg` worker, goal lead or gardener reaching it (they're a JSON string to `--settings`, with the session's project in their `env`), a project's `mcp.json` servers reaching live workers and leads (the MCP entries under "Assumptions to re-check"), `crew dispatch` with a real repo's Worktrunk hooks (the `wt` flags and JSON output were checked against `wt` 0.78 in a scratch repo), a live worker carrying out a `crew restack` (the rebases in the tests are run by the test itself), GitHub retargeting a stacked PR when its parent's branch is deleted, an image from a private evidence repo showing on a PR, and live model runs of the agents, goal leads among them. I didn't want to spend your quota from this workspace, and the background-session supervisor needs your machine. Before handing a worker real backlog work, do a smoke test: a tiny, low-stakes task (a typo or a log message) through the whole loop, including one review round.

## 12. Uninstall

```sh
crew host down                       # releases the lease and turns the services off; if it refuses
                                     # over unpushed work, push it, or use --force to drop it
rm ~/.claude/agents/{lead,implementer,casual-observer,brief-reviewer,reviewer,gardener,misses,goal-lead}.md
rm ~/.local/bin/{crew,lead} ~/.config/fish/completions/{crew,lead}.fish
rm ~/.config/systemd/user/crew-{gardener,tick}.{service,timer} ~/.config/systemd/user/crew-lead.service
rm ~/Library/LaunchAgents/crew-{tick,gardener,lead}.plist      # on a Mac, instead
# Remove env.CREW_HOME from ~/.claude/settings.json.
# ~/crew and ~/agent-work hold your history and any unmerged work: remove them deliberately.
# Your private repo on GitHub keeps the ledger until you delete it.
```
