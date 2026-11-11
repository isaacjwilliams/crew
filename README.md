# crew

crew runs a team of Claude Code agents on your GitHub repos, built around your review queue. You never face more PRs than you chose to, and each one implements a plan you approved.

## How it works

You talk to a **lead**, at a terminal or from the Claude app on your phone. It reads the code, asks what it needs to know, and writes a plan. A plan reviewer reads the plan cold, without your conversation. You say go. A **worker** builds it in its own git worktree and opens a draft PR. When you review the PR, your comments go back to that same worker.

## What's different

- **Your review capacity caps the work.** The lead dispatches a task only when you say go. When you hand the crew a goal to work through without you, the goal's `max_in_flight` caps how many of its tasks are out at once, counting every one dispatched and not yet merged, open PRs included.
- **You approve the plan before any code is written.** The plan says where the new code lives and which alternative lost. Inside a goal, a goal lead approves plans in your place and parks the decisions that should be yours. You can also tell the lead to just do small, clear tasks.
- **Agents never merge.** Only you do. Each agent's instructions say exactly what it may do on GitHub, and settings rules catch the obvious slips, like `gh pr merge`. Branch protection, where you can turn it on, is the backstop.
- **The shared repo sees only task branches and their PRs**, plus comments and reactions on those PRs, all under your GitHub account. No labels, no committed config, no files in the working tree. [SETUP.md](SETUP.md) has the full list, under "What your collaborators see".
- **The ledger is plain Markdown in a private repo of yours.** Every task, plan, decision and review round is a file you can read and edit. crew commits and pushes it as it changes, so another machine can take over where this one left off.
- **Several leads at once.** Each lead is a planning conversation, and they share the ledger, so each one sees what the others wrote down.
- **Ordinary words, used in their ordinary sense**: lead, worker, task, goal, proposal. A few terms are crew's own, like the ledger and the gardener.
- **Small.** One Go binary on the standard library, seven agent files, three short `sh` scripts and plain text. No database, no tmux. Timers run plain `crew` commands to keep work moving, not agents supervising agents, so a crew with nothing to do starts no agents. The one scheduled agent, the gardener, runs once a week, and only when your default branch has new commits.

## Limits

- **Claude Code only.** Every agent is a Claude Code session.
- **GitHub only.** Workers push and open PRs with the GitHub CLI.
- **One machine runs the crew at a time.** Your other machines can read the ledger, and any of them can take over.
- **Linux, or macOS 13 or later.**

## Getting started

You need Claude Code, git, curl and the GitHub CLI, signed in with `gh auth login`. [SETUP.md](SETUP.md#1-requirements) has the details.

```sh
# Install: make your private crew repo and run the crew on this machine
git clone https://github.com/isaacjwilliams/crew ~/crew && ~/crew/bin/crew setup

# Upgrade, when a new version is out
crew upgrade

# Start work: talk to the lead
lead
```

`crew` and `lead` are found through `~/.local/bin`, so it must be on your `PATH`; setup says what to add if it isn't. The first `lead` asks you to trust its folder, and offers to add your first repo.

Then paste this into your first lead:

> I'm new to crew. Briefly, what can I do here? Describe tasks, goals, proposals and project memory: what each is for and how I'd start one. Then ask what I'd like to work on.

## More

- [SETUP.md](SETUP.md): install, daily use, the safety model and troubleshooting.
- [ARCHITECTURE.md](ARCHITECTURE.md): the design principles and how the pieces fit together.
