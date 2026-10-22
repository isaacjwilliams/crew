# agent-crew

A lead, implementer workers, a casual observer, a plan reviewer, a gardener and goal leads for Claude Code, coordinated through a private ledger instead of GitHub metadata. Each person runs it from a private copy of this repo, on whichever of their machines holds it, and talks to the lead at a terminal or on their phone.

- `SETUP.md`: install, configuration, daily use, safety model, troubleshooting. **Start here.**
- `agents/`: the six agent profiles (installed to `~/.claude/agents/`).
- `bin/`, `cmd/crew/`, `config/`, `templates/`, `LEDGER.md`: the `crew` CLI (in Go; `bin/crew` downloads the binary for your version from its release, so you don't need Go), its write-guard hook, config, and the ledger's templates and conventions. They run from the clone, your crew home; the ledger goes in its `projects/`.
- `fish/`: the `lead` function, PATH setup and completions.
- `lead/`: the lead's session settings: a session started there is the lead.
- `systemd/`: the gardener and `crew tick` timers, and the lead's Remote Control service.
- `launchd/`: the same three services as launchd jobs, for a Mac.

Quick start (fish), with `gh` signed in:

    git clone https://github.com/isaacjwilliams/crew ~/crew; and ~/crew/bin/crew setup
    cd ~/agent-work; and claude          # accept trust once, then /exit
    cd ~/crew/lead; and claude           # the same for the lead
    cd ~/code/webapp; and crew project add; and crew doctor
    lead                                 # or open crew in the Claude app

`crew setup` makes your own private repo from this one, which keeps your ledger and pulls new versions from here (`crew upgrade`). Another machine of yours clones that private repo and runs `crew host up --take` to take the crew over.
