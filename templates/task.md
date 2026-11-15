---
id: {{ID}}
title: "{{TITLE}}"
project: {{PROJECT}}
status: draft
base: {{BASE}}
base_sha:
restack_sent:
retarget_sent:
branch: {{BRANCH}}
depends_on:
goal:
risk:
pre_pr_review: {{PRE_PR_REVIEW}}
pr_mode: {{PR_MODE}}
worktree:
pr:
pr_state:
pr_base:
pr_updated:
ci_state:
worker_name:
worker_job:
worker_session:
worker_fingerprint:
review_rounds: 0
last_round_sent: 0
interrupted:
feedback_since:
questions_routed:
created: {{NOW}}
updated: {{NOW}}
---
# {{TITLE}}

## Goal
<!-- The user's own description of the problem, quoted. Then one or two sentences: the outcome and
     why it matters. -->

## Context
<!-- What the worker needs that isn't obvious from the code: background, prior decisions, links. -->

## Structure
<!-- Where the new code lives (modules, classes, routes, components), whether it extends what exists
     or starts something new, the main alternative, and why it lost. If a preparatory refactor would
     make this simpler, name it here even if you decided against it. A change that adds no new code
     paths says so in one line. End with "Size: about <n> lines, <areas>" (added plus removed). -->

## Plan
<!-- The agreed approach, as numbered steps. Name the files and functions to touch. -->

## Constraints and conventions
<!-- Anything beyond AGENTS.md that applies: patterns to follow, things not to touch. -->

## Acceptance criteria
<!-- Checkable statements, including how to verify (tests to add or run, manual checks). When the goal
     is about how code is organised, at least one checks the code's shape, not only its behaviour.
     One "- " item each, any detail indented under it: the worker records evidence for each one,
     by its number. -->

## Out of scope
<!-- Tempting adjacent work the worker must NOT do. Follow-up ideas go in the Log. -->

## Open questions
<!-- Questions waiting on the user, one unchecked item each ("- [ ] …"): a worker's when it is
     blocked, and collaborators' PR comments from `crew feedback`. Whoever records the answer under
     Decisions ticks the item. -->

## Decisions
<!-- Dated decisions made after dispatch, each ending with whose call it was: "(user)" for the
     user's own words or answer, otherwise "(lead)" or "(worker)". -->

## Evidence
<!-- Written by the worker before the hand-over: one "### <n>. <the criterion, in a line>" entry
     for each top-level item of Acceptance criteria, numbered in order. Each holds the command
     that shows the criterion holds and its output, in a fenced block trimmed to what shows the
     result; or, where only a look would show it, what you saw and how (the page, the steps, the
     tool); or, for a check only the user can make, what to try and why you couldn't. Indent any
     output line that starts with "## ", or it ends this section. A high-risk task also has a
     "### Security review" entry: what /security-review found and what you did about it.
     `crew hand-over` refuses until every criterion has an entry, and a high-risk task this one. -->

## Automated reviews
<!-- Appended by the worker's pr-review skill, one "### Automated review N" per review; the worker
     adds each finding's outcome. -->

## Review rounds
<!-- Appended by `crew feedback`. -->

## Log
