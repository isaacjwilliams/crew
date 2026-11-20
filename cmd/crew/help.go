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
  crew upgrade [--yes]             Pause the crew while sessions wrap up, merge the
                                   public repo's new version, install it, push,
                                   restart the lead's server, and lift the pause.
                                   It asks first; --yes is the answer without a
                                   terminal, and answers crew install's questions
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
  crew lead --hook                 From the lead's SessionStart and UserPromptSubmit
                                   hooks: tell the session what crew did since its
                                   previous prompt, and say when the lead's
                                   instructions or settings changed since it
                                   started (always exits 0)
  crew serve                       Serve the lead with Remote Control (what
                                   crew-lead.service runs); exits 3 on a machine
                                   that doesn't run the crew
  crew doctor                      Check dependencies, versions, trust and settings
  crew version                     Print crew's version
  crew config [KEY] [--project P|--task T]
                                   Show effective config, with the project's
                                   overrides, and with --task, the task's
                                   AUTO_REVIEWS

Tasks  (TASK = id, number like 7, unique substring, "#<pr-number>", or file path)
  crew new "<title>" [--kind KIND] [--informs TASK] [--fixes TASK] [--branch NAME] [--on TASK] [--goal GOAL] [--project P]
                                   Create a draft task; prints its path. KIND is build
                                   (the default); fix, for a defect live in production;
                                   refine, for any other change to existing work; or
                                   spike, which tries a path and reports instead of
                                   making a PR. --informs ties a spike to the draft
                                   TASK whose question it answers; --fixes ties a fix
                                   to the merged TASK that introduced the defect, or
                                   unknown.
                                   NAME is the branch without BRANCH_PREFIX (default:
                                   from title). --on stacks it on TASK's unmerged
                                   branch; --goal makes it one of GOAL's tasks
  crew ready TASK                  Check the brief is complete for its kind, with
                                   nothing left under Still to learn, and mark it
                                   ready. A build needs a Goal, Acceptance criteria,
                                   a Size and a risk tier, and nothing under
                                   Structure, Plan or Constraints and conventions. A
                                   fix also needs fixes and missed_by
  crew dispatch TASK [--force]     Create worktree + branch and start an implementer.
                                   --force dispatches a task that isn't ready; a
                                   brief with a plan is refused either way
  crew feedback TASK [--since ISO]
                                   Pull new PR comments: yours into a review round,
                                   collaborators' under Open questions for you
  crew followup TASK [--fresh] [--note TEXT]
                                   Resume the task's worker on the latest round/note
  crew attach TASK                 Attach this terminal to the task's worker; a
                                   followup or tick that resumes the worker ends it
  crew restack TASK [--again]      Have a stacked task's worker rebase it after its
                                   parent moved or merged (bottom of the stack first),
                                   and any task's worker retarget a PR that targets
                                   another branch than its base;
                                   --again resends a restack the worker already has
  crew nudge TASK                  Resume, once, a worker whose turn ended mid-task;
                                   ask the lead if it stalls again (run by crew tick)
  crew review TASK                 Ask the task's worker for another automated review
                                   of its open PR, as a review round of its own: sent
                                   now if the worker is idle, else by crew tick
  crew hand-over TASK              Hand a build's PR over to you (status pr-open),
                                   once every acceptance criterion has evidence
                                   under ## Evidence and in the PR body, from a commit
                                   that holds the branch's own work as it is now, and
                                   a high-risk task a security review; a branch that
                                   touches SENSITIVE_PATHS makes it high risk. Mark a
                                   spike done once its ## Report starts with "Answer:"
                                   (used by workers)
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
  crew catch-up TASK               If the task's open PR conflicts with its base, the
                                   task isn't stacked, and the worker handed it over,
                                   send it the conflict as a review round, once a
                                   commit of the base, to merge it in (run by crew tick)
  crew ci-summary TASK SHA FILE    Post FILE on the task's PR as why CI was red on
                                   commit SHA, which the task reports, or edit the
                                   one there (used by workers)
  crew evidence TASK FILE...       Publish screenshots and other files to the task's
                                   folder in the project's EVIDENCE_REPO, in one
                                   commit, and print a Markdown link to each, pinned
                                   to that commit, for the PR (used by workers)
  crew codex ROOT PROMPT_FILE OUT_PREFIX
                                   A read-only Codex review in ROOT: its reply in
                                   OUT_PREFIX.md, events in OUT_PREFIX.events.jsonl,
                                   what it cost in OUT_PREFIX.cost; exits 124 when
                                   Codex stalls (used by pr-review)
  crew review-cost AGENT_ID        What the run of Claude subagent AGENT_ID cost:
                                   tokens, time, model and effort, from its
                                   transcript (used by pr-review)
  crew status [--done] [--all|--project P]
                                   Tasks with worker state, and what each one not yet
                                   dispatched waits for. A goal's tasks are grouped,
                                   the next ones in dispatch order; stacked tasks are
                                   listed under their parent. An open PR's row says
                                   the look it needs, and each project ends with its
                                   PRs to review, cheapest look first. For one
                                   project, it ends with what's waiting on you in the
                                   others
  crew stats [--since YYYY-MM-DD] [--all|--project P]
                                   For each task merged since the date (default 30
                                   days ago): your review rounds and comments, how
                                   long its PR and questions waited on you, how its
                                   automated reviews went, and its later fixes and
                                   reverts. Then the tasks by risk tier, and each
                                   fix and revert since the date, traced to the
                                   task that caused it. Reads the ledger, and
                                   fetches the project's clone to read its reverts
  crew sync [--quiet] [--all|--project P]
                                   Pull PR URLs/states from GitHub; mark merged tasks;
                                   raise to high the risk of tasks whose branch
                                   touches SENSITIVE_PATHS; report tasks that need a
                                   restack or a retarget
  crew tick                        Do what needs no decision, across all projects:
                                   sync, record new PR comments, red CI and
                                   conflicts, send review rounds and restacks to
                                   idle workers, nudge workers whose turn ended
                                   mid-task, mark tasks waiting on you, clean up
                                   merged tasks and done spikes (run by a timer)
  crew pause [--project P|--all] [--force]
                                   Stop the project's work without losing any:
                                   nothing new starts, sessions mid-turn are told to
                                   record where they are, and any still mid-turn
                                   after WRAP_UP_SECONDS are stopped. --force stops
                                   every session of the project now
  crew pause --hook                From a PostToolUse hook: tell a session crew
                                   started to wrap up once its project is paused
                                   (always exits 0)
  crew resume [--project P|--all]  Let a paused project's work go on; the next tick
                                   follows up what the pause interrupted
  crew list [--all|--project P] [--status S]
                                   Compact task list
  crew show TASK | crew path TASK  Print a task file / its path
  crew find TASK                   Print the resolved task id
  crew get TASK KEY                Read a front-matter field
  crew set TASK KEY VALUE...       Write a front-matter field (not status pr-open:
                                   crew hand-over sets that). TASK can be a
                                   proposal's file; an accepted one keeps its task
  crew log TASK MESSAGE...         Append to the task's Log
  crew cleanup TASK|--merged [--project P] [--force]
                                   Remove worktree/local branch of merged tasks and
                                   done spikes
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
  crew goal followup GOAL          Resume the goal lead with your unsent notes, its
                                   workers' new questions and its spikes' reports
                                   (run by crew tick)
  crew goal done GOAL              Close a goal whose tasks are all finished
                                   (run by crew tick)
  crew goal abandon GOAL           Stop the goal lead; its tasks stay as they are
  crew goal path GOAL              Print the goal file's path

Review and proposals  (PROPOSAL = id, unique substring, or file path)
  crew observe [TASK|PATH] [--base REF]
                                   Run the casual observer headless on a branch
  crew gardener [--all|--project P]
                                   Run the gardener over newly merged commits, and
                                   the misses agent over your comments on newly
                                   merged PRs
  crew review-eval [--project P] [--task T] [--limit N] [--rubric PATH]
                                   Rerun the reviewer on past misses (your comments
                                   and later fixes after automated review) and
                                   report which it finds now, compared with the
                                   last run. Each case costs a review: --limit N
                                   runs the first N. --rubric tests another rubric
  crew propose "<title>" [--why TEXT] [--revisit-when TEXT] [--source S]
               [--project P]
                                   Write down an idea worth keeping as a proposal;
                                   prints its path. S is lead (the default), user,
                                   gardener, misses or worker:TASK
  crew proposals [--any-status] [--all|--project P]
                                   List open proposals (--any-status: every one),
                                   with who wrote each and when to revisit it
  crew promote PROPOSAL [--branch NAME]
                                   Turn a proposal into a draft task

Project memory  (facts about a project you approved, which its agents load)
  crew remember "<fact>" --why TEXT [--source S] [--project P]
                                   Suggest a fact for the project's memory; it waits
                                   on you in memory-suggestions.md. S is lead (the
                                   default), user, gardener, misses or worker:TASK
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
