---
id: {{ID}}
title: "{{TITLE}}"
project: {{PROJECT}}
status: draft
base: {{BASE}}
base_sha:
restack_sent:
branch: {{BRANCH}}
depends_on:
goal:
pre_pr_review: {{PRE_PR_REVIEW}}
pr_mode: {{PR_MODE}}
worktree:
pr:
pr_state:
pr_updated:
worker_name:
worker_job:
worker_session:
review_rounds: 0
last_round_sent: 0
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
     paths says so in one line. -->

## Plan
<!-- The agreed approach, as numbered steps. Name the files and functions to touch. -->

## Constraints and conventions
<!-- Anything beyond AGENTS.md that applies: patterns to follow, things not to touch. -->

## Acceptance criteria
<!-- Checkable statements, including how to verify (tests to add or run, manual checks). When the goal
     is about how code is organised, at least one checks the code's shape, not only its behaviour. -->

## Out of scope
<!-- Tempting adjacent work the worker must NOT do. Follow-up ideas go in the Log. -->

## Open questions
<!-- Questions waiting on the user, one unchecked item each ("- [ ] …"): a worker's when it is
     blocked, and collaborators' PR comments from `crew feedback`. Whoever records the answer under
     Decisions ticks the item. -->

## Decisions
<!-- Dated decisions made after dispatch. -->

## Review rounds
<!-- Appended by `crew feedback`. -->

## Log
