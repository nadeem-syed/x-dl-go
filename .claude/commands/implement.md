---
description: Implement tasks from a plan one at a time, run tests, and self-grade against the spec at the end
argument-hint: Optional task number (e.g. "2") to implement just one task. Default: implement all tasks in order.
allowed-tools: Read, Write, Edit, Glob, Grep, Bash
---

You are helping execute a previously approved plan. Always adhere to any rules or requirements set out in any CLAUDE.md files when responding.

User input: $ARGUMENTS

## High level behavior

Your job is to:

1. Locate the right plan and spec for the current feature
2. Implement one task at a time, writing tests with the code
3. Run the tests; do not move to the next task until they pass
4. At the end, self-grade the diff against the spec's Acceptance Criteria
5. Refuse to declare the feature done if any Acceptance Criterion is not satisfied
6. NEVER commit, push, or open a PR — the user does that

## Step 1. Locate the plan and spec

Determine the `feature_slug`:

- Read the current git branch (`git rev-parse --abbrev-ref HEAD`)
- If the branch matches `claude/feature/<slug>`, use that slug
- If not, abort with: "Current branch is not a `claude/feature/*` branch — checkout the feature branch first."

Then:

- Read the plan: `_plans/<slug>.md`. Abort if missing: "No plan at `_plans/<slug>.md` — did `/plan` run successfully?"
- Read the spec: `_specs/<slug>.md`. Abort if missing.

Surface any **gaps** or **concerns** flagged in the plan's Validation block. If the plan flagged unresolved Open Questions, ask the user before proceeding — do NOT improvise answers.

## Step 2. Decide which tasks to run

- If `$ARGUMENTS` is a single integer (e.g. `2`), implement only Task 2 from the plan, then stop.
- If `$ARGUMENTS` is empty, implement all tasks in order (Task 1, Task 2, ...).
- If `$ARGUMENTS` is a range like `2-3` or a list like `2,3`, implement those tasks in order.

## Step 3. Implement each task

For each task in scope, in order:

1. **State the task.** Print "▶ Task N: <name>" so the user sees progress.
2. **Make the code changes** described in the task's "Files touched" and "What it does" sections. Stay strictly inside what the task describes — do not refactor adjacent code, do not rename unrelated symbols, do not "fix" things you notice in passing. If you find a real problem outside scope, note it for the user but do not act.
3. **Write the tests** described in the task's "Tests" section (or skip if the task explicitly says no tests). Tests live in `./tests/` or next to the code they test, per the language's convention (Go: `*_test.go` next to the file).
4. **Run the tests.** For Go: `go test ./...`. If a Makefile target exists (`make test`), prefer that.
5. **If tests fail:** fix the implementation and re-run. Do NOT move to the next task with red tests. If you can't get them green after a reasonable attempt, STOP and report the failure to the user — do not skip ahead.
6. **Print a short task summary** when done: files changed (list), tests passing (count).

## Step 4. Self-grade against the spec

After the last task in scope is done, re-read the spec's **Acceptance Criteria** section. For each criterion, evaluate honestly:

- **PASS** — the change satisfies this criterion. Cite the file/test that proves it.
- **PARTIAL** — partially satisfied. Describe what's missing.
- **FAIL** — not satisfied. Describe why.

Print a self-grade table with one row per criterion. If ANY criterion is PARTIAL or FAIL, explicitly say "Feature is NOT done" and explain what's needed. Do not declare the feature done unless every criterion is PASS.

## Step 5. Final output to the user

Print:

Tasks completed: <list of task numbers>
Tests: <count> passing
Self-grade: <"all green" | "N criteria not met — see above">
Files changed: <list>

Status: <"Ready for your review and commit" | "Blocked — see above">

You MUST NOT run `git commit`, `git push`, or `gh pr create`. The user reviews the diff and commits manually.
