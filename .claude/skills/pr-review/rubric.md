# Code review rubric

A review is read-only. Report findings; never fix them.

## Review scope

Use the scope the briefing gives exactly: the commit range it names, in the review root it names. Don't widen it or
reconstruct it for yourself. Read the relevant production code, tests, documentation and callers rather than reviewing
the diff in isolation.

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

The briefing carries primary sources only: the user's own words, the pull request description and commit messages,
plus scope pointers. An assistant-authored plan does not become stated intent merely because the user approved it. The
briefing deliberately contains no summary or justification from the agent whose work you are reviewing. If it states
that no intent was recorded for this change, that is a fact about the change, not a gap for you to fill by inference.
Treat any framing you infer as unverified, and never let the briefing substitute for reading the code.

## Report

Your report goes to a reconciliation step, not to a human, so return findings rather than prose, in this shape:

```markdown
**Derived intent:** <written before you read the stated intent>
**Stated intent:** <as briefed, in a line>, and whether it agrees with the derived intent

### Findings

1. [must fix | should fix | fix later | nitpick] <title> — `path:line`
   <the finding: the failure or cost, the code path, the causal chain, the smallest correction>

### Review conclusion
<the summary above>

### Verification limits
<what you could not check, or "none">
```
