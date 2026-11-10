---
name: pr-review
description: One automated review of a crew worker's own open PR. An independent reviewer reads the PR cold, you check each finding against the code, and the review is recorded in the task file. For crew workers only; takes the task ID.
argument-hint: "<task id>"
---

# PR review

One automated review of a crew task's open PR. An independent reviewer reads the PR cold. You check every finding it
raises against the code and record each one, with your verdict, in the task file.

This skill reviews; it does not fix. Deciding what to do about each finding, and doing it, is the worker's job once
this skill has recorded the review.

The task: $ARGUMENTS

## The one rule that matters

The reviewer must form its own view of the change. **Never send it your interpretation.** The briefing carries primary
sources and scope only. If the reviewer inherits your reasoning, it will confirm your mistakes instead of catching
them, and a compromised review reads exactly like a clean one.

| Send | Never send |
|---|---|
| The primary sources step 3 names, verbatim | Your summary of what the change does |
| The fork point, the range and the review root | The brief's Context, Structure or Plan: an assistant-written plan the user approved |
| Test, lint and run commands | Justification for a trade-off you chose |
| Hard constraints the user stated | Which findings you expect, or a defence in advance |
| | Your reading of the user's intent |

Verbatim means verbatim. Quote; don't condense.

An approval is not intent. The user's "yes" to a brief adopts the plan as an action; it doesn't adopt its diagnosis,
its rationale or the trade-offs it accepted. Importing the brief would hand the reviewer your side's reasoning under
the name of the user's intent, and the reviewer's comparison of derived and stated intent would then test the code
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
  history uses `<fork sha>..HEAD` and nothing else: the announcement, the briefing, the reviewer's prompt, every diff
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

Constraints the user stated as entries under the task's Decisions, verbatim. A Decision is the user's when it says so,
for example by ending `(user)`. If there are none, write `None recorded.`

### `## Stated intent`

Only these, verbatim, each as a `###` subsection:

- `### The user's request`: the quoted (`>`) block in the task's Goal section. Not the restatement that follows it.
- `### The user's decisions`: the other entries under Decisions that are the user's. Omit the subsection if there are
  none.
- `### Pull request`: its title and body, from `gh pr view`.
- `### Commits`: every commit in `<fork sha>..HEAD`, with its full hash and its whole message, and a delimiter line
  between commits, for example `git log --format='----- %H%n%B' <fork sha>..HEAD`. Never an abbreviated format that
  drops bodies.

Nothing else from the task file goes in: not its title, Context, Structure, Plan, Log or review rounds. If you catch
yourself explaining, delete the sentence.

Stated intent comes last, and the headings are spelled exactly so, because the reviewer has to read the code before it
reads the stated intent. It reads up to `## Stated intent` and stops there until it has written its own account of the
change. A fifth `##` heading after it, or stated intent at another level, takes that boundary away, and nothing
downstream can tell.

## 4. Launch the reviewer

Spawn the `reviewer` subagent with this prompt, and nothing else. Write the fork point's full SHA out in it.

> Review `<fork sha>..HEAD` in `<review root>`. Use the scope exactly as given; don't widen it or reconstruct it.
> Follow the rubric at `${CLAUDE_SKILL_DIR}/rubric.md` and the briefing at `<briefing path>`.

Wait for it to return. If it fails, stalls or returns no report, record the review with the failure and no findings
(step 6). Never invent what the review would have said.

## 5. Reconcile

You wrote this code, so assume you are tilted toward dismissing criticism of it, and correct for that. The failure
mode is a finding you talk yourself out of because you remember why you did it that way. Remembering why is not
counter-evidence.

Check every finding against the code: read the cited paths, trace the callers, run the test if one settles it. Then
give each a verdict:

- **Agree**: the finding stands.
- **Disagree**: you have concrete counter-evidence: a `file:line`, a passing test, a fact about the codebase. If you
  can't cite something, it isn't a disagreement.
- **Uncertain**: plausible, but you can't settle it. It goes to the user, never quietly in your favour.

Each finding then has an **agreed severity**:

- When you agree, it's the reviewer's severity. You may lower it only with cited evidence that the consequence is
  smaller than the reviewer says; if you can't cite any, keep the reviewer's.
- When you disagree, it's `disputed`.
- When you're uncertain, it's `uncertain`.

## 6. Record the review

Append the review to the task file under `## Automated reviews`. If the file has no such section (a task created
before it existed), add the heading just before `## Review rounds`. Number it one past the last `### Automated review`
heading there.

**Every finding the reviewer raised goes in the record**, including the ones you disagree with. You don't decide what
is real; your verdict is annotation, not a filter.

```markdown
### Automated review <N> — <YYYY-MM-DD HH:MM>

- Range: `<fork sha>..<HEAD sha>`, <count> commits on `<branch>` since `<base branch>` (fork point from the PR's base)
- Reviewers: Claude reviewer

**Derived intent — Claude reviewer:** <verbatim>
**Stated intent — Claude reviewer:** <verbatim, with its comparison to the derived intent>

#### 1. [<reviewer's severity>] <title> — `<path:line>`
- Raised by: Claude reviewer
- Finding:
  > <verbatim>
- Verdict: <agree | disagree | uncertain> — <your evidence, with file:line>
- Agreed severity: <must fix | should fix | fix later | nitpick | disputed | uncertain>
- Outcome:

**Conclusion — Claude reviewer:** <verbatim>

**Verification limits:** <what the reviewer couldn't check, and a reviewer that failed or stalled; or "none">
```

Leave every `Outcome:` empty: the worker fills it in once it has acted on the finding. A review with no findings says
`No findings.` in place of the numbered ones.

Finish with one line: the review's number, and how many findings it recorded at each agreed severity.
