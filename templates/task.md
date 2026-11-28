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
     every check but a user check: a gate the user makes themselves, a live smoke test say. Only
     the user makes a gate one, at brief time or later, and crew records it: crew user-check
     <this task> <n> "<why>". crew show marks it under its criterion; typed words don't make one. -->

## Size
<!-- "About <n> lines: <areas>": your estimate of the lines the PR will add plus remove, tests
     and docs included, and the areas it spans. A spike needs none. -->

## Out of scope
<!-- Tempting adjacent work the worker must NOT do, and anything the user rules out (a migration,
     a new dependency). Follow-up ideas go in the Log. -->

## Open questions
<!-- Questions waiting on the user, one unchecked item each ("- [ ] …"): a worker's when it is
     blocked, and collaborators' PR comments from `crew feedback`. Whoever records the answer (crew
     decide) ticks the item. -->

## Decisions
<!-- Decisions made after dispatch. crew keeps them, each with its date and whose call it was:
     crew decide <this task> user|lead|worker "<decision>", with user for the user's own words or
     answer. crew show lists them here; a line typed here isn't one. -->

## Evidence
<!-- Written by the worker before the hand-over: one "### <n>. <the criterion, in a line> (on <sha>)"
     entry for each top-level item of Acceptance criteria, numbered in order, naming the commit it
     ran on. Each holds the command that shows the criterion holds and its output, in a fenced
     block trimmed to what shows the result; or, where only a look would show it, what you saw and
     how (the page, the steps, the tool); or, for a user check, why you can't run it, then "To try:"
     and what to try. Indent any output line that starts with "## ", or it ends this section. A
     high-risk task also has a "### Security review" entry: what /security-review found and what
     you did about it. `crew hand-over` refuses until every criterion has an entry, and a
     high-risk task this one. It refuses an entry that ran on other work than the branch has now,
     and one that says its check wasn't run unless it's a user check. -->

## Automated reviews
<!-- Appended by the worker's pr-review skill, one "### Automated review N" per review; the worker
     adds each finding's outcome. -->

## Review rounds
<!-- Appended by `crew feedback`. -->

## Log
