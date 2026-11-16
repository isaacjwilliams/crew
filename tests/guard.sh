#!/usr/bin/env bash
# crew-guard write guard allow/block table, plus the permission config it relies on.
. "$(dirname "$0")/lib.sh"

SB=$(new_sandbox guard)
isolate_home "$SB/home"
# crew's source, for the checks that the agents' prompts agree with it.
# shellcheck disable=SC2034  # read inside check strings
SRC=$(cat "$REPO_DIR"/cmd/crew/*.go)
L=$SB/ledger; make_home "$L"
G=$L/bin/crew-guard
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
# --- only the lead edits a project's memory; the others suggest with crew remember
w block worker "$W" "$L/projects/p/memory.md"
w block gardener "$W" "$L/projects/p/memory.md"
w block worker "$W" "$L/projects/p/Memory.md"
w allow lead "$M" "$L/projects/p/memory.md"
# --- no agent edits a project's MCP servers, which every lead starts without asking; the user does
for role in worker gardener lead; do
  w block $role "$M" "$L/projects/p/mcp.json"
  w block $role "$M" "$L/projects/p/mcp.local.json"
done
w block worker "$W" "$L/projects/p/MCP.json"
w allow lead "$M" "$L/projects/p/tasks/mcp.json"   # only the project's own file, not one of that name in a task folder
# --- a path is judged by where it leads, and one that can't be worked out is blocked
ln -s "$L/bin" "$W/into-home"; ln -s "$L/projects" "$SB/ledger-link"; ln -s loop "$W/loop"
w block worker "$W" "$W/into-home/crew"
w allow lead "$M" "$SB/ledger-link/p/tasks/001-x.md"
w block worker "$W" "$W/loop/a.ts"
printf '{not json' | CREW_HOME=$L "$G" lead >/dev/null 2>&1; rc=$?
check "hook input that isn't JSON blocks (rc=$rc)" "[[ $rc == 2 ]]"

# --- in the crew home, only the state paths (the ledger) take edits, for every role
for r in lead worker gardener; do
  d=$W; [[ $r == worker ]] || d=$M
  w allow $r "$d" "$L/projects/p/tasks/1.md"
  w allow $r "$d" "$L/reports/x.md"
  for f in bin/crew agents/lead.md config/defaults.env lead/.claude/settings.json; do w block $r "$d" "$L/$f"; done
done
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
# section HEADING FILE: the lines of one section, from its heading to the next heading of any level.
section() { awk -v h="$1" '/^#+ / {w = ($0 == h)} w' "$2"; }
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
  for g in lead implementer gardener misses goal-lead; do
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
  check "${f#"$REPO_DIR"/} commits the agent's ledger edits after each Edit, Write or Bash call" "jq -e >/dev/null '.hooks.PostToolUse[] | select(.matcher == \"Edit|Write|NotebookEdit|Bash\") | .hooks[0].command == \"\\\"\${CREW_HOME:-\$HOME/crew}/bin/crew\\\" commit --hook\"' $f"
done
for f in "$LS" "$C/worker-settings.json" "$C/gardener-settings.json" "$C/readonly-settings.json"; do
  # Bash(git -C * show *) would also approve git -C . -c core.pager=<script> show.
  check "${f#"$REPO_DIR"/}: no allow rule has a * before the subcommand, where it would let any option through" \
    "jq -e >/dev/null '[.permissions.allow[] | select(test(\"^Bash\\\\(.*\\\\* [^)]\"))] == []' $f"
done
# --- project memory loads at every session start for the agents crew starts, but not for crew observe's casual observer
for f in "$LS" "$C/worker-settings.json" "$C/gardener-settings.json"; do
  check "${f#"$REPO_DIR"/} loads project memory when a session starts, resumes or compacts (no matcher)" \
    "jq -e >/dev/null '.hooks.SessionStart | length == 1 and (.[0] | has(\"matcher\") | not) and .[0].hooks[0].command == \"\\\"\${CREW_HOME:-\$HOME/crew}/bin/crew\\\" memory --hook\"' $f"
done
check "readonly-settings.json, crew observe's, has no hooks" "jq -e >/dev/null '.hooks == null' $C/readonly-settings.json"
check "the gardener may suggest project memory" "jq -e >/dev/null '.permissions.allow | index(\"Bash(crew remember *)\")' $C/gardener-settings.json"
check "the gardener's allow list is the read-only one, plus crew remember" \
  "[[ \$(jq -c '.permissions.allow - [\"Bash(crew remember *)\"]' $C/gardener-settings.json) == \"\$(jq -c .permissions.allow $C/readonly-settings.json)\" ]]"
# --- the misses agent: the gardener's powers, its own job
MS=$A/misses.md
check "misses.md runs crew-guard as the gardener" "grep -q 'bin/crew-guard\" gardener.\$' $MS"
check "misses.md names the four places a miss is fixed" \
  "grep -q \"The repo's AGENTS.md\" $MS && grep -q 'A lint rule' $MS && grep -q '~/.claude/CLAUDE.md' $MS && grep -q \"Crew's review rubric\" $MS"
check "misses.md sorts each comment, and what review already raised isn't a miss" \
  "grep -q 'Sort each comment: a miss, or not' $MS && grep -q 'a question, or praise' $MS && grep -q 'a change of mind' $MS && grep -q \"the brief or the user's decisions settled\" $MS && grep -q 'something review already raised: a finding left for the user, a finding written up as a proposal, or a split or disputed finding' $MS"
check "misses.md keeps the rubric generic: a project's miss goes to its AGENTS.md" \
  "grep -q 'it never names a framework, a language or a project' $MS && grep -q \"goes to the repo's AGENTS.md, never to the rubric\" $MS"
check "misses.md writes at most three proposals, source misses, citing every comment" \
  "grep -q 'at most three proposals' $MS && grep -q '\`source\` is \`misses\`' $MS && grep -q 'cites every comment the proposal covers' $MS"
check "misses.md marks a taste-file or rubric proposal as the user's to make, as lead.md and LEDGER.md expect" \
  "grep -qF 'Target: <file>. A change for you to make, not a task in this project.' $MS && grep -qF 'Target: <file>. A change for you to make, not a task in this project.' $REPO_DIR/LEDGER.md && grep -qF 'Proposal section starts \`Target: <file>.\`' $A/lead.md"
GD=$REPO_DIR/cmd/crew/cmd_gardener.go
check "crew gardener launches its agents from one call site, which takes the agent's name" \
  "[[ \$(grep -c '\"--agent\"' $GD) == 1 ]] && grep -q '\"--agent\", agent,' $GD"
check "crew gardener reads rounds and merges through ledger.go, with no parser of its own" "! grep -n 'PR merged\\|### Round' $GD"
check "the gardener's unit, its plist and crew help name the misses agent too" \
  "grep -q '^Description=.*misses agent' $REPO_DIR/systemd/crew-gardener.service && grep -q 'misses agent' $REPO_DIR/launchd/crew-gardener.plist && grep -A2 'crew gardener \[' $REPO_DIR/cmd/crew/help.go | grep -q 'misses agent'"
check "lead.md names the misses agent among the proposals' writers" "grep -q 'the misses agent (\`misses\`' $A/lead.md"
check "a session in the home's lead/ runs as the lead" "jq -e >/dev/null '.agent == \"lead\"' $LS"
check "the lead's settings leave the permission mode to LEAD_PERMISSION_MODE" "jq -e >/dev/null '.permissions.defaultMode == null' $LS"
check "a goal lead gets the lead's settings without their agent" "grep -qF 'lead/.claude/settings.json' <<<\"\$SRC\" && grep -qF 'lead.del(\"agent\")' <<<\"\$SRC\""
for f in worker gardener readonly; do
  check "$f: network left open" "jq -e >/dev/null '.permissions.allow | index(\"WebFetch(domain:*)\")' $C/$f-settings.json"
done
check "lead and workers default to auto mode" \
  "grep -qx 'LEAD_PERMISSION_MODE=\"auto\"' $C/defaults.env && grep -qx 'WORKER_PERMISSION_MODE=\"auto\"' $C/defaults.env"
# --- tripwires: the worker's GitHub allowances match its prompt
for r in 'gh pr merge *' 'gh pr close *' 'gh pr review *' 'gh run cancel *' 'gh run delete *' 'git push --force *' 'git push -f *'; do
  check "worker denies $r" "jq -e >/dev/null --arg r 'Bash($r)' '.permissions.deny | index(\$r)' $C/worker-settings.json"
done
for r in 'git push -u origin HEAD' 'git push --force-with-lease' 'git push --force-with-lease=*' 'gh pr create *' 'gh pr edit *' 'gh pr ready *' 'gh run view *' 'gh run rerun --failed *' 'gh run watch *'; do
  check "worker allows $r" "jq -e >/dev/null --arg r 'Bash($r)' '.permissions.allow | index(\$r)' $C/worker-settings.json"
done
check "the implementer pushes after every commit, with the push commands its settings allow" \
  "grep -q 'Push after every commit, .git push -u origin HEAD. the first time and .git push. after that' $A/implementer.md && jq -e >/dev/null '.permissions.allow | index(\"Bash(git push -u origin HEAD)\") and index(\"Bash(git push)\")' $C/worker-settings.json"
check "the implementer stops when this machine no longer runs the crew" "grep -q 'this machine no longer runs the crew (.crew runs on <machine>.), stop there' $A/implementer.md"
check "the implementer force-pushes on its own, with an explicit lease its settings allow" \
  "grep -q 'record where origin has it: .git rev-parse origin/<branch>.' $A/implementer.md && grep -qF 'git push --force-with-lease=<branch>:<recorded sha> origin <branch>' $A/implementer.md && grep -q 'Run the push as a command on its own, with no' $A/implementer.md"
check "the restack note's push uses the lease form the implementer is told to use" \
  "grep -qF 'git push --force-with-lease=%s:%s origin %s' <<<\"\$SRC\" && grep -q 'Push with the .git push --force-with-lease=…. command the note gives' $A/implementer.md"
check "implementer prompt spells out the GitHub allowances" "grep -q '^## On GitHub, you may do exactly this' $REPO_DIR/agents/implementer.md"
check "implementer answers every kind of review comment with crew reply, posts one automated-review summary a pass with crew review-summary, one CI comment a red build with crew ci-summary, and no other comments" \
  "grep -q 'reply to the review comments on it with .crew reply.: inline threads, conversation comments and review summaries' $A/implementer.md && grep -q '.crew review-summary <task> <file>., one summary comment for each pass of automated review' $A/implementer.md && grep -q 'Post one comment for each red build with .crew ci-summary <task> <sha> <file>.' $A/implementer.md && grep -q 'posting any comment that isn.t a .crew reply., the .crew review-summary. or a .crew ci-summary.;' $A/implementer.md"
check "implementer reads CI logs, reruns only failed jobs once and watches them with the gh run commands its settings allow, and never cancels a run" \
  "grep -q 'read the logs with .gh run view., rerun a run.s failed jobs once with .gh run rerun --failed <run>. and watch it with .gh run watch.' $A/implementer.md && grep -q 'Never rerun a whole run, and never cancel one' $A/implementer.md && grep -q '.gh run rerun --failed <run>. and then .gh run watch <run> --exit-status.' $A/implementer.md"
check "implementer names no CI service but GitHub Actions: for any other, the project says how" \
  "! grep -qiE 'semaphore|circleci|buildkite|jenkins|travis|gitlab|teamcity|drone' $A/implementer.md $C/worker-settings.json && grep -q 'For any other CI, read its logs the way the project.s AGENTS.md or memory says' $A/implementer.md"
check "a red build comes to the implementer as a round's CI line, which it handles before the comments, with the shipped template" \
  "awk '/^## Workflow: review rounds/ {w=1} w && /^4\\. If a round has a .CI failed on <sha>: <checks>. line/ && !c {c=NR} w && /^5\\. Address every comment/ && !a {a=NR} END {exit !(c && a && c < a)}' $A/implementer.md && grep -q 'fill in .templates/ci-comment.md.' $A/implementer.md && [[ -f $C/../templates/ci-comment.md ]] && grep -qF '\"- [ ] %s%s: %s\\n\\n\", ciFailedOn' <<<\"\$SRC\" && grep -qF 'ciFailedOn   = \"CI failed on \"' <<<\"\$SRC\""
check "a conflict with the base comes to the implementer as a round's line, which it handles first by merging the base in and pushing without force, then ticks" \
  "awk '/^## Workflow: review rounds/ {w=1} w && /^3\\. Fetch and fast-forward/ && /If a round has a .Conflicts with <base> at <sha>. line/ && !m {m=NR} w && /^4\\. If a round has a .CI failed on/ && !c {c=NR} END {exit !(m && c && m < c)}' $A/implementer.md && grep -q '^   - .git fetch origin., then .git merge origin/<base>.' $A/implementer.md && grep -q '^   - Run the checks, commit the merge, and push it with plain .git push., never a force-push.' $A/implementer.md && grep -q '^   - Tick the line. It needs no PR comment' $A/implementer.md && grep -qF '\"- [ ] %s%s at %s\\n\\n\", conflictsWith' <<<\"\$SRC\" && grep -qF 'conflictsWith = \"Conflicts with \"' <<<\"\$SRC\""
check "implementer's ground rule turns on the hand-over: rebase a conflict before it; after it, rebase only for a restack, and merge only when the task isn't stacked" \
  "grep -q '^- Stay on your branch. Until you first hand your PR over (first-run step 10), rebase it onto its base when it conflicts, then force-push it with a lease.*Once you.ve handed it over, never rewrite it: rebase only for a restack (below). If your task isn.t stacked, resolve a conflict by merging the base in.*A stacked task.s conflicts wait for its restack.' $A/implementer.md"
check "implementer reacts to review comments only with crew react, never with gh" \
  "grep -q 'react to them with .crew react. as .Workflow: review rounds. says. Never reply or react with .gh. directly' $A/implementer.md && grep -q 'adding any reaction but a .crew react.;' $A/implementer.md"
check "on a review round, the implementer reacts 👀 to every item before reading them, and 🚀 before changing code for one" \
  "awk '/^## Workflow: review rounds/ {w=1} w && /^1\\. .*crew react <task> eyes/ && !e {e=NR} w && /^2\\. Read the rounds/ && !r {r=NR} w && /^5\\. Address every comment/ && !a {a=NR} w && /make the change: .*before you start on it, react 🚀 with .crew react <task> rocket <id>./ && !k {k=NR} w && /^6\\. Run checks, commit/ && !c {c=NR} END {exit !(e && e < r && r < a && a < k && k < c)}' $A/implementer.md && grep -q 'react 🚀 to the comment behind each .Automated review requested (comment <id>). line, run a new pass' $A/implementer.md"
check "after automated review, the implementer carries on at the review-round step that runs the pass, not an earlier one" \
  "n=\$(sed -n 's/.*or step \\([0-9]*\\) of \"Workflow: review rounds\".*/\\1/p' $A/implementer.md) && [[ -n \$n ]] && awk -v n=\"\$n\" '/^## Workflow: review rounds/ {w=1} w && index(\$0, n \". \") == 1 {print; exit}' $A/implementer.md | grep -q 'run a new pass of .Workflow: automated review.'"
check "first run opens every PR as a draft, and only step 10 marks it ready, after a successful crew hand-over" \
  "awk '/^## Workflow: first run/ {w=1} w && /^7\\. / {o=1} w && /^8\\. / {o=0} o && /gh pr create/ {n++; if (!/gh pr create --draft/) bad=1} END {exit !(n && !bad)}' $A/implementer.md && ! grep 'gh pr ready' $A/implementer.md | grep -v '^5\\. \\*\\*Post your PR' | grep -qv '^    - Once it succeeds, if .pr_mode. is .ready.' && awk '/^10\\. \\*\\*Hand over\\*\\*: .crew hand-over <task>./ {h=NR} h && /^    - Once it succeeds, if .pr_mode. is .ready. and the PR is still a draft.*mark it ready: .gh pr ready <url>.. Only this first hand-over marks the PR ready: later ones.*leave it as it is/ && !r {r=NR} END {exit !(h && r && h < r)}' $A/implementer.md"
check "On GitHub item 5 lets a worker mark its PR ready only after crew hand-over has succeeded" \
  "grep -q '^5\\. \\*\\*Post your PR.s automated-review summary, and mark it ready after its first hand-over\\*\\*: .*.gh pr ready <url>. on your own PR only after .crew hand-over. has succeeded' $A/implementer.md"
check "the implementer records evidence for each acceptance criterion before it opens the PR, including one only the user can check" \
  "awk '/^## Workflow: first run/ {w=1} w && /^6\\. \\*\\*Record the evidence, then push\\*\\*/ && !e {e=NR} w && /^7\\. \\*\\*Open the PR\\*\\*/ && !o {o=NR} END {exit !(e && o && e < o)}' $A/implementer.md && grep -q 'A criterion only the user can check, such as a live smoke test, gets an entry saying exactly what to try' $A/implementer.md && grep -qx '## Evidence' $C/../templates/task.md"
check "the implementer fills in the PR body from its template, which has Risk, How to revert and Evidence, and no Changes" \
  "grep -q 'The PR body is .templates/pr-body.md.' $A/implementer.md && ! grep -q 'Changes.*: short bullets' $A/implementer.md && grep -qx '## Risk' $C/../templates/pr-body.md && grep -qx '## How to revert' $C/../templates/pr-body.md && grep -qx '## Evidence' $C/../templates/pr-body.md && ! grep -q '^## Changes' $C/../templates/pr-body.md"
# Step n of "Workflow: review rounds", with every paragraph it has.
review_round_step() { section "## Workflow: review rounds" "$A/implementer.md" | awk -v n="$1. " '/^[0-9]+\. / {on = index($0, n) == 1} on'; }
check "no prompt puts a task in pr-open with crew set: the first run, a review round, a restack and the lead's set-backs all hand over" \
  "! grep -rqiE 'status pr-open|back to .?pr-open' $A/ && grep -q '^10\\. \\*\\*Hand over\\*\\*: .crew hand-over <task>.' $A/implementer.md && review_round_step 9 | grep -q 'Hand over again: .crew hand-over <task>.' && awk '/^## Workflow: restack/ {w=1} w && /^6\\. / {print; exit}' $A/implementer.md | grep -q 'hand over again: .crew hand-over <task>.' && [[ \$(awk '/^## PR feedback/ {w=1; next} w && /^## / {exit} w' $A/lead.md | grep -o 'hand the task back with .crew hand-over <task>.' | wc -l) == 2 ]]"
check "a review round closes a proposal its worker wrote once the rounds did its work, or logs on it what's left, before the hand-over; LEDGER.md lets that worker accept it" \
  "s=\$(review_round_step 9 | sed '/^ *Hand over again/,\$d') && grep -qF 'crew set <proposal-file> task <task>' <<<\"\$s\" && grep -qF 'crew set <proposal-file> status accepted' <<<\"\$s\" && grep -qF 'crew log <proposal-file> \"Done in <task> (#<pr>, <short sha>)\"' <<<\"\$s\" && grep -qF 'crew log <proposal-file> \"Part done in <task> (#<pr>, <short sha>); left: …\"' <<<\"\$s\" && grep -q '^| .accepted. | .* | .crew promote., the worker that wrote it once a review round does its work, or the lead via .crew set. |\$' $REPO_DIR/LEDGER.md"
check "every pass of automated review refreshes the evidence its fixes changed, before it pushes" \
  "awk '/^## Workflow: automated review/ {w=1} w && /If your fixes changed what an .## Evidence. entry shows, rerun it/ && !e {e=NR} w && /Then run the checks and push/ && !p {p=NR} END {exit !(e && p && e <= p)}' $A/implementer.md"
# --- risk tiers: the lead sets one at intake; a high-risk worker runs a security review
check "the lead sets the risk tier at intake, with the rule for each tier in a line" \
  "grep -q 'Set it with .crew set <task> risk low|medium|high.' $A/lead.md && grep -q '^     - \\*\\*high\\*\\* when the plan touches one of the project.s sensitive paths (.crew config SENSITIVE_PATHS --project <p>.)' $A/lead.md && grep -q '^     - \\*\\*medium\\*\\* when it adds or changes structure' $A/lead.md && grep -q '^     - \\*\\*low\\*\\* when it adds none' $A/lead.md"
check "a high-risk worker runs /security-review in step 5, after the observer, and records it under Evidence" \
  "awk '/^## Workflow: first run/ {w=1} w && /^5\\. \\*\\*Pre-PR review\\*\\*/ && !p {p=NR} w && /Don.t run the observer a second time/ && !o {o=NR} w && /^   \\*\\*Security review\\*\\*, when the task.s .risk. is .high./ && !r {r=NR} w && /^6\\. / && !e {e=NR} END {exit !(p && o && r && e && p < o && o < r && r < e)}' $A/implementer.md && grep -q 'run ./security-review. and act on its findings.*add a .### Security review. entry under .## Evidence.' $A/implementer.md"
check "a tier raised after the hand-over sends the worker back for a security review: the lead relays it, and a review round's hand-over needs it" \
  "grep -q 'A .pr-open. row that says .high: no security review yet.*crew followup <task> --note.*and hand over again with crew hand-over' $A/lead.md && review_round_step 9 | grep -q 'Hand over again: .crew hand-over <task>. checks the round.s commits as it did the first run.s. When it refuses, do what first-run step 10 says'"
check "the lead hands a task back with crew hand-over, and fixes only the refusals that are its own" \
  "grep -q '^.crew hand-over. is the only way into .pr-open.*Evidence and a security review are the worker.s to add.*Acceptance criteria that aren.t .- . items are yours to fix.*When a worker asks you about them in its first run, rewrite them and answer it: it hands over itself' $A/lead.md"
check "the PR body's Risk section opens with the tier and its look" \
  "grep -q 'Its Risk section opens with the task.s tier and the look it asks for' $A/implementer.md && [[ \$(awk '/^## Risk\$/ {r=1; next} r && NF {print; exit}' $C/../templates/pr-body.md) == '<Low | Medium | High> risk: <the look>.' ]]"
# --- spikes: a throwaway try that ends in a report, not a PR
spike_workflow() { awk '/^## Workflow: spike/ {on=1; next} on && /^## / {exit} on' "$A/implementer.md"; }
check "a spike leaves first run after step 3 for Workflow: spike" \
  "awk '/^## Workflow: first run/ {w=1} w && /^3\\. / && !t {t=NR} w && /If your task is a spike .*follow .Workflow: spike. from here instead of the steps below/ && !s {s=NR} w && /^4\\. / && !f {f=NR} END {exit !(t && s && f && t < s && s < f)}' $A/implementer.md"
check "Workflow: spike commits locally and never pushes, writes a report that starts with its answer, and hands over" \
  "spike_workflow | grep -q 'only locally: never push' && spike_workflow | grep -q 'a .## Report. section in your task file.*Its first line is .Answer: <the answer, in a line>.' && spike_workflow | grep -q '^3\\. \\*\\*Hand over\\*\\*, last: .crew hand-over <task>.' && ! spike_workflow | grep -q '^4\\. '"
check "Workflow: spike opens no PR and runs no observer, automated review or evidence" \
  "! spike_workflow | grep -qE 'gh pr create|git push -u|casual-observer|/pr-review|crew review-summary' && spike_workflow | grep -q 'Tests, the observer, a PR, automated review and .## Evidence. are for builds, so skip them'"
check "the lead lists what's still to learn and opens a spike for each approach, then puts the answers side by side" \
  "awk '/^3\\. \\*\\*Investigate\\*\\*/ && !i {i=NR} /^4\\. \\*\\*Find out what.s still to learn\\.\\*\\*/ && !l {l=NR} /^5\\. \\*\\*Propose a plan\\*\\*/ && !p {p=NR} END {exit !(i && l && p && i < l && l < p)}' $A/lead.md && grep -q 'crew new \"<what it tries>\" --kind spike --informs <draft>' $A/lead.md && grep -q 'write their answers side by side under the item, tick it, and finish the plan from them' $A/lead.md && grep -q 'It needs the user.s go-ahead like any task, but no plan review' $A/lead.md"
check "the goal lead opens and readies spikes without parking, and hears when one reports" \
  "grep -q '^- \\*\\*Spikes\\*\\* (step 4) are yours to open and mark ready without parking' $A/goal-lead.md && grep -q '^- \\*\\*A spike reported\\*\\*' $A/goal-lead.md && grep -q '^- \\*\\*Run the plan review\\*\\* (step 8)' $A/goal-lead.md && grep -q '^8\\. \\*\\*Finish the brief\\*\\*.*start a plan review' $A/lead.md"
check "crew tells the goal lead a spike reported in the words its workflow names" \
  "grep -qF '\"%s reported: %s\", t.ID(), t.Answer()' <<<\"\$SRC\""
# --- fixes: a defect live in production, proved by a failing test and traced to its cause
check "a fix's worker commits a failing test that reproduces the defect before the fix, in step 4, and its evidence shows both runs" \
  "awk '/^## Workflow: first run/ {w=1} w && /^4\\. / && !f {f=NR} w && /^   If your task is a fix .*prove the defect before you fix it/ && !x {x=NR} w && /^5\\. / && !p {p=NR} END {exit !(f && x && p && f < x && x < p)}' $A/implementer.md && grep -q 'Commit a test that reproduces the defect and fails on the code as it is, before the fix commit' $A/implementer.md && grep -q 'shows both runs: the test failing before the fix, and passing after' $A/implementer.md"
check "a fix's failing test takes the place of pinning the defective behaviour, and pins of what must stay may come first" \
  "grep -q 'If you also pin behaviour that must stay the same, those passing tests may come first: the failing test takes the place of pinning the defective behaviour, and it still comes before the fix commit' $A/implementer.md"
# The checks a fix's missed_by can name, as lead.md lists them: "`plan-review`, `tests`, … or `user-review`".
missed_by() { sed -n 's/^var missedBy = \[\]string{\(.*\)}$/\1/p' <<<"$SRC" | sed -e 's/"\([a-z-]*\)"/`\1`/g' -e 's/, \([^,]*\)$/ or \1/'; }
check "the lead makes a fix only of a defect live in production, and anything else a refine or a build" \
  "grep -q 'A \\*\\*fix\\*\\* (.--kind fix.) is only for a defect live in production: released or deployed, and affecting users now' $A/lead.md && grep -q 'A defect in unreleased work, a bug found in review, or polish is a \\*\\*refine\\*\\*' $A/lead.md"
check "the lead traces a fix to the task that introduced it and names the check that missed it, from crew's list" \
  "grep -q 'Set .fixes. to the merged task whose change introduced it: find the commit with .git log -S' $A/lead.md && grep -q 'set .unknown. and say why under Context' $A/lead.md && [[ -n \$(missed_by) ]] && grep -qF \"Set \\\`missed_by\\\` to the check that should have caught it, from why it didn't: \$(missed_by).\" $A/lead.md"
# --- stacks: crew restack's note and the dispatch prompt point at sections the implementer must have
check "implementer may change its PR's base only when a restack says so" \
  "grep -q 'its base branch when a restack tells you to' $A/implementer.md"
check "implementer has the sections crew's stack prompts name" \
  "grep -q '^## Workflow: restack' $A/implementer.md && grep -q '^## Stacked tasks' $A/implementer.md && grep -q 'follow your restack workflow' <<<\"\$SRC\" && grep -q 'Stacked tasks.. in your instructions' <<<\"\$SRC\""
check "implementer records the new base only after pushing the restack" \
  "awk '/^## Workflow: restack/ {w=1} w && /git push --force-with-lease=/ && !p {p=NR} w && /crew set <task> base/ && !b {b=NR} END {exit !(p && b && p < b)}' $A/implementer.md"
check "a retarget note is one the implementer's restack workflow knows: gh pr edit alone, and nothing to rebase" \
  "grep -qF 'note := fmt.Sprintf(\"Retarget (%s): ' <<<\"\$SRC\" && grep -qF 'Run \`gh pr edit --base %s\`, then follow your restack workflow.' <<<\"\$SRC\" && grep -q 'A retarget note, one that starts \"Retarget\", has nothing to rebase or push: run the .gh pr edit --base. command it gives' $A/implementer.md"
check "a restack note's retarget comes after the push, as the implementer runs it" \
  "grep -qF 'not your base: after pushing, run \`gh pr edit --base %s\`.' <<<\"\$SRC\" && grep -q 'If the note asks you to retarget your PR (.gh pr edit --base.) or to record a new base (.crew set <task> base.), run what it gives, only after the push' $A/implementer.md"
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
check "a request given when the lead starts replaces the wait after its briefing" \
  "grep -q '^initialPrompt: .*If my request follows, .*start on the request. Otherwise wait for me.' $A/lead.md"
check "the lead knows other lead sessions may run beside it, rereads a task file before acting on it, and doesn't pick up drafts on its own" \
  "grep -q 'several lead sessions at once' $A/lead.md && grep -q 'Reread a task file right before you change it or act on it' $A/lead.md && grep -q 'pick up a draft on your own' $A/lead.md && ! grep -q 'single point of contact' $A/lead.md"
check "the tick service stops only crew when a run ends, so the workers it resumed keep running" \
  "grep -qx 'KillMode=process' $REPO_DIR/systemd/crew-tick.service"
L_UNIT=$REPO_DIR/systemd/crew-lead.service
check "the lead service runs crew serve, restarts it, and leaves it stopped when serve exits 3 (not the host)" \
  "grep -qx 'ExecStart=%h/crew/bin/crew serve' $L_UNIT && grep -qx 'Restart=always' $L_UNIT && grep -qx 'RestartSec=30' $L_UNIT && grep -qx 'RestartPreventExitStatus=3' $L_UNIT && grep -qx 'KillMode=process' $L_UNIT && grep -q 'exitNotHost = 3' <<<\"\$SRC\""
check "crew followup's prompt, not the worker, decides whether a review round gets replies" \
  "grep -q 'Your prompt says whether to reply' $A/implementer.md && ! grep -q REPLY_TO_THREADS $A/implementer.md && grep -q 'Reply to each comment in those rounds with' <<<\"\$SRC\""
check "the implementer takes the user's taste from their CLAUDE.md, not a copy of its own" \
  "grep -q '~/.claude/CLAUDE.md. with their taste' $A/implementer.md && ! grep -q '^## Engineering standards' $A/implementer.md"
check "plan-reviewer reads the code at the task's base in the project's repo, found with crew config, through git -C" \
  "grep -q 'as it is on .origin/<base>.' $A/plan-reviewer.md && grep -q 'crew config REPO --project <project>' $A/plan-reviewer.md && grep -q 'git -C <repo> show origin/<base>:<path>' $A/plan-reviewer.md && grep -q 'git -C <repo> grep' $A/plan-reviewer.md && grep -q 'git -C <repo> fetch' $A/plan-reviewer.md && jq -e >/dev/null '.permissions.allow | index(\"Bash(crew *)\")' $LS"
check "plan-reviewer reads the project's AGENTS.md at the task's base, since it doesn't load" "grep -q 'Start with the project.s AGENTS.md or CLAUDE.md at that ref' $A/plan-reviewer.md"
check "the lead reads code in its crew checkout, after the project's AGENTS.md, which doesn't load for it" \
  "grep -q 'Run .crew checkout <p>. and read the project.s AGENTS.md' $A/lead.md && grep -q 'not for you: read it in your checkout' $A/lead.md"
check "the lead names the project in every crew new, and asks when more than one fits" "grep -q 'Name the project in every .crew new.' $A/lead.md && grep -q 'more than one fits, ask' $A/lead.md"
check "the lead's hard rules keep it off its reading copy too" "grep -q 'That covers the user.s checkout, which is theirs, and your reading copy' $A/lead.md"
check "goal leads read code in their working directory, a checkout, not through crew checkout" "grep -q 'Read code in your working directory.' $A/goal-lead.md"
check "lead relays answers to a finished worker with crew followup, not SendMessage" \
  "grep -q 'Only if .crew followup. refuses' $A/lead.md"
# --- the plan is where structure gets decided, so it has to be visible there and in what reaches the user
check "the brief has a Structure section that crew ready requires and the lead shows on its own line" \
  "grep -qx '## Structure' $C/../templates/task.md && grep -q '\"## Structure\"' <<<\"\$SRC\" && grep -q 'Give structure its own line' $A/lead.md"
check "the lead quotes the user's own words in the Goal" \
  "grep -q 'own description of the problem, quoted' $C/../templates/task.md && grep -q 'own description of the problem, quoted' $A/lead.md"
check "design findings a worker declines because the brief settles them reach the user" \
  "grep -q 'Findings the brief settled' $A/implementer.md && grep -q 'Findings the brief settled' $A/lead.md"
check "what automated review left for the user reaches the user" \
  "grep -q '.left for the user.' $A/implementer.md && grep -q 'findings marked .left for the user. under the task.s .## Automated reviews.' $A/lead.md"
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

# --- automated review: the pr-review skill, which reaches workers through --add-dir <home>, and its read-only reviewer
SK=$REPO_DIR/.claude/skills/pr-review
check "the pr-review skill is where --add-dir <home> loads it, and names its rubric by its own folder" \
  "[[ -f $SK/SKILL.md && -f $SK/rubric.md ]] && grep -q '\"--add-dir\", c.Home' <<<\"\$SRC\" && grep -qF '\${CLAUDE_SKILL_DIR}/rubric.md' $SK/SKILL.md && ! grep -q '~/.claude/skills' $SK/*.md"
check "crew review-eval's briefing has SKILL.md step 3's first line and headings, in its order" \
  "h=\$(awk '/^## 3\\. Write the briefing/ {w=1; next} /^## 4\\./ {w=0} w && /^### .## .*.\$/ {gsub(/^### .|.\$/, \"\"); printf \"%s\\\"%s\\\"\", s, \$0; s=\", \"}' $SK/SKILL.md) && [[ -n \$h ]] &&
   grep -qF \"var briefingHeadings = []string{\$h}\" <<<\"\$SRC\" && grep -q '^.Code review.\\. Then exactly these four' $SK/SKILL.md && grep -qF 'b.WriteString(\"Code review\\n\")' <<<\"\$SRC\""
check "crew review-eval prompts the reviewer as SKILL.md step 4 does" \
  "grep -qF \"> Review \\\`<from sha>..HEAD\\\` in \\\`<review root>\\\`. Use the scope exactly as given; don't widen it or reconstruct it.\" $SK/SKILL.md &&
   grep -qF '> Follow the rubric at \`\${CLAUDE_SKILL_DIR}/rubric.md\` and the briefing at \`<briefing path>\`.' $SK/SKILL.md &&
   grep -qF \"Use the scope exactly as given; don't widen it or reconstruct it. Follow the rubric at \\\`%s\\\` and the briefing at \\\`%s\\\`.\" <<<\"\$SRC\""
check "workers can run pr-review through the Skill tool" "grep -qx 'name: pr-review' $SK/SKILL.md && ! grep -q '^disable-model-invocation' $SK/SKILL.md"
check "pr-review reviews and never fixes: no step commits, pushes or posts" "! grep -qE 'git (commit|push)|gh pr (comment|edit|ready)|crew (reply|review-summary)' $SK/SKILL.md"
check "the shipped rubric is generic: it names no framework" "! grep -qiE 'rails|django|laravel|react|active ?record|active storage|action text' $SK/rubric.md"
check "reviewer is read-only (a tools allowlist without Edit, Write or NotebookEdit)" "grep -qx 'tools: Read, Glob, Grep, Bash' $A/reviewer.md"
check "reviewer leaves commit messages, which are stated intent, until it has derived intent" \
  "grep -q 'Commit messages are stated intent' $A/reviewer.md && grep -q 'log --format=%H <range>' $A/reviewer.md && ! grep -q 'diff and log' $A/reviewer.md"
check "the implementer runs pr-review after opening its PR when AUTO_REVIEWS is above 0, and stops at that many reviews" \
  "grep -q 'crew config AUTO_REVIEWS --project <project>. is above 0' $A/implementer.md && grep -q 'Run the .pr-review. skill' $A/implementer.md && grep -q 'this pass has run fewer reviews than .AUTO_REVIEWS.' $A/implementer.md && grep -qx 'AUTO_REVIEWS=3' $C/defaults.env"
check "the implementer pushes its fixes after every review, the last one included, before it posts the summary" \
  "awk '/^## Workflow: automated review/ {w=1} w && /Then run the checks and push/ && !p {p=NR} w && /post it with .crew review-summary/ && !s {s=NR} END {exit !(p && s && p < s)}' $A/implementer.md && ! grep -q 'run the checks, push, and go back' $A/implementer.md"
check "a pass is counted from the last Summary posted, one the user asked for runs at least one review, and only a draft is marked ready" \
  "grep -q 'A pass is the reviews recorded after the last .Summary posted. line' $A/implementer.md && grep -q 'A pass the user asked for runs at least one, even when .AUTO_REVIEWS. is 0' $A/implementer.md && grep -q 'and the PR is still a draft' $A/implementer.md"
check "a requested pass runs after the round's other comments are pushed, and the request gets no crew reply" \
  "grep -q 'Automated review requested (%s)' <<<\"\$SRC\" && awk '/^## Workflow: review rounds/ {w=1} w && /^6\\. Run checks, commit/ && !p {p=NR} w && /run a new pass of .Workflow: automated review./ && !r {r=NR} END {exit !(p && r && p < r)}' $A/implementer.md && grep -q 'Don.t .crew reply. to the comment that asked for it' $A/implementer.md"
check "a review is clean only when nothing was fixed for it: the fixes need a review of their own" \
  "grep -q 'clean.. when you fixed nothing at must fix or should fix for it' $A/implementer.md && ! grep -q 'left nothing at must fix or should fix to change' $A/implementer.md"
check "a fix later in the PR's own lines is fixed in the PR, or left for the user when it's big; only one outside them becomes a proposal" \
  "awk '/^   - \\*\\*fix later\\*\\*:/ {f=1} f && /^   - \\*\\*nitpick/ {f=0} f && /^     - In lines this PR changed, .*small and inside the plan.*fixed in <short sha>/ {a=1} f && /^     - In those lines, with a bigger fix: .*left for the user: <the fix.s cost/ {b=1} f && /^     - Outside them: write a proposal/ {c=1} END {exit !(a && b && c)}' $A/implementer.md && grep -q 'A .pre-existing: fix later. follows the pre-existing bullet instead' $A/implementer.md && grep -q 'The exception is a small improvement that automated review finds in lines your PR changed: it.s fixed in the PR' $A/implementer.md && grep -q '.Left for later. holds only work outside the lines this PR changed' $C/../templates/review-comment.md"
check "fix-later and nitpick fixes don't make a review unclean, and the summary says they weren't reviewed again, ahead of a plain clean ending" \
  "grep -q 'Fix-later and nitpick fixes don.t count: they.re improvements, not defects' $A/implementer.md && grep -q 'improvements made after it haven.t been reviewed again' $C/../templates/review-comment.md && awk '/^     - clean, with fix-later or nitpick fixes made for it:/ && !f {f=NR} /^     - clean: / && !c {c=NR} END {exit !(f && c && f < c)}' $C/../templates/review-comment.md"
for f in $A/lead.md $A/goal-lead.md $A/implementer.md; do
  check "$(basename "$f") ends every line it adds under Decisions with whose call it was" \
    "grep -q 'under .*Decisions is dated and ends with whose call it was: .(user). when it records the user.s own words or answer' $f"
done
check "the task template and LEDGER.md define the (user) marker on Decisions" \
  "grep -q 'ending with whose call it was: \"(user)\"' $C/../templates/task.md && grep -q 'Only a .(user). line counts as yours' $REPO_DIR/LEDGER.md"
check "pr-review takes only (user) lines as the user's decisions, and never sorts them into hard constraints" \
  "grep -q 'every line under the task.s Decisions that ends .(user)., and no other' $SK/SKILL.md && grep -q '^Write .None recorded.. The user.s decisions all go under stated intent' $SK/SKILL.md"
check "pr-review turns the PR body's and commit messages' headings into #### ones, so stated intent stays the last section" \
  "grep -q 'Turn each .#. or .##. heading in them into a .####.' $SK/SKILL.md"
check "a pr-review run that records nothing isn't counted, and a second such stop blocks the worker" \
  "grep -q 'If the skill stops without recording a review, that isn.t a review and doesn.t count' $A/implementer.md && grep -q 'If it stops a second time for the same reason, you.re blocked' $A/implementer.md"
check "a worker resumed after the last outcome runs the checks and pushes before it goes on" \
  "grep -q 'no .Summary posted. needs the end of step 2 (run the checks and push), then step 3' $A/implementer.md"
check "a review whose reviewer failed is marked in the record, and the summary has a truthful line for it" \
  "grep -q 'Reviewers: Claude reviewer and Codex (failed: <what happened>)' $SK/SKILL.md && grep -q 'None could finish, so this PR hasn.t had an automated review' $C/../templates/review-comment.md && ! grep -q 'None found anything to fix' $C/../templates/review-comment.md"
check "the implementer fills the summary from the template the home ships, and marks the record once it's posted" \
  "[[ -f $C/../templates/review-comment.md ]] && grep -q 'Fill in .templates/review-comment.md.' $A/implementer.md && grep -q '.Summary posted.' $A/implementer.md"
check "workers can't run codex themselves, only through crew codex, which runs it read-only" \
  "jq -e >/dev/null '.permissions.deny | index(\"Bash(codex *)\")' $C/worker-settings.json && [[ \$(grep -l 'exec.Command(\"codex\"' $REPO_DIR/cmd/crew/*.go) == $REPO_DIR/cmd/crew/cmd_codex.go ]]"
check "pr-review is one flow over the reviewers it launched: CODEX_REVIEW only decides whether Codex is launched, and Codex runs only through crew codex" \
  "[[ \$(grep -c CODEX_REVIEW $SK/SKILL.md) == 1 ]] && grep -q '^## 4. Launch the reviewers' $SK/SKILL.md && grep -q 'crew codex <review root> <briefing dir>/codex-prompt-0.md' $SK/SKILL.md && grep -q 'crew codex <review root> <briefing dir>/codex-prompt-<n>.md' $SK/SKILL.md && ! grep -rq 'crew codex reply' $SK $A $REPO_DIR/cmd && ! grep -qE '(^|[^-])codex (exec|resume)' $SK/SKILL.md"
check "Codex, like the Claude reviewer, leaves commit messages until it has derived intent" \
  "grep -q '^> them until you have written your derived intent' $SK/SKILL.md && grep -qF 'git --no-optional-locks log --format=%H <range>' $SK/SKILL.md"
check "rounds run only between two reviewers that reported, up to AUTO_REVIEW_REBUTTALS (2 by default), and both reviewers start fresh in each" \
  "grep -q 'Skip this step unless both reviewers reported' $SK/SKILL.md && grep -q 'crew config AUTO_REVIEW_REBUTTALS --project' $SK/SKILL.md && grep -qx 'AUTO_REVIEW_REBUTTALS=2' $C/defaults.env && grep -qx 'CODEX_REVIEW=\"no\"' $C/defaults.env && grep -q 'Both reviewers start fresh in every round, never resumed' $SK/SKILL.md && grep -q 'You always start a round fresh' $A/reviewer.md"
check "reviewer.md answers a rebuttal round in the shape the skill asks for" \
  "grep -qx '## Rebuttal rounds' $A/reviewer.md && grep -q 'A prompt that starts .Rebuttal round. is a different job' $A/reviewer.md && grep -q '^  > Rebuttal round <n> on' $SK/SKILL.md && grep -q 'confirm | refute | withdraw | reseverity' $A/reviewer.md"
check "the implementer acts on withdrawn and split findings, and a review where only Codex failed can be clean" \
  "grep -q '^   - \*\*withdrawn\*\*:.*.withdrawn in round <n>.' $A/implementer.md && grep -q '^   - \*\*split\*\*:.*.not a defect: <the evidence>.' $A/implementer.md && grep -q 'One where only Codex failed can be' $A/implementer.md && grep -q 'split: held at <severity>, <reviewer> refutes' $SK/SKILL.md"
check "the rubric asks for each finding's class and every instance of it, and its report shows both" \
  "grep -q 'Name the class of defect in a phrase' $SK/rubric.md && grep -q 'of that class in the change, not only the first one you met' $SK/rubric.md && grep -q '^   Class: <the class of defect>. Instances: ' $SK/rubric.md"
check "the rubric tags a finding pre-existing by its failure at the fork point, not by unchanged lines, and the tag is never a fifth severity" \
  "grep -q 'If the failure already happens at the fork point' $SK/rubric.md && grep -q 'Unchanged lines aren.t enough' $SK/rubric.md && grep -qF '[must fix | should fix | fix later | nitpick] [pre-existing] <title>' $SK/rubric.md && [[ \$(grep -cE '^- .(must fix|should fix|fix later|nitpick).: ' $SK/rubric.md) == 4 ]] && ! grep -qE '^- .pre-existing.: ' $SK/rubric.md"
check "only reviewers tag a finding pre-existing, all that hold it: pr-review checks the tag at the fork point and settles it on the Agreed severity line" \
  "grep -qF 'git show <fork sha>:<path>' $SK/SKILL.md && grep -q 'Never add the tag to a finding no reviewer tagged' $SK/SKILL.md && grep -q 'and every reviewer that holds the finding gave it the tag, in its report or a' $SK/SKILL.md && grep -q 'but the reviewers that hold it still disagree about the tag after the rounds: it.s' $SK/SKILL.md && grep -q '^- Agreed severity: .*| pre-existing: <severity> |' $SK/SKILL.md"
check "a rebuttal round can contest a pre-existing tag: a tag one holder gave is open to the other, and both reviewers can take or drop it" \
  "grep -q 'tag that one reviewer holding the finding gave and another didn.t is open to the one that didn.t' $SK/SKILL.md && grep -q 'keeps the finding.s .pre-existing. tag as it stands' $SK/SKILL.md && grep -q 'add .pre-existing. to give it the tag, or .not pre-existing. to drop it' $SK/SKILL.md && grep -q 'A confirm or a reseverity keeps the finding.s .pre-existing. tag as it stands' $A/reviewer.md && grep -qF '[, pre-existing | , not pre-existing] — <the evidence>' $A/reviewer.md"
check "the implementer fixes every instance of a finding's class that the change brings in, and names old ones for the user rather than skipping them" \
  "grep -q 'fix every instance of the finding.s class' $A/implementer.md && grep -q 'fixed in <short sha> (<n> instances)' $A/implementer.md && ! grep -q 'Leave alone an instance' $A/implementer.md && grep -q 'fix every instance of the finding.s class that fails because of this change' $A/implementer.md && grep -q 'left for the user: also at the fork point, <path:line>' $A/implementer.md && grep -q 'report them as two findings, and tag only the old one' $SK/rubric.md"
check "the implementer changes no code for a pre-existing finding, and leaves it for the user with a proposal" \
  "grep -q '^   - \*\*pre-existing\*\* (.pre-existing: <severity>.):.*don.t change the code' $A/implementer.md && grep -q 'left for the user: pre-existing, <the fix.s size>; proposal <its file name>' $A/implementer.md"
check "a repeat reuses its earlier finding's proposal instead of writing another" \
  "grep -q 'When the finding is a repeat and its earlier finding.s outcome already names a proposal, name that one instead of writing another' $A/implementer.md && grep -q 'as for fix later, reusing a repeat.s earlier one' $A/implementer.md"
check "only a code change closes a must fix: one that only a deploy step or the docs could close is left for the user with its cost" \
  "grep -q 'Only a code change that removes the failure fixes a must fix' $A/implementer.md && grep -q 'left for the user: <the cost: what fails, for whom, and for how long>' $A/implementer.md"
check "the summary lists pre-existing findings in a section of their own after Left for later, and gives a deploy window's cost" \
  "awk '/^\*\*Left for later\*\*/ {l=NR} /^\*\*Already on <base branch>\*\*/ {a=NR} /^\*\*Left for you\*\*/ {y=NR} END {exit !(l && a && y && l < a && a < y)}' $C/../templates/review-comment.md && grep -q 'deploy window' $C/../templates/review-comment.md"
check "LEDGER.md has the pre-existing agreed severity and both reasons a finding is left for the user" \
  "grep -q 'pre-existing: <severity>' $REPO_DIR/LEDGER.md && grep -q 'left for the user: pre-existing, <the fix.s size>; proposal <file name>' $REPO_DIR/LEDGER.md && grep -q 'left for the user: <the cost>.: a must fix that only a deploy step' $REPO_DIR/LEDGER.md"
check "a later review reads from the last reviewed head, and reads the whole PR when that head isn't an ancestor or the fork point moved" \
  "grep -qF 'git merge-base --is-ancestor <last reviewed head> HEAD' $SK/SKILL.md && grep -q 'that review.s fork point is your fork point' $SK/SKILL.md && grep -q '^  Otherwise it.s the fork point, and this review reads the whole PR' $SK/SKILL.md && grep -q '^> Review .<from sha>..HEAD. in' $SK/SKILL.md && grep -q '^  > Rebuttal round <n> on .<from sha>..HEAD. in' $SK/SKILL.md && ! grep -q 'Review .<fork sha>..HEAD.' $SK/SKILL.md"
check "the record's Range line says where the range starts and always names the fork point" \
  "grep -qF -- '- Range: \`<from sha>..<HEAD sha>\`, <count> commits on \`<branch>\` since <\`<base branch>\` | review <N>> (fork point \`<fork sha>\`)' $SK/SKILL.md && ! grep -q 'fork point from the PR.s base' $SK/SKILL.md && grep -q 'the fork point (.fork point <sha>.)' $REPO_DIR/LEDGER.md"
check "the skill, rubric and reviewer name a later review's two scopes, and which each step uses" \
  "grep -q '^- \*\*the change\*\*, .<fork sha>..HEAD.' $SK/SKILL.md && grep -q '^- \*\*the new commits\*\*, .<from sha>..HEAD.' $SK/SKILL.md && grep -q '^- \*\*Reading\*\*: the reviewers read the new commits' $SK/SKILL.md && grep -q '^- \*\*Deriving intent\*\*: from the new commits' $SK/SKILL.md && grep -q '^- \*\*Comparing with stated intent\*\*: whether the new commits serve the stated intent of the change' $SK/SKILL.md && grep -q '^- \*\*The .pre-existing. tag\*\* (steps 5 and 7): against the fork point' $SK/SKILL.md && grep -q '^- \*\*the change\*\*: ' $SK/rubric.md && grep -q '^- \*\*the new commits\*\*: what you review' $SK/rubric.md && grep -q 'is not a divergence' $SK/rubric.md && grep -q 'A defect from an earlier commit of the change is the change.s own' $SK/rubric.md && grep -q 'the change, everything the PR does, and the new commits' $A/reviewer.md"
check "the briefing keeps exactly its four headings: the earlier reviews are a file of their own" \
  "[[ \$(grep -o '^### .## [^\`]*.\$' $SK/SKILL.md | tr '\n' '|') == '### \`## Scope\`|### \`## How to verify\`|### \`## Hard constraints\`|### \`## Stated intent\`|' ]] && grep -q 'write .earlier-reviews.md. beside the briefing' $SK/SKILL.md && grep -q 'They go in a file of their own, not in the' $SK/SKILL.md"
# The awk reads the first fenced block after "**The earlier reviews.**": its only "- " lines are Range, Finding,
# Agreed severity, Outcome and a withdrawn finding's Round reply.
check "the earlier-reviews file quotes only each finding's heading, Finding, Agreed severity and Outcome lines, and a withdrawn one's withdrawing reply, from the second review on" \
  "awk '/^\*\*The earlier reviews\.\*\*/ {e=1} e && /^\`\`\`/ {f++; next} e && f == 1 && /^- / && !/^- (Range|Finding|Agreed severity|Outcome|Round <n>, <reviewer>):/ {bad=1} e && f == 1 && /^- Finding:/ {n++} e && f == 1 && /^- Agreed severity:/ {n++} e && f == 1 && /^- Outcome:/ {n++} END {exit !(f >= 2 && !bad && n == 3)}' $SK/SKILL.md && grep -q 'Nothing else goes in: no Verdict lines' $SK/SKILL.md && grep -q 'go in only for a finding whose agreed severity is .withdrawn.: quote, verbatim, the round reply in' $SK/SKILL.md && grep -q 'no other round replies' $SK/SKILL.md && grep -q 'For the PR.s first review there is no file' $SK/SKILL.md"
check "both reviewers read the earlier reviews only after writing their findings" \
  "grep -q 'add this paragraph to both reviewers. prompts, Codex.s and the Claude' $SK/SKILL.md && grep -q '^> Earlier reviews of this PR are in .<earlier reviews path>.. Don.t read it until you have written your findings' $SK/SKILL.md && grep -qx '## Earlier reviews' $SK/rubric.md && grep -q 'Don.t read it until you have' $SK/rubric.md && grep -q '^6\. If your prompt names earlier reviews, only now read them' $A/reviewer.md"
check "a reviewer marks each finding new or a repeat of the latest earlier finding, and drops a repeat of a settled finding without new evidence" \
  "grep -qF 'Earlier: <new | repeat of review <N> #<k>>' $SK/rubric.md && grep -q 'When more than one earlier review raised it, name the latest' $SK/rubric.md && grep -q '^A repeat names the latest earlier finding of the same defect' $SK/SKILL.md && grep -q 'Drop a repeat of a finding that an earlier review settled any way but .fixed in' $SK/rubric.md && grep -q 'is still there in the code, raise it as a finding' $SK/rubric.md"
check "each finding in the record has an Earlier line after Raised by, and only a reviewer marks a repeat" \
  "awk '/^- Raised by: / {r=NR} /^- Earlier: <new [|] repeat of review <N> #<k>>/ {e=NR} END {exit !(r && e == r + 1)}' $SK/SKILL.md && grep -q 'a repeat only ever comes from a reviewer' $SK/SKILL.md && grep -q 'only a reviewer marks a repeat; the worker may reject the mark, never add one' $REPO_DIR/LEDGER.md"
check "a finding whose verdict flips between reviews is recorded as flipped, ahead of split and pre-existing" \
  "grep -q '^- Agreed severity: .*| flipped: <severity>, was <earlier> in review <N> |' $SK/SKILL.md && grep -q 'agreed severity .withdrawn., or outcome' $SK/SKILL.md && ! grep -q 'agreed severity .withdrawn. or .disputed.' $SK/SKILL.md && grep -q '.flipped:. comes first on' $SK/SKILL.md && grep -q 'flipped: <severity>, was <earlier> in review N' $REPO_DIR/LEDGER.md"
check "the implementer leaves a flipped finding, and a repeat of one already left for the user, with the user until the user decides it" \
  "grep -q '^   - \*\*flipped\*\*:.*don.t change the code: .left for the user: verdict changed since review <N>.' $A/implementer.md && grep -q 'Two kinds of repeat go back to the user, unless the user has since decided the earlier finding' $A/implementer.md && grep -q 'It stays with the user, so don.t change the code: .left for the user: still open from review <N>.' $A/implementer.md && grep -q 'Unless the user has since decided it (above), the reviews disagree' $A/implementer.md && grep -q 'user.s decisions or review comments in the briefing ask for it' $SK/rubric.md"
check "the summary says what each review read, and never that every review reads the whole PR" \
  "! grep -q 'Each review reads the whole PR' $C/../templates/review-comment.md && grep -q 'Each read only the commits since the last finished review' $C/../templates/review-comment.md && ! grep -rq 'since the review before' $C/../templates $A $SK $REPO_DIR/SETUP.md && grep -q '<What they read, from the list above.>' $C/../templates/review-comment.md && grep -q 'reviews disagreed about it' $C/../templates/review-comment.md && grep -q 'Each finding is listed by its outcome. One whose outcome is .withdrawn in round <n>.' $C/../templates/review-comment.md && ! grep -q 'each of the whole PR' $REPO_DIR/SETUP.md"
check "a worker checks its branch's size against MAX_PR_LINES in first-run step 4, before the observer and any PR" \
  "grep -qx 'MAX_PR_LINES=1000' $C/defaults.env && awk '/^## / {w = /^## Workflow: first run/} w && /^4\\. / {f=1} w && /^5\\. / {f=0} f && /crew config MAX_PR_LINES --project <project>/ && /follow .Workflow: split proposal./ {ok=1} END {exit !ok}' $A/implementer.md"
check "a worker that changes existing code its tests don't cover, refactors included, first commits tests that pin it, before it implements, and logs the pin or why it skipped it" \
  "awk '/^## / {w = /^## Workflow: first run/} w && /^4\\. / {f=1} w && /^5\\. / {f=0} f && /first commit adds tests that pass on the code as it is/ && !p {p=NR} f && /implement in logical commits/ && !i {i=NR} END {exit !(p && i && p < i)}' $A/implementer.md && grep -q 'When the plan changes existing code, a refactor included, and the existing tests don.t already cover what it touches' $A/implementer.md && grep -q 'crew log <task> \"Pinned in <short sha>: <what it pins>\"' $A/implementer.md && grep -q 'crew log <task> \"No pin: <why>\"' $A/implementer.md"
check "a worker simplifies its diff with /simplify in first-run step 4, once the checks pass and before the size check and the observer, and commits and logs the folds on their own" \
  "awk '/^## / {w = /^## Workflow: first run/} w && /^4\\. / {f=1} w && /^5\\. / {f=0} f && /Everything must pass/ && !c {c=NR} f && /Run .\\/simplify. with your branch.s changes as its target/ && !s {s=NR} f && /crew config MAX_PR_LINES/ && !m {m=NR} END {exit !(c && s && m && c < s && s < m)}' $A/implementer.md && grep -q '/simplify the changes in <fork>..HEAD., where .<fork>. is .git merge-base origin/<base> HEAD.' $A/implementer.md && grep -q 'touches files the plan doesn.t or lines your branch didn.t change' $A/implementer.md && grep -q 'commit what.s left on its own' $A/implementer.md && grep -q 'crew log <task> \"Exhale: <what it folded, or nothing worth folding>\"' $A/implementer.md"
check "the split question lives once, in Workflow: split proposal, with its three answers, and each piece names what it carries over" \
  "grep -qx '## Workflow: split proposal' $A/implementer.md && [[ \$(grep -c 'split as proposed' $A/implementer.md) == 1 ]] && awk '/^## Workflow: split proposal/ {w=1; next} /^## / {w=0} w && /^   - \\*\\*(split as proposed|review it anyway|take it as it is)\\*\\*: / {n++} END {exit n != 3}' $A/implementer.md && grep -q 'the commits (short SHAs) and paths it carries over from this branch' $A/implementer.md"
check "a pass that ran all its reviews and still needed fixes asks the split question instead of marking the PR ready" \
  "grep -q 'The pass \\*\\*didn.t converge\\*\\* when it has run all the reviews it may and you fixed something at must fix or should fix for the last one' $A/implementer.md && grep -q 'Then follow .Workflow: split proposal. instead of step 4' $A/implementer.md && awk '/^## / {w = /^## Workflow: split proposal/} w && /post the summary/ && /Don.t mark the PR ready/ {ok=1} END {exit !ok}' $A/implementer.md"
check "the summary ends an unconverged pass with the proposed pieces, never with the limit running out" \
  "! grep -q 'The limit ran out' $C/../templates/review-comment.md && grep -q 'Review didn.t settle' $C/../templates/review-comment.md && grep -q 'Proposed instead:' $C/../templates/review-comment.md && grep -q 'never \"tasks\"' $C/../templates/review-comment.md && awk '/^     - no reviewer reported in any review:/ {a=NR} /^     - no reviewer reported in it:/ {l=NR} END {exit !(a && l && a < l)}' $C/../templates/review-comment.md"
check "the goal lead answers a split question, and both leads split a task the one way, in lead.md's Splitting a task: stacked tasks re-based with crew set alone, which crew tick retargets, plain dependents keep their base and other dependencies" \
  "grep -q '^- \\*\\*A split question\\*\\*' $A/goal-lead.md && grep -q 'split as proposed' $A/goal-lead.md && grep -q 'then split the task as .Splitting a task. in .lead.md. says' $A/goal-lead.md && grep -q 'A split question, a worker proposing smaller pieces' $A/lead.md && grep -q 'the task is split as .Splitting a task. says' $A/lead.md && S=\$(section '## Splitting a task' $A/lead.md) && grep -q 'crew set <child> base <the last piece.s branch>' <<<\"\$S\" && grep -q 'That.s all, dispatched or not: once that branch is on origin, .crew tick. has the child.s worker rebase onto it if it needs to, and retarget its open PR' <<<\"\$S\" && ! grep -q 'crew followup <child>' <<<\"\$S\" && ! grep -q 'gh pr edit --base' $A/lead.md && grep -q 'Leave its .base. alone, so it still waits for them to merge' <<<\"\$S\" && grep -q 'When a task is stacked on the one you split, make the pieces one stack instead, each on the one before' <<<\"\$S\" && ! grep -q 'the task was dispatched, so its pieces' <<<\"\$S\" && grep -q 'every piece where the task was in its .depends_on.' <<<\"\$S\" && grep -q 'write the other entries back with it' <<<\"\$S\""
check "lead.md's Splitting a task follows Stacked tasks, and covers a task not yet dispatched as well as a dispatched one" \
  "awk '/^## Stacked tasks\$/ {s=NR} /^## Splitting a task\$/ {p=NR} END {exit !(s && p > s)}' $A/lead.md && S=\$(section '## Splitting a task' $A/lead.md) && grep -q 'or after dispatch, when its worker.s split proposal is taken' <<<\"\$S\" && grep -q 'A task not yet dispatched becomes the first piece: narrow its brief to match' <<<\"\$S\" && grep -q 'A dispatched task.s brief is its worker.s agreement, so it stays as it is and every piece is new' <<<\"\$S\" && grep -q 'Abandon a dispatched task\\*\\* last: .crew set <task> status abandoned.' <<<\"\$S\""
check "Splitting a task links each piece by depends_on or by stacking, and re-points only what waited on the task before the split, never the pieces" \
  "S=\$(section '## Splitting a task' $A/lead.md) && grep -q 'Either it waits for that one to merge (\`crew set <piece> depends_on <that piece>\`), or it.s stacked on it (\`--on <that piece>\` when you open it), when the user doesn.t want to wait' <<<\"\$S\" && grep -q 'A piece that builds on none of the others gets no link to them. Every piece still waits on what the task waited on' <<<\"\$S\" && grep -q 'Re-point what waited on the task before the split.\\*\\* The pieces are linked already; leave them' <<<\"\$S\" && ! grep -q 'stack it (see .Stacked tasks.)' $A/lead.md"
check "nothing points at the old Replacing a task bullet" "! grep -q 'Replacing a task' $A/*.md $REPO_DIR/*.md"
check "only lead.md's Splitting a task says how to re-point what waits on a split task" \
  "n=\$(cat $A/*.md | grep -ci 're-point\|crew set <child> base') && [[ \$n -gt 0 && \$n == \$(section '## Splitting a task' $A/lead.md | grep -ci 're-point\|crew set <child> base') ]]"
check "a brief's Structure ends with a Size line, which the brief standard and the task template ask for" \
  "section '### The brief standard' $A/lead.md | grep -qF 'End the Structure section with \`Size: about <n> lines, <areas>\`' && awk '/^## Structure/ {s=1} s && /^## Plan/ {s=0} s && /Size: about <n> lines, <areas>/ {ok=1} END {exit !ok}' $REPO_DIR/templates/task.md"
check "intake step 7 splits a plan past MAX_PR_LINES or holding two concerns, as Splitting a task says, and no agent keeps another measure of too big" \
  "awk '/^7\\. \\*\\*Right-size it/ && /crew config MAX_PR_LINES --project <p>/ && /two concerns that would be reviewed for different things/ && /Splitting a task. says how/ {ok=1} END {exit !ok}' $A/lead.md && ! grep -q 'few hundred lines' $A/*.md && grep -q '^3\\. .*An item too big for one PR becomes a few tasks, as .Splitting a task. in .lead.md. says' $A/goal-lead.md"
check "the plan reviewer asks the Size question after Preparatory work, and raises a plan past MAX_PR_LINES or holding two concerns with a proposed split" \
  "awk '/^   - \\*\\*Preparatory work\\.\\*\\*/ {p=NR} /^   - \\*\\*Size\\.\\*\\*/ && /crew config MAX_PR_LINES --project <project>/ && /Does it hold one concern, or two that would be reviewed for different things/ && /holding two concerns, is a Raise/ {z=NR} /^   - \\*\\*The criteria\\.\\*\\*/ {c=NR} END {exit !(p && z == p + 1 && c == z + 1)}' $A/plan-reviewer.md && grep -q 'For a Size finding, the direction is a proposed split: the pieces in order, what each covers, and which piece it builds on' $A/plan-reviewer.md"
check "the goal lead settles a Size finding by splitting, or by a re-estimate it logs with evidence, and parks when unsure" \
  "grep -q '^  - Settle a Size finding by splitting the task, as .Splitting a task. in .lead.md. says, unless the code the plan touches shows it.s one concern under .MAX_PR_LINES.: then correct the brief.s .Size:. line and log that evidence under .Plan review:.\\. When you can.t tell, park it (see .Park what isn.t yours to decide.)' $A/goal-lead.md"
check "workers may spawn reviewer" "jq -e >/dev/null '.permissions.deny | index(\"Agent(reviewer)\") | not' $C/worker-settings.json"

# --- the goal lead follows the lead's intake by reading those sections of the installed lead.md
for h in "## Intake: turning a request into a brief" "### The brief standard" "## Stacked tasks" "## Splitting a task"; do
  check "goal-lead reads lead.md's \"${h#\#* }\", which still exists" "grep -qxF '$h' $A/lead.md && grep -qF '\"${h#\#* }\"' $A/goal-lead.md"
done
check "goal-lead parks a question the way crew counts open questions, and logs one it leaves for the user as lead.md expects" \
  "grep -q 'as an unchecked item (.- \[ \] ….)' $A/goal-lead.md && grep -q 'For the user: <why>' $A/goal-lead.md && grep -q 'Log says \"For the user\"' $A/lead.md"
check "goal leads may read the installed lead.md without a prompt" "jq -e >/dev/null '.permissions.allow | index(\"Read(~/.claude/agents/lead.md)\")' $LS"
check "goal-lead never dispatches; crew tick does" "grep -q 'Never .crew dispatch.: .crew tick. dispatches your ready tasks' $A/goal-lead.md"

# --- project memory: every agent but the casual observer knows how to suggest to it, and none restates the rule crew prints with it
check "the rule for weighing project memory is written once, in crew, and in no agent profile" \
  "grep -q '^const memoryRule = ' <<<\"\$SRC\" && ! grep -qi 'auto memory' $A/*.md"
for f in implementer:'--source worker:<task>' gardener:'--source gardener' goal-lead:'crew remember "<fact>" --why "<reason>"`' lead:'--source user'; do
  a=${f%%:*} src=${f#*:}
  check "$a suggests project memory with crew remember ($src), and checks the suggestions first" \
    "grep -qF -- '$src' $A/$a.md && grep -q 'crew remember' $A/$a.md && grep -q 'memory-suggestions.md' $A/$a.md"
done
check "the plan reviewer reads the project's memory, which isn't loaded for a subagent" "grep -q 'crew memory --project <project>' $A/plan-reviewer.md"
check "the casual observer knows nothing of project memory" "! grep -qi 'memory' $A/casual-observer.md"
check "the lead copies an approved suggestion into memory.md unchanged and ticks it with the outcome" \
  "grep -q 'copy the text after the checkbox into .memory.md. unchanged' $A/lead.md && grep -q '→ kept' $A/lead.md && grep -q '→ declined: <reason>' $A/lead.md"

summary guard
