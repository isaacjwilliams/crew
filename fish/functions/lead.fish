function lead --description 'Talk to the crew lead, about any project the crew knows'
    # The lead runs in the crew home's lead/, whose settings make the session the
    # lead and give it the ledger and the project checkouts. Every `lead` run starts
    # a new session named lead; the ledger, not the conversation, carries state
    # between them. `lead -c` continues the most recent lead conversation, and keeps
    # the instructions it started with until it's compacted.
    # conf.d/agent-crew.fish sets CREW_HOME.
    pushd $CREW_HOME/lead; or return 1
    claude --name lead --permission-mode (crew config LEAD_PERMISSION_MODE) $argv
    popd
end
