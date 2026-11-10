function lead --description 'Talk to the crew lead: lead [--project P] [claude options] ["<request>"]'
    # The lead runs in the crew home's lead/, whose settings make the session the
    # lead and give it the ledger and the project checkouts. Every `lead` run starts
    # a new session; the ledger, not the conversation, carries state between them.
    # `lead -c` continues the most recent lead conversation, and keeps the
    # instructions it started with until it's compacted. `lead "<request>"` starts
    # on the request after the briefing.
    #
    # A lead works on one project when you name it with --project, or start it in
    # that project's repo; crew's commands then default to that project. Otherwise
    # it works on every project. CREW_PROJECT carries the choice to the session.
    # conf.d/agent-crew.fish sets CREW_HOME.
    #
    # Every lead loads lead/.mcp.json, every project's own MCP servers, which crew
    # mcp sync brings up to date first. A lead for one project starts with settings
    # that switch off the other projects' servers.
    set -l project
    if set -l i (contains -i -- --project $argv)
        if test $i -eq (count $argv)
            echo "lead: --project needs a project (crew status --all lists them)" >&2
            return 1
        end
        set project (crew config PROJECT --project $argv[(math $i + 1)]); or return 1
        set -e argv[$i..(math $i + 1)]
    else
        set project (crew config PROJECT 2>/dev/null)
    end
    set -l name lead
    if test -n "$project"
        set name lead-$project
    else
        set project all
    end
    set -lx CREW_PROJECT $project
    crew mcp sync
    set -l mcp
    if test $project != all
        set -l settings (crew mcp lead-settings --project $project); or return 1
        set mcp --settings $settings
    end
    pushd $CREW_HOME/lead; or return 1
    claude --name $name --permission-mode (crew config LEAD_PERMISSION_MODE) $mcp $argv
    popd
end
