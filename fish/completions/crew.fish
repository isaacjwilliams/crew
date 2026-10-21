set -l crew_cmds help setup upgrade migrate-ledger install project checkout host serve doctor config new ready dispatch feedback followup restack reply status sync tick list show path find get set log cleanup commit observe gardener proposals promote goal
set -l goal_cmds new start note followup done abandon path
set -l project_cmds add
complete -c crew -f
complete -c crew -n "not __fish_seen_subcommand_from $crew_cmds" -a "$crew_cmds"
complete -c crew -n "__fish_seen_subcommand_from ready dispatch feedback followup restack show path find get set log cleanup observe; and not __fish_seen_subcommand_from goal" \
    -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from promote" -a "(crew proposals 2>/dev/null | string replace -r '^(\S+).*' '\$1')"
complete -c crew -n "__fish_seen_subcommand_from new promote" -l branch -r -d 'Branch name, without BRANCH_PREFIX'
complete -c crew -n "__fish_seen_subcommand_from new" -l project -r
complete -c crew -n "__fish_seen_subcommand_from new" -l on -x -d 'Stack on this task\'s unmerged branch' \
    -a "(crew list 2>/dev/null | string replace -r '^\S+\s+(\S+)\s+(\S+).*' '\$1\t\$2')"
complete -c crew -n "__fish_seen_subcommand_from dispatch" -l force -d 'Dispatch even if not marked ready'
complete -c crew -n "__fish_seen_subcommand_from feedback" -l since -r
complete -c crew -n "__fish_seen_subcommand_from followup" -l fresh -d 'Start a new worker instead of resuming'
complete -c crew -n "__fish_seen_subcommand_from followup" -l note -r
complete -c crew -n "__fish_seen_subcommand_from restack" -l again -d 'Send a restack the worker already has'
complete -c crew -n "__fish_seen_subcommand_from cleanup" -l merged -d 'All merged tasks with worktrees'
complete -c crew -n "__fish_seen_subcommand_from cleanup" -l force
complete -c crew -n "__fish_seen_subcommand_from sync" -l quiet
complete -c crew -n "__fish_seen_subcommand_from sync" -l all -d 'Every project, not just this one'
complete -c crew -n "__fish_seen_subcommand_from status" -l done -d 'Include merged/abandoned'
complete -c crew -n "__fish_seen_subcommand_from list proposals" -l all
complete -c crew -n "__fish_seen_subcommand_from observe" -l base -r
complete -c crew -n "__fish_seen_subcommand_from gardener" -l all
complete -c crew -n "__fish_seen_subcommand_from new; and not __fish_seen_subcommand_from goal" -l goal -x -d 'Make it one of this goal\'s tasks' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/goals/*.md 2>/dev/null | path change-extension '')"
complete -c crew -n "__fish_seen_subcommand_from goal; and not __fish_seen_subcommand_from $goal_cmds" -a "$goal_cmds"
complete -c crew -n "__fish_seen_subcommand_from goal; and __fish_seen_subcommand_from start note followup done abandon path" \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/goals/*.md 2>/dev/null | path change-extension '')"
complete -c crew -n "__fish_seen_subcommand_from install" -l yes -d 'Answer yes to every question'
complete -c crew -n "__fish_seen_subcommand_from install" -l no-fish -d 'Leave the fish files alone'
complete -c crew -n "__fish_seen_subcommand_from install" -l no-systemd -d 'Leave the systemd units alone'
complete -c crew -n "__fish_seen_subcommand_from install" -l skip-agents-md-setting -d "Don't ask about AGENTS.md loading"
complete -c crew -n "__fish_seen_subcommand_from project; and not __fish_seen_subcommand_from $project_cmds" -a "$project_cmds"
complete -c crew -n "__fish_seen_subcommand_from project; and __fish_seen_subcommand_from add" -F
complete -c crew -n "__fish_seen_subcommand_from config" -l project -x -d 'Read this project' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null)"
complete -c crew -n "__fish_seen_subcommand_from checkout" -x -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null)"
set -l host_cmds up down
complete -c crew -n "__fish_seen_subcommand_from host; and not __fish_seen_subcommand_from $host_cmds" -a "$host_cmds"
complete -c crew -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from up" -l take -d 'Take the crew over from the machine running it'
complete -c crew -n "__fish_seen_subcommand_from host; and __fish_seen_subcommand_from down" -l force -d 'Hand over even with unpushed work in worktrees'
complete -c crew -n "__fish_seen_subcommand_from setup" -l name -x -d 'Name of your private repo (default: crew)'
complete -c crew -n "__fish_seen_subcommand_from migrate-ledger" -x -a "(__fish_complete_directories)"
