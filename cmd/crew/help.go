package main

import "fmt"

func cmdHelp(c *Crew, args []string) error {
	fmt.Print(helpText)
	return nil
}

const helpText = `crew — ledger CLI for the agent crew

A command works on one project: --project P, else CREW_PROJECT (set by crew lead
and for every session crew starts), else the repo you're in. Without
one, commands that list take every project, and --project all or --all asks
for every project.

Setup
  crew setup [--name NAME]         Once, in a fresh clone of the public crew repo: make
                                   your private repo (NAME, default crew) its origin,
                                   with the public one as upstream, and run the crew here
  crew upgrade                     Merge the public repo's new version, install it,
                                   push, and restart the lead's server
  crew install [--yes] [--no-fish] [--no-services] [--skip-agents-md-setting]
                                   Put this machine's files in place: agents, links
                                   to crew and lead in ~/.local/bin, fish
                                   completions, the services (not enabled)
  crew project add [PATH]          Register a repo (default: current) with the ledger,
                                   or this machine's clone of one registered already
  crew checkout PROJECT            Bring the lead's reading copy of PROJECT up to date
                                   with origin; prints its path
  crew host                        Which machine runs the crew, and this one's name
  crew host up [--take]            Run the crew on this machine: install, claim the
                                   lease (--take: from another machine), clone missing
                                   projects, enable the services
  crew host down [--force]         Hand over: stop the crew here and release the lease
                                   (refused while a worktree has unpushed commits)
  crew lead [--project P] [claude options] ["<request>"]
                                   Talk to the lead, in a new session (lead -c
                                   continues the last one); bin/lead runs it
  crew serve                       Serve the lead with Remote Control (what
                                   crew-lead.service runs); exits 3 on a machine
                                   that doesn't run the crew
  crew doctor                      Check dependencies, versions, trust and settings
  crew version                     Print crew's version
  crew config [KEY] [--project P]  Show effective config, with the project's
                                   overrides

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
  crew attach TASK                 Attach this terminal to the task's worker; a
                                   followup or tick that resumes the worker ends it
  crew restack TASK [--again]      Have a stacked task's worker rebase it after its
                                   parent moved or merged (bottom of the stack first);
                                   --again resends a restack the worker already has
  crew review TASK                 Ask the task's worker for another automated review
                                   of its open PR, as a review round of its own: sent
                                   now if the worker is idle, else by crew tick
  crew reply TASK COMMENT_ID TEXT  Reply to a review comment, conversation comment or
                                   review summary on the task's PR (used by workers)
  crew react TASK eyes|rocket COMMENT_ID...
                                   React to your own review comments on the task's
                                   PR: picked up, or being changed (used by workers)
  crew review-summary TASK FILE    Post FILE as the automated-review summary on the
                                   task's PR, or edit the one there (used by workers)
  crew ci-fix TASK                 If CI is red on the head of the task's open PR, and
                                   the worker handed it over, send it the failure as a
                                   review round, once a commit; past MAX_CI_FIXES,
                                   ask you instead (run by crew tick)
  crew ci-summary TASK SHA FILE    Post FILE on the task's PR as why CI was red on
                                   commit SHA, which the task reports, or edit the
                                   one there (used by workers)
  crew codex ROOT PROMPT_FILE OUT_PREFIX
                                   A read-only Codex review in ROOT: its reply in
                                   OUT_PREFIX.md, events in OUT_PREFIX.events.jsonl;
                                   exits 124 when Codex stalls (used by pr-review)
  crew status [--done] [--all|--project P]
                                   Tasks with worker state; stacked tasks are listed
                                   under their parent. For one project, it ends with
                                   what's waiting on you in the others
  crew sync [--quiet] [--all|--project P]
                                   Pull PR URLs/states from GitHub; mark merged tasks;
                                   report stacked tasks that need a restack
  crew tick                        Do what needs no decision, across all projects:
                                   sync, record new PR comments and red CI, send
                                   review rounds and restacks to idle workers, mark
                                   tasks waiting on you, clean up merged ones (run
                                   by a timer)
  crew list [--all|--project P] [--status S]
                                   Compact task list
  crew show TASK | crew path TASK  Print a task file / its path
  crew find TASK                   Print the resolved task id
  crew get TASK KEY                Read a front-matter field
  crew set TASK KEY VALUE...       Write a front-matter field
  crew log TASK MESSAGE...         Append to the task's Log
  crew cleanup TASK|--merged [--project P] [--force]
                                   Remove worktree/local branch of merged tasks
  crew commit [MESSAGE]            Commit pending ledger edits
  crew commit --hook               The same for one file, from an agent's PostToolUse
                                   hook JSON on stdin (always exits 0)

Goals  (GOAL = id, number, unique substring, or file path)
  crew goal new "<title>" [--max-in-flight N] [--project P]
                                   Create a draft goal; prints its path. N is how many
                                   of its tasks may be in flight at once (default
                                   GOAL_MAX_IN_FLIGHT)
  crew goal start GOAL             Start its goal lead, which plans every item
  crew goal note GOAL TEXT         Record your words under the goal's Decisions and
                                   pass them to its goal lead
  crew goal cap GOAL N             Let N of the goal's tasks be in flight at once
  crew goal followup GOAL          Resume the goal lead with your unsent notes and its
                                   workers' new questions (run by crew tick)
  crew goal done GOAL              Close a goal whose tasks are all merged or abandoned
                                   (run by crew tick)
  crew goal abandon GOAL           Stop the goal lead; its tasks stay as they are
  crew goal path GOAL              Print the goal file's path

Review and proposals  (PROPOSAL = id, unique substring, or file path)
  crew observe [TASK|PATH] [--base REF]
                                   Run the casual observer headless on a branch
  crew gardener [--all|--project P]
                                   Run the gardener over newly merged commits
  crew propose "<title>" [--why TEXT] [--revisit-when TEXT] [--source S]
               [--project P]
                                   Write down an idea worth keeping as a proposal;
                                   prints its path. S is lead (the default), user,
                                   gardener or worker:TASK
  crew proposals [--any-status] [--all|--project P]
                                   List open proposals (--any-status: every one),
                                   with who wrote each and when to revisit it
  crew promote PROPOSAL [--branch NAME]
                                   Turn a proposal into a draft task

Project memory  (facts about a project you approved, which its agents load)
  crew remember "<fact>" --why TEXT [--source S] [--project P]
                                   Suggest a fact for the project's memory; it waits
                                   on you in memory-suggestions.md. S is lead (the
                                   default), user, gardener or worker:TASK
  crew memory [--project P]        Print the project's memory, after the rule for
                                   weighing it
  crew memory --hook               The same for CREW_PROJECT, from a SessionStart
                                   hook (nothing for no project or all; always
                                   exits 0)

MCP servers  (a project's own, from projects/<p>/mcp.json)
  crew mcp sync                    Rebuild each project's mcp.local.json and the
                                   leads' lead/.mcp.json now (crew tick does it too),
                                   and report any problem

Other
  crew push                        Push the ledger to origin now (crew pushes it in
                                   the background after every change)
`
