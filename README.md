# agent-crew

A lead, implementer workers, a casual observer, a plan reviewer, a PR reviewer, a gardener and goal leads for Claude Code, coordinated through a private ledger instead of GitHub metadata. Each person runs it from a private copy of this repo, on whichever of their machines holds it, and talks to the lead at a terminal or on their phone.

- `SETUP.md`: install, configuration, daily use, safety model, troubleshooting. **Start here.**
- `agents/`: the seven agent profiles (installed to `~/.claude/agents/`).
- `bin/`, `cmd/crew/`, `config/`, `templates/`, `LEDGER.md`: the `crew` CLI (in Go; `bin/crew` downloads the binary for your version from its release, so you don't need Go), its write-guard hook, config, and the ledger's templates and conventions. They run from the clone, your crew home; the ledger goes in its `projects/`.
- `.claude/skills/pr-review/`: the skill workers run to review their own PR before you do.
- `fish/`: tab completion for fish users.
- `lead/`: the lead's session settings: a session started there is the lead.
- `systemd/`: the gardener and `crew tick` timers, and the lead's Remote Control service.
- `launchd/`: the same three services as launchd jobs, for a Mac.

Quick start, with `gh` signed in and `~/.local/bin` on your `PATH`:

    git clone https://github.com/isaacjwilliams/crew ~/crew && ~/crew/bin/crew setup
    cd ~/agent-work && claude            # accept trust once, then /exit
    cd ~/crew/lead && claude             # the same for the lead
    cd ~/code/webapp && crew project add && crew doctor
    lead                                 # or open crew in the Claude app

`crew setup` makes your own private repo from this one, which keeps your ledger and pulls new versions from here (`crew upgrade`). Another machine of yours clones that private repo and runs `crew host up --take` to take the crew over.
