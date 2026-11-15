# Code review rubric

A review is read-only. Report findings; never fix them.

## Review scope

Use the scope the briefing gives exactly: the commit range it names for you to review, in the review root it names.
Don't widen it or reconstruct it for yourself. Read the relevant production code, tests, documentation and callers
rather than reviewing the diff in isolation.

The briefing's scope names two ranges:

- **the change**: everything the pull request does, from the fork point to its head;
- **the new commits**: what you review. In a pull request's first review they're the change. In a later one they're
  usually only the commits since the last finished review, so you read those, and the code around them.

"The range" below means the new commits.

Review for:

- Data flow and persistence correctness.
- Logic errors, regressions, race conditions, and stale-state behavior.
- Authorization and ownership boundaries.
- Object-oriented design, and the project's own conventions in its `AGENTS.md` (or `CLAUDE.md`).
- Public and internal interfaces that are broader than current callers require.
- Speculative compatibility, defensive code, and legacy runtime paths.
- Test gaps, incorrect test layers, production-path mismatches, and vacuous assertions.
- Performance and query-shape regressions.
- Dead code and incomplete migrations from previous approaches.
- Naming, responsibility, locality, and cognitive load.
- Plan, documentation, and stated-requirement conformance.

## Findings

Prioritize actionable findings using:

- `must fix`: correctness, data loss, security, authorization, production failure, or material regression.
- `should fix`: substantial maintainability, testing, performance, or design issue that belongs in the current work.
- `fix later`: valid improvement outside the current completion contract.
- `nitpick`: low-impact clarity or consistency issue.

For each finding:

- Name the concrete failure or maintenance cost.
- Cite the relevant code path and affected caller or user flow.
- Explain the causal chain rather than only describing suspicious code.
- Distinguish a proven defect from a question or preference.
- Recommend the smallest coherent correction without implementing it.
- Name the class of defect in a phrase (for example "job arguments read with the wrong id type"). List every instance
  of that class in the change, not only the first one you met: an instance in a commit you didn't review needs the
  same fix.
- If the failure already happens at the fork point (the start of the change), for the same caller and input, tag the
  finding `pre-existing` and cite the code at the fork point that fails the same way. Unchanged lines aren't enough: an
  old defect that the change newly makes reachable (a new caller of a buggy function, say) is the change's own, so
  don't tag it. A defect from an earlier commit of the change is the change's own too, even when you didn't review
  that commit. The tag sits beside the severity; it doesn't change it. When some instances of a class already fail at
  the fork point and others don't, report them as two findings, and tag only the old one.

Do not inflate the review with findings that lack a demonstrated caller, behavioral consequence, or maintenance cost.

## Review conclusion

Summarize:

- The intended change.
- Whether the implementation actualizes that intent.
- Preserved and changed behavior.
- Verification already present and important gaps.
- Whether the work is ready to merge after the listed findings.

When no findings remain, say so directly and identify any residual verification limits.

## Derived intent vs. stated intent

Derive intent first, from the code and tests alone, and commit it to writing as `Derived intent` before you read any
stated intent. The briefing's `## Stated intent` is off-limits until you have written your own. Commit messages and the
pull request description are *stated* intent, not derivation material: they are the author's claim about the change,
exactly like the briefing's. Read what the code does before you read what it was said to do.

Only then read the stated intent in the briefing. Record it as `Stated intent` and compare:

- If they agree, say so in one line.
- If they diverge, that divergence is itself a finding, severity assigned on its consequences. Divergence means either
  the change fails to communicate its own purpose, or the wrong thing was built. Say which and why.

When the new commits aren't the whole change, derive the intent of the new commits. Then compare: do they serve the
stated intent of the change (the user's request, decisions and comments, and the pull request), and do they do what
their own commit messages say? That they do less than the whole pull request set out to is not a divergence. Working
against its stated intent, or going beyond it, is.

The briefing carries primary sources only: the user's own words, the pull request description and commit messages,
plus scope pointers. An assistant-authored plan does not become stated intent merely because the user approved it. The
briefing deliberately contains no summary or justification from the agent whose work you are reviewing. If it states
that no intent was recorded for this change, that is a fact about the change, not a gap for you to fill by inference.
Treat any framing you infer as unverified, and never let the briefing substitute for reading the code.

## Earlier reviews

From a pull request's second review on, `earlier-reviews.md`, beside the briefing, holds its earlier reviews: each
one's range and findings, with how each finding was settled, quoted from the record. Don't read it until you have
written your findings. Then, for each finding:

- Mark it `Earlier: repeat of review <N> #<k>` when it's the same defect as finding k of review N, and `Earlier: new`
  otherwise. When more than one earlier review raised it, name the latest: its settlement is the one that stands.
  With no earlier-reviews file, every finding is `new`.
- Drop a repeat of a finding that an earlier review settled any way but `fixed in …` (withdrawn, disputed, not a
  defect, a proposal, recorded, left for the user), unless you have evidence that the settlement missed, or the
  user's decisions or review comments in the briefing ask for it. Then keep it, and state that evidence in the
  finding.
- If a finding settled as `fixed in <sha>` is still there in the code, raise it as a finding, marked as a repeat of
  it.

The same goes for findings you first raise in a rebuttal round.

## Report

Your report goes to a reconciliation step, not to a human, so return findings rather than prose, in this shape:

```markdown
**Derived intent:** <written before you read the stated intent>
**Stated intent:** <as briefed, in a line>, and whether it agrees with the derived intent

### Findings

1. [must fix | should fix | fix later | nitpick] [pre-existing] <title> — `path:line`
   Class: <the class of defect>. Instances: `path:line`, `path:line`, …
   Earlier: <new | repeat of review <N> #<k>>
   <the finding: the failure or cost, the code path, the causal chain, the smallest correction>

### Review conclusion
<the summary above>

### Verification limits
<what you could not check, or "none">
```

Leave out `[pre-existing]` unless you tagged the finding.
