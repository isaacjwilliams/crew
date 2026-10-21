#!/usr/bin/env bash
# crew-guard write guard allow/block table, plus the permission config it relies on.
. "$(dirname "$0")/lib.sh"

SB=$(new_sandbox guard)
isolate_home "$SB/home"
G=$REPO_DIR/bin/crew-guard
L=$SB/ledger; mkdir -p "$L"
M=$SB/main; W=$SB/wt
git init -q "$M" && git -C "$M" commit -q --allow-empty -m init && git -C "$M" worktree add -q -b agent/x "$W" 2>/dev/null

# w EXPECT ROLE CWD PATH [CLAUDE_PROJECT_DIR]
w() {
  local exp=$1 role=$2 dir=$3 p=$4 pd=${5:-} json out rc got
  json=$(jq -n --arg p "$p" --arg d "$dir" '{cwd:$d,tool_input:{file_path:$p}}')
  out=$(printf '%s' "$json" | CLAUDE_PROJECT_DIR=$pd CREW_HOME=$L "$G" "$role" 2>&1); rc=$?
  got=$([[ $rc == 0 ]] && echo allow || echo block)
  check "$role [$exp, got $got]: $p (cwd $dir)${out:+ → $out}" "[[ $got == $exp ]]"
}

# --- write guard
w allow worker "$W" "$W/src/a.ts"
w allow worker "$W" "src/rel.ts"
w block worker "$W" "$M/src/a.ts"
w block worker "$W" "$W/../main/x"
w allow worker "$W" "$L/projects/p/tasks/001-x.md"
w allow worker "$L" "$W/src/a.ts" "$W"          # session cd'd into the ledger; project dir is the worktree
w block worker "$M" "$M/a"                      # worker accidentally started in the main checkout
w block lead "$M" "$M/a"
w allow lead "$M" "$L/projects/p/tasks/001-x.md"
w block gardener "$W" "$W/a"
w allow gardener "$W" "$L/projects/p/proposals/x.md"

# --- in the crew home, only the state paths (the ledger) take edits, for every role
for r in lead worker gardener; do
  d=$W; [[ $r == worker ]] || d=$M
  w allow $r "$d" "$L/projects/p/tasks/1.md"
  w allow $r "$d" "$L/reports/x.md"
  for f in bin/crew agents/lead.md config/defaults.env lead/.claude/settings.json; do w block $r "$d" "$L/$f"; done
done
check "crew-guard's state paths are crew's" '[[ $(grep -m1 "^STATE_PATHS=" "$G") == $(grep -m1 "^STATE_PATHS=" "$REPO_DIR/bin/crew") ]]'
tracked=$(git -C "$REPO_DIR" ls-files -- projects reports config/crew.env host)
check "VERSION holds a semantic version" 'grep -qxE "[0-9]+\.[0-9]+\.[0-9]+" "$REPO_DIR/VERSION"'
check "the repo tracks no state: nothing under projects/ but .gitkeep, no reports/, config/crew.env or host (got: ${tracked//$'\n'/ })" '[[ $tracked == projects/.gitkeep ]]'

printf '{}' | CREW_HOME=$L "$G" bash >/dev/null 2>&1; rc=$?
check "unknown role blocks (rc=$rc)" "[[ $rc == 2 ]]"
# --- every hook in the agent files passes a role the guard accepts, and none hooks Bash
roles=$(grep -ho 'crew-guard" [a-z]*' "$REPO_DIR"/agents/*.md | awk '{print $2}' | sort -u | tr '\n' ' ')
check "agent hooks use guard roles ($roles)" "[[ '$roles' == 'gardener lead worker ' ]]"
check "no agent hooks Bash" "! grep -q 'matcher: \"Bash\"' $REPO_DIR/agents/*.md"
# --- the lead and workers inherit every tool, MCP servers included, so their settings carry the guard
#     too: settings hooks also fire for the subagents they spawn, which their frontmatter hooks don't cover.
A=$REPO_DIR/agents; C=$REPO_DIR/config; LS=$REPO_DIR/lead/.claude/settings.json
mkdir -p "$L/bin" && ln -sf "$G" "$L/bin/crew-guard"
# s EXPECT ROLE PATH: run the settings file's hook command the way Claude Code would, from the worktree
s() {
  local exp=$1 role=$2 p=$3 cmd json rc got f
  f=$C/worker-settings.json; [[ $role == lead ]] && f=$LS
  cmd=$(jq -r '.hooks.PreToolUse[] | select(.matcher == "Edit|Write|NotebookEdit") | .hooks[0].command' "$f")
  json=$(jq -n --arg p "$p" --arg d "$W" '{cwd:$d,tool_input:{file_path:$p}}')
  printf '%s' "$json" | CLAUDE_PROJECT_DIR=$W CREW_HOME=$L bash -c "$cmd" >/dev/null 2>&1; rc=$?
  got=$([[ $rc == 0 ]] && echo allow || echo block)
  check "${f#"$REPO_DIR"/} hook [$exp, got $got]: $p" "[[ $got == $exp ]]"
}
s allow worker "$W/src/a.ts"
s block worker "$M/src/a.ts"
s allow lead "$L/projects/p/tasks/001-x.md"
s block lead "$W/src/a.ts"
for f in lead:lead implementer:worker goal-lead:lead; do
  a=${f%:*} r=${f#*:}
  check "$a has no tools allowlist (it would hide the repo's MCP servers)" "! grep -q '^tools:' $A/$a.md"
  check "$a disallows EnterWorktree and ExitWorktree" "grep '^disallowedTools:' $A/$a.md | grep -q 'EnterWorktree, ExitWorktree'"
  check "$a's frontmatter runs crew-guard as $r" "grep -q 'bin/crew-guard\" $r.\$' $A/$a.md"
  sf=$C/worker-settings.json; [[ $r == lead ]] && sf=$LS
  for g in lead implementer gardener goal-lead; do
    check "${sf#"$REPO_DIR"/} denies Agent($g)" "jq -e >/dev/null '.permissions.deny | index(\"Agent($g)\")' $sf"
  done
done
for a in implementer goal-lead; do
  check "$a disallows AskUserQuestion and scheduled wakeups (nobody watches a background session)" "grep '^disallowedTools:' $A/$a.md | grep -q 'AskUserQuestion.*ScheduleWakeup'"
done

# --- no Claude Code sandbox (on Linux it blocks localhost, which test suites need); lead and workers in auto mode
for f in "$LS" "$C"/*-settings.json; do
  check "${f#"$REPO_DIR"/}: no sandbox" "jq -e >/dev/null '.sandbox == null' $f"
done
for f in "$LS" "$C/worker-settings.json"; do
  check "${f#"$REPO_DIR"/} commits the agent's ledger edits after each Edit or Write" "jq -e >/dev/null '.hooks.PostToolUse[] | select(.matcher == \"Edit|Write|NotebookEdit\") | .hooks[0].command == \"\\\"\${CREW_HOME:-\$HOME/crew}/bin/crew\\\" commit --hook\"' $f"
done
check "a session in the home's lead/ runs as the lead" "jq -e >/dev/null '.agent == \"lead\"' $LS"
check "the lead's settings leave the permission mode to LEAD_PERMISSION_MODE" "jq -e >/dev/null '.permissions.defaultMode == null' $LS"
check "a goal lead gets the lead's settings without their agent" "grep -qF 'jq -c '\''del(.agent)'\'' \"\$CREW_HOME/lead/.claude/settings.json\"' $REPO_DIR/bin/crew"
for f in worker readonly; do
  check "$f: network left open" "jq -e >/dev/null '.permissions.allow | index(\"WebFetch(domain:*)\")' $C/$f-settings.json"
done
check "lead and workers default to auto mode" \
  "grep -qx 'LEAD_PERMISSION_MODE=\"auto\"' $C/defaults.env && grep -qx 'WORKER_PERMISSION_MODE=\"auto\"' $C/defaults.env"
# --- tripwires: the worker's GitHub allowances match its prompt
for r in 'gh pr merge *' 'gh pr close *' 'gh pr review *' 'git push --force *' 'git push -f *'; do
  check "worker denies $r" "jq -e >/dev/null --arg r 'Bash($r)' '.permissions.deny | index(\$r)' $C/worker-settings.json"
done
for r in 'git push -u origin HEAD' 'git push --force-with-lease' 'gh pr create *' 'gh pr edit *'; do
  check "worker allows $r" "jq -e >/dev/null --arg r 'Bash($r)' '.permissions.allow | index(\$r)' $C/worker-settings.json"
done
check "the implementer pushes after every commit, with the push commands its settings allow" \
  "grep -q 'Push after every commit, .git push -u origin HEAD. the first time and .git push. after that' $A/implementer.md && jq -e >/dev/null '.permissions.allow | index(\"Bash(git push -u origin HEAD)\") and index(\"Bash(git push)\")' $C/worker-settings.json"
check "the implementer stops when this machine no longer runs the crew" "grep -q 'this machine no longer runs the crew (.crew runs on <machine>.), stop there' $A/implementer.md"
check "implementer prompt spells out the GitHub allowances" "grep -q '^## On GitHub, you may do exactly this' $REPO_DIR/agents/implementer.md"
check "implementer answers every kind of review comment with crew reply, and posts no other comments" \
  "grep -q 'reply to the review comments on it with .crew reply.: inline threads, conversation comments and review summaries' $A/implementer.md && grep -q 'posting any comment that isn.t a .crew reply.' $A/implementer.md"
# --- stacks: crew restack's note and the dispatch prompt point at sections the implementer must have
check "implementer may change its PR's base only when a restack says so" \
  "grep -q 'its base branch when a restack tells you to' $A/implementer.md"
check "implementer has the sections crew's stack prompts name" \
  "grep -q '^## Workflow: restack' $A/implementer.md && grep -q '^## Stacked tasks' $A/implementer.md && grep -q 'follow your restack workflow' $C/../bin/crew && grep -q 'Stacked tasks.. in your instructions' $C/../bin/crew"
check "implementer records the new base only after pushing the restack" \
  "awk '/^## Workflow: restack/ {w=1} w && /git push --force-with-lease/ && !p {p=NR} w && /crew set <task> base/ && !b {b=NR} END {exit !(p && b && p < b)}' $A/implementer.md"
check "lead restacks with crew restack and never edits base or depends_on to force a dispatch" \
  "grep -q 'run .crew restack <task>.' $A/lead.md && grep -q 'Never clear .depends_on. or edit .base. to get a dispatch through' $A/lead.md"
# --- conversations are disposable: intake is written to the ledger as it happens, and agents trust it over a summary
check "lead opens the draft task before investigating" \
  "awk '/crew new \"<title>\" --branch/ && !d {d=NR} /^[0-9]+\\. \\*\\*Investigate\\*\\*/ {i=NR} END {exit !(d && i && d < i)}' $A/lead.md"
for f in lead implementer; do
  check "$f rereads the task file after a compaction" "grep -q 'compacted' $A/$f.md && grep -qi 'reread' $A/$f.md"
done
check "a question waiting on the user is an unchecked item under Open questions, ticked by whoever records the answer" \
  "grep -q 'as an unchecked item (.- \[ \] ….)' $A/implementer.md && grep -q 'tick the question (.- \[x\].)' $A/implementer.md && grep -q 'tick the question (.- \[x\].)' $A/lead.md && grep -q 'Whoever records the answer under' $C/../templates/task.md"
check "collaborators' comments wait for the user: the lead asks, and workers leave them alone unless told" \
  "grep -q 'never acted on without the user' $A/lead.md && grep -q 'quote a collaborator.s PR comment are for the user' $A/implementer.md"
check "the lead runs crew tick at the start of a session and for status, so rounds and restacks go out without the timer" \
  "grep -q '^initialPrompt: .*crew tick; crew status' $A/lead.md && grep -q 'run .crew tick. and then .crew status.' $A/lead.md"
check "the tick service stops only crew when a run ends, so the workers it resumed keep running" \
  "grep -qx 'KillMode=process' $REPO_DIR/systemd/crew-tick.service"
L_UNIT=$REPO_DIR/systemd/crew-lead.service
check "the lead service runs crew serve, restarts it, and leaves it stopped when serve exits 3 (not the host)" \
  "grep -qx 'ExecStart=%h/crew/bin/crew serve' $L_UNIT && grep -qx 'Restart=always' $L_UNIT && grep -qx 'RestartSec=30' $L_UNIT && grep -qx 'RestartPreventExitStatus=3' $L_UNIT && grep -qx 'KillMode=process' $L_UNIT && grep -qx 'EXIT_NOT_HOST=3' $REPO_DIR/bin/crew"
check "crew followup's prompt, not the worker, decides whether a review round gets replies" \
  "grep -q 'Your prompt says whether to reply' $A/implementer.md && ! grep -q REPLY_TO_THREADS $A/implementer.md && grep -q 'Reply to each comment in the round with' $C/../bin/crew"
check "the implementer takes the user's taste from their CLAUDE.md, not a copy of its own" \
  "grep -q '~/.claude/CLAUDE.md. with their taste' $A/implementer.md && ! grep -q '^## Engineering standards' $A/implementer.md"
check "plan-reviewer reads the code at the task's base in the project's repo, found with crew config, through git -C commands the lead allows" \
  "grep -q 'as it is on .origin/<base>.' $A/plan-reviewer.md && grep -q 'crew config REPO --project <project>' $A/plan-reviewer.md && grep -q 'git -C <repo> show origin/<base>:<path>' $A/plan-reviewer.md && grep -q 'git -C <repo> grep' $A/plan-reviewer.md && grep -q 'git -C <repo> fetch' $A/plan-reviewer.md && jq -e >/dev/null '.permissions.allow | index(\"Bash(git -C * grep *)\") and index(\"Bash(git -C * show *)\") and index(\"Bash(git -C * fetch *)\") and index(\"Bash(git -C * log *)\") and index(\"Bash(git -C * diff *)\") and index(\"Bash(crew *)\")' $LS"
check "plan-reviewer reads the project's AGENTS.md at the task's base, since it doesn't load" "grep -q 'Start with the project.s AGENTS.md or CLAUDE.md at that ref' $A/plan-reviewer.md"
check "the lead reads code in its crew checkout, after the project's AGENTS.md, which doesn't load for it" \
  "grep -q 'Run .crew checkout <p>. and read the project.s AGENTS.md' $A/lead.md && grep -q 'not for you: read it in your checkout' $A/lead.md"
check "the lead names the project in every crew new, and asks when more than one fits" "grep -q 'Name the project in every .crew new.' $A/lead.md && grep -q 'more than one fits, ask' $A/lead.md"
check "the lead's hard rules keep it off its reading copy too" "grep -q 'That covers the user.s checkout, which is theirs, and your reading copy' $A/lead.md"
check "goal leads read code in their working directory, a checkout, not through crew checkout" "grep -q 'Read code in your working directory.' $A/goal-lead.md"
check "the lead function starts the lead in the home's lead/, in LEAD_PERMISSION_MODE" "grep -q 'pushd \$CREW_HOME/lead' $REPO_DIR/fish/functions/lead.fish && grep -qF -- '--permission-mode (crew config LEAD_PERMISSION_MODE)' $REPO_DIR/fish/functions/lead.fish"
check "lead relays answers to a finished worker with crew followup, not SendMessage" \
  "grep -q 'Only if .crew followup. refuses' $A/lead.md"
# --- the plan is where structure gets decided, so it has to be visible there and in what reaches the user
check "the brief has a Structure section that crew ready requires and the lead shows on its own line" \
  "grep -qx '## Structure' $C/../templates/task.md && grep -q '\"## Structure\"' $C/../bin/crew && grep -q 'Give structure its own line' $A/lead.md"
check "the lead quotes the user's own words in the Goal" \
  "grep -q 'own description of the problem, quoted' $C/../templates/task.md && grep -q 'own description of the problem, quoted' $A/lead.md"
check "design findings a worker declines because the brief settles them reach the user" \
  "grep -q 'Findings the brief settled' $A/implementer.md && grep -q 'Findings the brief settled' $A/lead.md"
check "workers remove a check the brief prescribed when it can never change the result" \
  "grep -q 'if a finding shows a prescribed check can never change the result, it.s right, so remove it' $A/implementer.md"
check "the lead states conditions in a brief as rules, not expressions" \
  "grep -q 'State a condition as the rule it enforces' $A/lead.md"
check "the lead flags the cost of stacking on an unreviewed PR" \
  "grep -q 'Stacking on an unreviewed PR' $A/lead.md"
# --- plan review: an independent, read-only reader of the brief, run by the lead before the user approves
check "plan-reviewer is read-only (a tools allowlist without Edit, Write or NotebookEdit)" \
  "grep -qx 'tools: Read, Glob, Grep, Bash' $A/plan-reviewer.md"
check "the lead starts plan-reviewer on a finished brief with only its path, then shows the summary without waiting" \
  "awk '/Spawn the .plan-reviewer. subagent with the task file.s path and nothing else/ && !r {r=NR} /Straight away, show the user a compact summary/ && !s {s=NR} END {exit !(r && s && r < s)}' $A/lead.md"
check "plan-reviewer always runs in the background, and the lead dispatches only once its review is settled" \
  "grep -qx 'background: true' $A/plan-reviewer.md && grep -q 'Dispatch only on a go-ahead, after the plan review' $A/lead.md && grep -q 'say you.ll dispatch once it.s in' $A/lead.md"
check "plan review findings reach the user" "grep -q 'tell the user each finding the reviewer marked Raise' $A/lead.md"
check "worker-settings.json denies Agent(plan-reviewer) (plan review belongs to the lead)" \
  "jq -e >/dev/null '.permissions.deny | index(\"Agent(plan-reviewer)\")' $C/worker-settings.json"

# --- the goal lead follows the lead's intake by reading those sections of the installed lead.md
for h in "## Intake: turning a request into a brief" "### The brief standard" "## Stacked tasks"; do
  check "goal-lead reads lead.md's \"${h#\#* }\", which still exists" "grep -qxF '$h' $A/lead.md && grep -qF '\"${h#\#* }\"' $A/goal-lead.md"
done
check "goal-lead parks a question the way crew counts open questions, and logs one it leaves for the user as lead.md expects" \
  "grep -q 'as an unchecked item (.- \[ \] ….)' $A/goal-lead.md && grep -q 'For the user: <why>' $A/goal-lead.md && grep -q 'Log says \"For the user\"' $A/lead.md"
check "goal leads may read the installed lead.md without a prompt" "jq -e >/dev/null '.permissions.allow | index(\"Read(~/.claude/agents/lead.md)\")' $LS"
check "goal-lead never dispatches; crew tick does" "grep -q 'Never .crew dispatch.: .crew tick. dispatches your ready tasks' $A/goal-lead.md"

summary guard
