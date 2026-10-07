---
name: justvoxel-review
description: Perform an independent human-directed review and verification of completed JustVoxel changes by inspecting the real diff, scope, relevant tests, architecture, regressions, and unverified runtime behavior.
---

# JustVoxel Review

Review the implementation that actually exists, not the Codex completion narrative.

Review is read-only by default.

## Review Process

1. Inspect current branch, `git status`, diff/stat, and the exact changed files.
2. Compare the changes with the approved task scope.
3. Read affected callers, contracts, tests, and documentation when needed to understand regression risk.
4. Run only the tests/checks that are sensible for the changed areas.
5. Separate what was proven from what remains unverified.
6. Report concrete findings without modifying code unless the human separately approves a fix scope.

## What to Look For

Prioritize:

- correctness bugs;
- data-loss/recovery risks;
- regressions in known-working behavior;
- authorization or privileged-boundary mistakes;
- duplicated orchestration or architecture drift;
- transactional-safety failures;
- desktop/mobile WebUI regressions;
- unintended scope expansion or unrelated cleanup;
- tests/docs that no longer match behavior.

Do not invent findings to make a review look thorough. Avoid style nitpicks and broad refactor suggestions unless they materially affect correctness or maintenance.

## Scope Discipline

Verification may discover additional problems. Discovery is not permission to fix them.

If an out-of-scope test fails or another subsystem appears broken:

- identify it clearly;
- explain whether it blocks the approved work;
- do not chase it through unrelated files;
- let the human define the next scope.

Do not sacrifice known-working behavior merely to make the current task or test suite look green.

## Evidence Levels

Keep these separate:

- source review/tests: source-level evidence;
- CI image build: image/build evidence;
- human development-VM testing: real boot/runtime/visual evidence.

Do not claim WebUI visual acceptance from Go/JavaScript tests or runtime acceptance from CI alone.

## Findings Format

Keep findings concise and actionable:

- exact file/path and line/range when practical;
- what is wrong;
- why it matters;
- the safest intended correction.

If no meaningful issue is found, say so and note any important area that was not actually verified.
