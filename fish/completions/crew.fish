set -l crew_cmds help version setup upgrade migrate install project checkout host lead doctor config new ready to-learn learned answer hand-over check-base dispatch feedback followup attach restack nudge settle review reply react review-summary ci-fix catch-up ci-summary evidence codex review-cost status stats costs sync tick pause resume list show path find get set log cleanup commit push observe gardener review-eval propose proposals promote remember keep decline memory mcp goal
set -l goal_cmds new start note cap followup done abandon path
set -l project_cmds add
set -l mcp_cmds sync
set -l show_types task proposal goal memory memory-suggestions file
# Whether the crew command being typed is one of the words given. It checks only
# the word after crew, so crew show goal isn't taken for crew goal.
function __crew_command
    set -l words (commandline -opc)
    contains -- "$words[2]" $argv
end
# Whether it's crew show or crew path, with one of the type words given typed
# after it: '' for none yet.
function __crew_showing
    set -l words (commandline -opc)
    contains -- "$words[2]" show path; and contains -- "$words[3]" $argv
end
# The names of the free-form files crew show file finds.
function __crew_free_files
    set -l h (crew config CREW_HOME 2>/dev/null); or return
    set -l files $h/reports/* $h/logs/* $h/news/*.log $h/projects/*/review-eval/*
    path filter -f $files | path basename
end
complete -c crew -f
complete -c crew -n "not __fish_seen_subcommand_from $crew_cmds" -a "$crew_cmds"
complete -c crew -n "__fish_seen_subcommand_from ready to-learn learned answer hand-over check-base dispatch feedback followup attach restack nudge settle review ci-fix catch-up evidence find get set log cleanup observe; and not __fish_seen_subcommand_from goal" \
    -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__crew_showing ''" -a "$show_types"
complete -c crew -n "__crew_showing '' task" -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__crew_showing proposal" -a "(crew proposals --any-status 2>/dev/null | string replace -rf '^(\S+).*' '\$1')"
complete -c crew -n "__crew_showing goal" -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/goals/*.md 2>/dev/null | path change-extension '')"
complete -c crew -n "__crew_showing file" -a "(__crew_free_files)"
complete -c crew -n "__crew_showing memory memory-suggestions file" -l project -x -d 'Of this project' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null)"
complete -c crew -n "__fish_seen_subcommand_from evidence" -F
complete -c crew -n "__fish_seen_subcommand_from promote" -a "(crew proposals 2>/dev/null | string replace -rf '^(\S+).*' '\$1')"
complete -c crew -n "__fish_seen_subcommand_from new promote" -l branch -r -d 'Branch name, without BRANCH_PREFIX'
complete -c crew -n "__crew_command new propose proposals remember keep decline memory status stats list sync cleanup gardener review-eval config pause resume" -l project -x -d 'Work on this project' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null) all"
complete -c crew -n "__fish_seen_subcommand_from config" -l task -x -d 'With this task\'s overrides too' \
    -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from propose" -l why -r -d 'The idea, and why it is worth keeping'
complete -c crew -n "__fish_seen_subcommand_from propose" -l revisit-when -r -d 'What would make it worth acting on'
complete -c crew -n "__fish_seen_subcommand_from propose remember" -l source -x -a 'lead user gardener misses worker:' -d 'Who wrote it'
complete -c crew -n "__fish_seen_subcommand_from remember" -l why -r -d 'Why the fact is worth remembering'
# The open memory suggestions, from crew show, up to the lines typed into the file:
# those of the project --project names, if it's typed.
function __crew_open_suggestions
    # Expanded, so a variable typed after --project gives its value. This comes
    # first, before any local could hide the user's variable of that name.
    # Fish before 4.0 can't expand them, and reads them as typed.
    set -l words (commandline -xpc 2>/dev/null; or commandline -opc)
    set -l project
    if set -l i (contains -i -- --project $words)
        set project --project $words[(math $i + 1)]
    end
    for l in (crew show memory-suggestions $project 2>/dev/null)
        string match -q 'Typed into the file*' -- $l; and return
        string replace -rf '^- \[ \] (\S+) \*\*(.*?)\*\*.*' '$1\t$2' -- $l
    end
end
complete -c crew -n "__crew_command keep decline" -a "(__crew_open_suggestions)"
complete -c crew -n "__crew_command decline" -l why -r -d 'Why the user turned it down'
complete -c crew -n "__crew_command memory" -l hook -d 'For a SessionStart hook: the memory of CREW_PROJECT'
complete -c crew -n "__fish_seen_subcommand_from new" -l kind -x -a 'build spike fix refine' -d 'What the task makes'
complete -c crew -n "__fish_seen_subcommand_from new" -l informs -x -d 'The draft whose question this spike answers' \
    -a "(crew list --status draft 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from new" -l fixes -x -d 'The merged task that introduced the defect this fixes' \
    -a "unknown (crew list --status merged 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from new" -l on -x -d 'Stack on this task\'s unmerged branch' \
    -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from dispatch" -l force -d 'Dispatch even if not marked ready'
complete -c crew -n "__fish_seen_subcommand_from dispatch" -l tick -d "crew tick's: hold back once tick wouldn't dispatch it"
complete -c crew -n "__fish_seen_subcommand_from feedback" -l since -r
complete -c crew -n "__fish_seen_subcommand_from ci-summary" -l not-the-branch -d 'The branch didn\'t cause it: the round doesn\'t count toward MAX_CI_FIXES'
complete -c crew -n "__fish_seen_subcommand_from followup" -l fresh -d 'Start a new worker instead of resuming'
complete -c crew -n "__fish_seen_subcommand_from followup" -l note -r
complete -c crew -n "__fish_seen_subcommand_from restack" -l again -d 'Send a restack the worker already has'
complete -c crew -n "__fish_seen_subcommand_from cleanup" -l merged -d 'All merged tasks and done spikes with worktrees'
complete -c crew -n "__fish_seen_subcommand_from cleanup" -l force
complete -c crew -n "__fish_seen_subcommand_from sync" -l quiet
complete -c crew -n "__fish_seen_subcommand_from status stats list sync proposals pause resume" -l all -d 'Every project, not just this one'
complete -c crew -n "__fish_seen_subcommand_from status" -l done -d 'Include merged/done/abandoned'
complete -c crew -n "__fish_seen_subcommand_from status" -l plain -d 'Print the text, even in a terminal'
complete -c crew -n "__fish_seen_subcommand_from stats" -l since -x -d 'Tasks merged since this date (YYYY-MM-DD)'
complete -c crew -n "__fish_seen_subcommand_from costs" -l from -x -d 'First day to count (YYYY-MM-DD)'
complete -c crew -n "__fish_seen_subcommand_from costs" -l to -x -d 'Last day to count (YYYY-MM-DD)'
complete -c crew -n "__fish_seen_subcommand_from proposals" -l any-status -d 'Every status, not just open'
complete -c crew -n "__fish_seen_subcommand_from observe" -l base -r
complete -c crew -n "__fish_seen_subcommand_from gardener" -l all
complete -c crew -n "__fish_seen_subcommand_from review-eval" -l task -x -d 'Only the cases from this merged task' \
    -a "(crew list --status merged 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from review-eval" -l limit -x -d 'Run only the first N cases'
complete -c crew -n "__fish_seen_subcommand_from review-eval" -l rubric -r -F -d 'Review against this rubric instead of the home\'s'
complete -c crew -n "__fish_seen_subcommand_from pause" -l force -d 'Stop its sessions now, without letting them wrap up'
complete -c crew -n "__fish_seen_subcommand_from pause" -l hook -d 'For a PostToolUse hook: tell a session to wrap up'
complete -c crew -n "__fish_seen_subcommand_from new; and not __fish_seen_subcommand_from goal" -l goal -x -d 'Make it one of this goal\'s tasks' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/goals/*.md 2>/dev/null | path change-extension '')"
complete -c crew -n "__crew_command goal; and not __fish_seen_subcommand_from $goal_cmds" -a "$goal_cmds"
complete -c crew -n "__crew_command goal; and __fish_seen_subcommand_from start note cap followup done abandon path" \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/goals/*.md 2>/dev/null | path change-extension '')"
complete -c crew -n "__crew_command goal; and __fish_seen_subcommand_from new" -l project -x -d 'Work on this project' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null) all"
complete -c crew -n "__crew_command goal; and __fish_seen_subcommand_from new" -l max-in-flight -x -d 'How many of its tasks may be in flight at once'
complete -c crew -n "__fish_seen_subcommand_from upgrade" -l yes -d 'Go ahead without asking'
complete -c crew -n "__fish_seen_subcommand_from install" -l yes -d 'Answer yes to every question'
complete -c crew -n "__fish_seen_subcommand_from install" -l no-fish -d 'Leave the fish files alone'
complete -c crew -n "__fish_seen_subcommand_from install" -l no-services -d "Leave the crew's services alone"
complete -c crew -n "__fish_seen_subcommand_from install" -l skip-agents-md-setting -d "Don't ask about AGENTS.md loading"
complete -c crew -n "__fish_seen_subcommand_from project; and not __fish_seen_subcommand_from $project_cmds" -a "$project_cmds"
complete -c crew -n "__fish_seen_subcommand_from project; and __fish_seen_subcommand_from add" -F
complete -c crew -n "__fish_seen_subcommand_from checkout" -x -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null)"
set -l host_cmds up down
complete -c crew -n "__fish_seen_subcommand_from host; and not __fish_seen_subcommand_from $host_cmds" -a "$host_cmds"
complete -c crew -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from up" -l take -d 'Take the crew over from the machine running it'
complete -c crew -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from down" -l force -d 'Hand over even with unpushed work in worktrees'
complete -c crew -n "__fish_seen_subcommand_from lead" -l hook -d "For a UserPromptSubmit hook: say when the lead's instructions changed"
complete -c crew -n "__fish_seen_subcommand_from lead" -l project -x -d 'Work on this project (all: every project)' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null) all"
complete -c crew -n "__fish_seen_subcommand_from setup" -l name -x -d 'Name of your private repo (default: crew)'
complete -c crew -n "__fish_seen_subcommand_from mcp; and not __fish_seen_subcommand_from $mcp_cmds" -a "$mcp_cmds"
complete -c crew -n "__fish_seen_subcommand_from codex" -F
