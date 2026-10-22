package main

import "fmt"

func cmdHelp(c *Crew, args []string) error {
	fmt.Print(helpText)
	return nil
}

const helpText = `crew — ledger CLI for the agent crew

Setup
  crew setup [--name NAME]         Once, in a fresh clone of the public crew repo: make
                                   your private repo (NAME, default crew) its origin,
                                   with the public one as upstream, and run the crew here
  crew upgrade                     Merge the public repo's new version, install it,
                                   push, and restart the lead's server
  crew install [--yes] [--no-fish] [--no-services] [--skip-agents-md-setting]
                                   Put this machine's files in place: agents, fish
                                   functions, systemd units (timers not enabled)
  crew project add [PATH]          Register a repo (default: current) with the ledger,
                                   or this machine's clone of one registered already
  crew checkout PROJECT            Bring the lead's reading copy of PROJECT up to date
                                   with origin; prints its path
  crew host                        Which machine runs the crew, and this one's name
  crew host up [--take]            Run the crew on this machine: install, claim the
                                   lease (--take: from another machine), clone missing
                                   projects, enable the timers
  crew host down [--force]         Hand over: stop the crew here and release the lease
                                   (refused while a worktree has unpushed commits)
  crew serve                       Serve the lead with Remote Control (what
                                   crew-lead.service runs); exits 3 on a machine
                                   that doesn't run the crew
  crew doctor                      Check dependencies, versions, trust and settings
  crew version                     Print crew's version
  crew config [KEY] [--project P]  Show effective config (project overrides applied:
                                   the current repo's, or P's)

Tasks  (TASK = id, number like 7, unique substring, "#<pr-number>", or file path)
  crew new "<title>" [--branch NAME] [--on TASK] [--goal GOAL] [--project P]
                                   Create a draft task; prints its path. NAME is the
                                   branch without BRANCH_PREFIX (default: from title).
                                   --on stacks it on TASK's unmerged branch; --goal
                                   makes it one of GOAL's tasks
  crew ready TASK                  Check the brief is complete and mark it ready
  crew dispatch TASK [--force]     Create worktree + branch and start an implementer
  crew feedback TASK [--since ISO]
                                   Pull new PR comments: yours into a review round,
                                   collaborators' under Open questions for you
  crew followup TASK [--fresh] [--note TEXT]
                                   Resume the task's worker on the latest round/note
  crew restack TASK [--again]      Have a stacked task's worker rebase it after its
                                   parent moved or merged (bottom of the stack first);
                                   --again resends a restack the worker already has
  crew reply TASK COMMENT_ID TEXT  Reply to a review comment, conversation comment or
                                   review summary on the task's PR (used by workers)
  crew status [--done]             Tasks with worker state (current project, or all);
                                   stacked tasks are listed under their parent
  crew sync [--quiet] [--all]      Pull PR URLs/states from GitHub; mark merged tasks;
                                   report stacked tasks that need a restack (current
                                   project, or all)
  crew tick                        Do what needs no decision, across all projects:
                                   sync, record new PR comments, send review rounds
                                   and restacks to idle workers, mark tasks waiting
                                   on you, clean up merged ones (run by a timer)
  crew list [--all] [--status S]   Compact task list
  crew show TASK | crew path TASK  Print a task file / its path
  crew find TASK                   Print the resolved task id
  crew get TASK KEY                Read a front-matter field
  crew set TASK KEY VALUE...       Write a front-matter field
  crew log TASK MESSAGE...         Append to the task's Log
  crew cleanup TASK|--merged [--force]
                                   Remove worktree/local branch of merged tasks
  crew commit [MESSAGE]            Commit pending ledger edits
  crew commit --hook               The same for one file, from an agent's PostToolUse
                                   hook JSON on stdin (always exits 0)

Goals  (GOAL = id, number, unique substring, or file path)
  crew goal new "<title>" [--project P]
                                   Create a draft goal; prints its path
  crew goal start GOAL             Start its goal lead, which plans every item
  crew goal note GOAL TEXT         Record your words under the goal's Decisions and
                                   pass them to its goal lead
  crew goal followup GOAL          Resume the goal lead with your unsent notes and its
                                   workers' new questions (run by crew tick)
  crew goal done GOAL              Close a goal whose tasks are all merged or abandoned
                                   (run by crew tick)
  crew goal abandon GOAL           Stop the goal lead; its tasks stay as they are
  crew goal path GOAL              Print the goal file's path

Review / gardening
  crew observe [TASK|PATH] [--base REF]
                                   Run the casual observer headless on a branch
  crew gardener [--all|PROJECT]    Run the gardener over newly merged commits
  crew proposals [--all]           List gardener proposals (open by default)
  crew promote PROPOSAL [--branch NAME]
                                   Turn a proposal into a draft task

Other
  crew push                        Push the ledger to origin now (crew pushes it in
                                   the background after every change)
`
