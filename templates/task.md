---
id: {{ID}}
title: "{{TITLE}}"
project: {{PROJECT}}
status: draft
kind: {{KIND}}
informs:
fixes:
missed_by:
base: {{BASE}}
base_sha:
earlier_base_shas:
restack_sent:
retarget_sent:
branch: {{BRANCH}}
depends_on:
go_ahead:
goal:
risk:
pre_pr_review: {{PRE_PR_REVIEW}}
pr_mode: {{PR_MODE}}
model:
effort:
auto_review:
worktree:
pr:
pr_state:
pr_base:
pr_updated:
ci_state:
mergeable:
merge_state:
worker_name:
worker_job:
worker_session:
worker_fingerprint:
worker_model:
worker_effort:
review_rounds: 0
last_round_sent: 0
interrupted:
feedback_since:
questions_routed:
report_sent:
created: {{NOW}}
updated: {{NOW}}
---
# {{TITLE}}

## Goal
<!-- The user's own description of the problem, quoted. Then the objective: one outcome a single
     task can reach, in a sentence or two, and why it matters. Nothing about how to reach it: the
     design is the worker's, within the project's rules. -->

## Context
<!-- Facts the worker needs that aren't obvious from the code: where the relevant code lives now,
     traps, the user's decisions, links. Facts, not instructions. -->

## Still to learn
<!-- What the objective or its gates rest on that nobody has checked yet. crew keeps these items:
     add one with crew to-learn <this task> "<item>", and settle it with crew learned <this task>
     <item> "<answer>". crew show lists them here, each with its answer; a line typed here isn't
     one. A spike can answer one: crew new --kind spike --informs <this task>. crew ready refuses
     while any is open. -->

## Plan
<!-- A spike's only: what to try, and a time box. Any other brief leaves it empty, and crew ready
     refuses one that doesn't. -->

## Acceptance criteria
<!-- The gates: checkable statements that show the objective is met, each with how to check it.
     A gate measures the outcome, never a way to reach it. When the objective is about how code
     is organised, the shape is the outcome, so a gate checks it. One "- " item each, any detail
     indented under it: the worker records evidence for each one, by its number. The worker runs
     every check but a user check: a gate the user makes themselves, a live smoke test say. It
     starts "User check:", or a Decision of the user's makes it one later: "- <date> — Criterion
     <n> is a user check: <why> (user)". -->

## Size
<!-- "About <n> lines: <areas>": your estimate of the lines the PR will add plus remove, tests
     and docs included, and the areas it spans. A spike needs none. -->

## Out of scope
<!-- Tempting adjacent work the worker must NOT do, and anything the user rules out (a migration,
     a new dependency). Follow-up ideas go in the Log. -->

## Open questions
<!-- Questions waiting on the user, one unchecked item each ("- [ ] …"): a worker's when it is
     blocked, and collaborators' PR comments from `crew feedback`. Whoever records the answer under
     Decisions ticks the item. -->

## Decisions
<!-- Dated decisions made after dispatch, each ending with whose call it was: "(user)" for the
     user's own words or answer, otherwise "(lead)" or "(worker)". -->

## Evidence
<!-- What shows the gates hold, recorded by the worker with crew, never typed here: for each
     top-level item of Acceptance criteria, by its number,
     `crew record-evidence <task> <n> --on <sha> --ran "<what ran and what it showed>"`, or
     `--not-run "<why, then To try: …>"` for a user check; for a high-risk task, `crew record-security-review <task> "<what it found, and
     what was done about each>"`. `crew show` prints those records here. `crew hand-over` reads
     only them: it refuses until every criterion has evidence on the work the branch has now,
     recorded as run unless it's a user check, and a high-risk task a security review. Anything
     typed below this comment is for people, and crew show keeps it below the records. -->

## Automated reviews
<!-- Appended by the worker's pr-review skill, one "### Automated review N" per review; the worker
     adds each finding's outcome. -->

## Review rounds
<!-- Appended by `crew feedback`. -->

## Log
