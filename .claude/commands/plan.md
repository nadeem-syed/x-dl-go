---
description: Generate a plan from a spec, validate it against the spec, and save to _plans/
argument-hint: Optional spec slug or path (defaults to spec matching current branch)
allowed-tools: Read, Write, Glob, Grep, Bash(git branch:*), Bash(git rev-parse:*)
---

You are helping turn an existing feature spec into a concrete implementation plan that another agent (or the user) can execute. Always adhere to any rules or requirements set out in any CLAUDE.md files when responding.

User input: $ARGUMENTS

## High level behavior

Your job is to:

1. Locate the right spec file (branch-driven by default; explicit override allowed)
2. Read the spec carefully
3. Produce a plan that covers every Functional Requirement and Acceptance Criterion, with no out-of-scope additions
4. Validate the plan against the spec — flag any gaps or scope drift before saving
5. Save the plan to `_plans/<feature_slug>.md` using the structure in `_plans/template.md`
6. Print a short summary

You MUST NOT write any application code as part of this command. This is plan generation, not implementation.

## Step 1. Locate the spec

Determine the `feature_slug` and the spec file path:

- If `$ARGUMENTS` is non-empty, treat it as either:
  - a slug (e.g. `clip-video-time-range`) → spec at `_specs/clip-video-time-range.md`
  - a path (e.g. `_specs/clip-video-time-range.md`) → use as-is
- If `$ARGUMENTS` is empty:
  - Read the current git branch (`git rev-parse --abbrev-ref HEAD`)
  - If the branch matches `claude/feature/<slug>`, the spec is `_specs/<slug>.md`
  - If the branch does NOT match that pattern (e.g. you're on `main`), abort with: "No spec specified and current branch is not a `claude/feature/*` branch. Pass a slug or path."

If the spec file does not exist on disk, abort with: "Expected `_specs/<slug>.md` to exist — did `/spec` run successfully?"

## Step 2. Read the spec

Read the spec file in full. Pay particular attention to:

- **Summary** — what is being built
- **Functional Requirements** — every requirement MUST map to at least one task in your plan
- **Acceptance Criteria** — every criterion MUST be testable; record how you'll know each is met
- **Edge Cases** — must be handled by a task or explicitly noted as out-of-scope
- **Open Questions** — resolve as many as you can during planning; surface the rest

You may also explore the codebase to inform your plan (read files, grep for relevant symbols). Do not write any code.

## Step 3. Draft the plan

Use the structure in `@_plans/template.md`. Key rules:

- **Right-size the task list.** Small feature → one task is fine. Large feature → multiple tasks, each independently reviewable and revertable.
- **Each task names the files it will touch.** Don't be vague — "modify config" is not enough; specify `cmd/x-dl/main.go` and the change.
- **Each task includes test guidance** when tests are appropriate. Pure refactors or doc changes may not need new tests; say so.
- **Tasks are ordered** so the diff at each step is clean and the codebase stays in a buildable state.
- **No scope creep.** If you find yourself wanting to add a task not implied by the spec (e.g. "also refactor X"), STOP. Either add it to "Out-of-scope decisions" with a one-line rationale, or push back to the user to revise the spec first.

## Step 4. Validate the plan against the spec

Before saving, run through the **Validation against the spec** checklist at the top of the template and fill it in honestly:

- For each Functional Requirement in the spec, point at the task(s) that cover it. If any are unmapped, note the gap.
- For each Acceptance Criterion, name the test (or other verification) that will prove it. If any criterion isn't testable as written, flag it.
- For each task, confirm it doesn't introduce capability the spec doesn't ask for. If it does, move that work to "Out-of-scope decisions" or surface it as a concern.
- For each Open Question in the spec, either record your resolution or note that it's still open and needs the user's input before implementation can start.

If the validation surfaces real issues, INCLUDE them in the saved plan — do not silently fix them by inflating the spec.

## Step 5. Save the plan

Write the plan to `_plans/<feature_slug>.md`. Use the slug derived in Step 1. If `_plans/<feature_slug>.md` already exists, overwrite it (the user is re-planning).

## Step 6. Final output to the user

Respond with a short summary in this exact format:

Spec:  _specs/<feature_slug>.md
Plan:  _plans/<feature_slug>.md
Tasks: <count>
Validation: <"clean" | "N issues flagged — see plan">
Open questions remaining: <count>

Do NOT repeat the full plan in chat. The user will open the file to review it.
