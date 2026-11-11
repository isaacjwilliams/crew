---
name: pr-review
description: One automated review of a crew worker's own open PR. Independent reviewers (the reviewer agent, and Codex when it's turned on) read the PR cold and answer each other's findings, you check each finding against the code, and the review is recorded in the task file. For crew workers only; takes the task ID.
argument-hint: "<task id>"
---

# PR review

One automated review of a crew task's open PR. You are the worker that wrote it. Independent reviewers read the PR
cold: the `reviewer` agent always, and Codex too when it's turned on. When both report, they answer each other's findings
in rounds, and you referee. You check every finding against the code, decide each one's severity, and record them all,
with your verdict, in the task file. The reviewers are input; the results are yours.

This skill reviews; it does not fix. Deciding what to do about each finding, and doing it, is the worker's job once
this skill has recorded the review.

The task: $ARGUMENTS

## The one rule that matters

Each reviewer must form its own view of the change. **Never send one your interpretation.** The briefing carries
primary sources and scope only. If a reviewer inherits your reasoning, it will confirm your mistakes instead of catching
them, and a compromised review reads exactly like a clean one. Both reviewers get the same briefing and the same rubric,
and nothing else.

| Send | Never send |
|---|---|
| The primary sources step 3 names, verbatim | Your summary of what the change does |
| The fork point, the range and the review root | The brief's Context, Structure or Plan: an assistant-written plan the user approved |
| Test, lint and run commands | Justification for a trade-off you chose |
| The user's decisions, the lines marked `(user)` | Which findings you expect, or a defence in advance |
| | Your reading of the user's intent |

Verbatim means verbatim. Quote; don't condense.

An approval is not intent. The user's "yes" to a brief adopts the plan as an action; it doesn't adopt its diagnosis,
its rationale or the trade-offs it accepted. Importing the brief would hand the reviewers your side's reasoning under
the name of the user's intent, and their comparison of derived and stated intent would then test the code
against its own justification.

## 1. Check the PR is ready to review

Read the task file (`crew path <task>`). The review is of the PR as it stands, so stop and say why if:

- the task has no `pr`;
- `git status --porcelain` shows any change, tracked or untracked;
- `HEAD` isn't what origin has for the branch: after `git fetch origin`, `git rev-parse HEAD` differs from
  `git rev-parse @{upstream}`.

## 2. Resolve the range

Read the PR's base and text: `gh pr view <pr> --json baseRefOid,baseRefName,headRefOid,title,body`.

- If `headRefOid` isn't `HEAD`, stop: the PR doesn't show what you have.
- **Use `baseRefOid`, not `baseRefName`.** The OID names the base commit unambiguously; a branch of that name can
  predate the fork. Ask git whether that commit is here (`git cat-file -e <oid>^{commit}`). If it isn't, stop and say
  to fetch it. Don't substitute a ref of the same name.
- **Resolve the fork point once:** `git merge-base <baseRefOid> HEAD`. From here on, every operation that reads
  history uses `<fork sha>..HEAD` and nothing else: the announcement, the briefing, the reviewers' prompts, every diff
  and log. Never `<base name>..HEAD`: a two-dot diff compares two endpoint trees, so once the base branch gains a
  commit after your fork, that commit shows up as a deletion the branch never made.
- Count the commits in the range. None means there's nothing to review: stop.

Announce the range before you spend a review, for example:
`Reviewing 7 commits on agent/saved-search since main (fork point a1b2c3d, from the PR's base)`.

## 3. Write the briefing

Make a new directory outside the worktree (`mktemp -d`) and write `briefing.md` in it. Its first line is
`Code review`. Then exactly these four `##` headings, in this order, and no others:

### `## Scope`

- The review root: the worktree's top level (`git rev-parse --show-toplevel`), as an absolute path.
- The range: `<fork sha>..HEAD`, with the fork point's full SHA, `HEAD`'s full SHA, the PR's base branch and the
  number of commits.
- That the working tree is clean, so only the commits are in scope.

### `## How to verify`

The test, lint and run commands from the repo's `AGENTS.md` (or `CLAUDE.md`), verbatim. If it names none, say so.

### `## Hard constraints`

Write `None recorded.` The user's decisions all go under stated intent, so a reviewer reads them only after it has
derived intent of its own, and you never sort them into constraints and intent.

### `## Stated intent`

Only these, verbatim, each as a `###` subsection:

- `### The user's request`: the quoted (`>`) block in the task's Goal section. Not the restatement that follows it.
- `### The user's decisions`: every line under the task's Decisions that ends `(user)`, and no other. A line without
  that ending isn't the user's, whatever it says. Omit the subsection if there are none.
- `### Pull request`: its title and body, from `gh pr view`.
- `### Commits`: every commit in `<fork sha>..HEAD`, with its full hash and its whole message, and a delimiter line
  between commits, for example `git log --format='----- %H%n%B' <fork sha>..HEAD`. Never an abbreviated format that
  drops bodies.

A PR body or commit message often has headings of its own. Turn each `#` or `##` heading in them into a `####`
heading, so nothing after `## Stated intent` starts a new section. That's the only change you make to them.

Nothing else from the task file goes in: not its title, Context, Structure, Plan, Log or review rounds. If you catch
yourself explaining, delete the sentence.

Stated intent comes last, and the headings are spelled exactly so, because a reviewer has to read the code before it
reads the stated intent. It reads up to `## Stated intent` and stops there until it has written its own account of the
change. A fifth `##` heading after it, or stated intent at another level, takes that boundary away, and nothing
downstream can tell.

## 4. Launch the reviewers

The Claude reviewer always runs. Codex runs alongside it when `crew config CODEX_REVIEW --project <project>` (the task's
`project`) prints `yes`; that's the only thing the setting decides. Launch Codex first, so the two run at the same time.
They're isolated: pass nothing from one to the other while both run.

**Codex.** Write this prompt to `codex-prompt-0.md` in the briefing's directory, with both paths written out in full:

> Adversarially review the work described in `<briefing path>`. Follow the rubric at `${CLAUDE_SKILL_DIR}/rubric.md`
> exactly, including its `Derived intent vs. stated intent` section — derive intent from the artifact named for this
> review type before you read the stated intent in the briefing. Commit messages are stated intent too, so don't read
> them until you have written your derived intent: list the range's commits with
> `git --no-optional-locks log --format=%H <range>`, which shows no messages. Use `git --no-optional-locks` for git
> status and diff; the sandbox is read-only. You are reviewing work produced by another AI agent. Be direct and
> specific. Do not soften findings, and do not manufacture findings to appear thorough.

Then start it as a background Bash command, so you're told when it finishes:

```
crew codex <review root> <briefing dir>/codex-prompt-0.md <briefing dir>/codex-answer-0
```

`crew codex` is the only way to run Codex: it keeps every run read-only, rounds included. Its report lands in
`<briefing dir>/codex-answer-0.md`.

**The Claude reviewer.** Spawn the `reviewer` subagent with this prompt, and nothing else. Write the fork point's full
SHA out in it.

> Review `<fork sha>..HEAD` in `<review root>`. Use the scope exactly as given; don't widen it or reconstruct it.
> Follow the rubric at `${CLAUDE_SKILL_DIR}/rubric.md` and the briefing at `<briefing path>`.

Wait for it to return, then for Codex if you launched it. Don't go on while either is still working: reconciling early
turns a two-reviewer review into a one-reviewer review, and nothing downstream can tell that apart from a reviewer
that found nothing.

A reviewer that fails, stalls or returns no report hasn't reported. For Codex, exit status 124 means it wrote nothing
for 15 minutes and was stopped; any other non-zero status, or an empty report, is a failure. Note what happened for the
record (step 8) and carry on with the reviewer that did report. Never invent what a review would have said. If neither
reported, go straight to step 8 and record the review with no findings.

## 5. Check every finding against the code

You wrote this code, so assume you are tilted toward dismissing criticism of it, and correct for that. The failure
mode is a finding you talk yourself out of because you remember why you did it that way. Remembering why is not
counter-evidence.

When both reviewers reported, first match their findings: two findings about the same defect are one finding,
raised by `both`, however differently they're worded or rated. Every other finding was raised by `Codex` or by
`Claude reviewer`.

Check every finding against the code: read the cited paths, trace the callers, run the test if one settles it. Note
your **counter-evidence** against any finding you think is wrong: a cited `file:line`, a passing test, or a fact about
the code. If you can't cite something, it isn't counter-evidence. Don't decide anything yet: with two reviewers, the
rounds come first.

## 6. Rounds between the reviewers

Skip this step unless both reviewers reported. The rounds are where the reviewers test each other's findings. You
referee: you decide what's open, and you add your counter-evidence. `crew config AUTO_REVIEW_REBUTTALS --project
<project>` is the most rounds this review may run. It's a ceiling, not a quota.

**What's open.** An item is open while a reviewer hasn't answered the latest word on it:

- a finding raised by one reviewer is open to the other, until it has weighed it;
- a refutation is open to the reviewer that raised the finding, so it can answer it or withdraw;
- an answer to a refutation is open to the reviewer that refuted, to weigh in turn;
- your counter-evidence is open to both.

A confirmation, a withdrawal or a change of severity closes an item. A difference in severity between the reviewers
isn't a disagreement, and isn't sent: step 7 starts from the higher one. A finding first raised in a round reply is a
finding like any other, open to the other reviewer.

Before the first round, the open items are the findings only one reviewer raised and every finding you have
counter-evidence against. If nothing is open, skip the rounds.

**A round.** Both reviewers start fresh in every round, never resumed, so each gets everything it needs in a round
file of its own, in the briefing's directory: its own report, then each earlier round's items and its reply to them,
all verbatim, then this round's numbered items. Start Codex, then spawn the Claude reviewer, so both work at once, and
wait for both answers before you close the round.

- **Codex's** items are the Claude reviewer's open findings that Codex didn't raise, the Claude reviewer's refutations
  of Codex's findings and its answers to Codex's refutations, and your counter-evidence on any open item. Write them to
  `codex-round-<n>.md`, and this prompt to `codex-prompt-<n>.md`, then run, in the background,
  `crew codex <review root> <briefing dir>/codex-prompt-<n>.md <briefing dir>/codex-answer-<n>`:

  > Rebuttal round <n> on the change described in `<briefing path>`, under the rubric at
  > `${CLAUDE_SKILL_DIR}/rubric.md`. The round file `<briefing dir>/codex-round-<n>.md` holds your own earlier report,
  > each earlier round's items with your reply to them, and this round's numbered items: findings from a second
  > reviewer who reviewed the same change independently under the same rubric and briefing, its objections to your
  > findings, and evidence from the change's author. You've derived intent already, in your report, so read the
  > briefing whole. Check every item against the code yourself. Answer each one with exactly one of: confirm, refute,
  > withdraw (only a finding you raised) or reseverity with the new severity, and your evidence: a `file:line`, a
  > test, or a fact about the code. Reply under `### Round <n>`, one numbered line per item, then list any new finding
  > in the rubric's format under `### New findings`, or write "None.". Use `git --no-optional-locks`; the sandbox is
  > read-only.

- **The Claude reviewer's** items are the mirror image: Codex's open findings it didn't raise, Codex's refutations and
  answers, and your counter-evidence. Write them to `reviewer-round-<n>.md`, and spawn a fresh `reviewer` subagent with
  this prompt:

  > Rebuttal round <n> on `<fork sha>..HEAD` in `<review root>`. The rubric is `${CLAUDE_SKILL_DIR}/rubric.md`, the
  > briefing `<briefing path>`, and the round `<briefing dir>/reviewer-round-<n>.md`.

Label each item with where it came from (`Codex's finding`, `the Claude reviewer's refutation`, `the author's
evidence`) and quote the reviewers verbatim. Your counter-evidence is stated flat, as a fact with its citation:
"`cmd/x.go:40` returns before the write when the file is missing". Never your reasons, what you meant, or why you
built it that way: that's the interpretation the one rule keeps out.

A reviewer that fails in a round leaves the rounds; note it for the record, keep what it said earlier, and stop: the
rounds need both.

**After each round,** check anything new against the code (step 5), then recompute what's open. A second round carries
only what's still open: re-sending a settled item to use up the budget invites a reviewer to relitigate what it
already withdrew. Stop when nothing is open or the budget is spent, whichever comes first. A disagreement still open
then is a legitimate result, not a failure to fix.

## 7. Settle each finding

Now give each finding your verdict, on where the reviewers ended:

- **Agree**: the finding stands.
- **Disagree**: you have counter-evidence, as step 5 defines it.
- **Uncertain**: plausible, but you can't settle it. It goes to the user, never quietly in your favour.

Then its **agreed severity**, from where each reviewer stands after the last round. A reviewer **holds** a finding it
raised and didn't withdraw, or one it confirmed or gave a severity of its own. It **refutes** one it refuted, or one it
raised and then withdrew.

- **Withdrawn** when no reviewer holds it: its raisers withdrew it and nobody confirmed it.
- **Split** when one reviewer holds it and the other refutes it. You settle a split when you act on the review, not
  here: record the severity it's held at and who refutes it.
- Otherwise, start from the **highest severity any reviewer holds**. When you agree, that's the agreed severity.
  You may lower it only with cited evidence that the consequence is smaller than the reviewers say; if you can't cite
  any, keep it.
- When you disagree and no reviewer refutes the finding, it's `disputed`.
- When you're uncertain, it's `uncertain`.

## 8. Record the review

Append the review to the task file under `## Automated reviews`. If the file has no such section (a task created
before it existed), add the heading just before `## Review rounds`. Number it one past the last `### Automated review`
heading there.

**Every finding any reviewer raised goes in the record**, including the ones you disagree with, the ones withdrawn and
the ones first raised in a round. You don't decide what is real; your verdict is annotation, not a filter.

```markdown
### Automated review <N> — <YYYY-MM-DD HH:MM>

- Range: `<fork sha>..<HEAD sha>`, <count> commits on `<branch>` since `<base branch>` (fork point from the PR's base)
- Reviewers: <Claude reviewer, or Claude reviewer and Codex>
- Rounds: <how many ran> of <AUTO_REVIEW_REBUTTALS>

**Derived intent — Claude reviewer:** <verbatim>
**Derived intent — Codex:** <verbatim>
**Stated intent — Claude reviewer:** <verbatim, with its comparison to the derived intent>
**Stated intent — Codex:** <verbatim, with its comparison to the derived intent>

#### 1. [<highest severity a reviewer gave it>] <title> — `<path:line>`
- Raised by: <both | Codex | Claude reviewer>
- Finding:
  > <verbatim, from whoever raised it; when both did, each one's, labelled>
- Round <n>, <reviewer>:
  > <its reply on this finding, verbatim>
- Verdict: <agree | disagree | uncertain> — <your evidence, with file:line>
- Agreed severity: <must fix | should fix | fix later | nitpick | withdrawn | split: held at <severity>, <reviewer> refutes | disputed | uncertain>
- Outcome:

**Conclusion — Claude reviewer:** <verbatim>
**Conclusion — Codex:** <verbatim>

**Verification limits:** <what the reviewers couldn't check, and a reviewer that failed or stalled; or "none">
```

Leave out the Codex lines and `Rounds:` when Codex didn't run, and a `Round` line for each round that didn't address
the finding. Report both derived intents verbatim and unmerged: two reviewers who read the same change and came away
with different accounts of its purpose is evidence that the change doesn't communicate it. When they diverge, say how
in a line under them.

A reviewer that didn't report is named on the Reviewers line with what happened:
`- Reviewers: Claude reviewer and Codex (failed: <what happened>)`. One that failed in a round says which:
`Codex (failed in round 2: <what happened>)`; what it said before that stays in the record.

Leave every `Outcome:` empty: you fill it in once you have acted on the finding, after this skill. A review with no
findings says `No findings.` in place of the numbered ones. Once the PR's summary comment is posted, the newest review
gets a last line, `Summary posted`; that isn't this skill's either.

Finish with one line: the review's number, and how many findings it recorded at each agreed severity.
