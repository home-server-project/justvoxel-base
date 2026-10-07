---
name: justvoxel-ci
description: Change, review, or diagnose JustVoxel GitHub Actions workflows for source checks, bootc image builds, publishing, signing, workflow triggers, cleanup, stable/testing image flows, and CI failures.
---

# JustVoxel CI

Make CI truthful, minimal, deterministic, and easy to diagnose.

## Investigation

- Inspect the exact workflow definition and commit/run SHA that actually executed.
- Isolate the first real failing step before editing workflow logic.
- Distinguish source-test failure, image-build failure, publish/signing failure, and runtime acceptance.
- Do not claim CI is green while required jobs are pending, skipped unexpectedly, or failed.

## Workflow Rules

- Pin third-party actions to full commit SHAs with useful version comments.
- Use the least permissions required by each job.
- Preserve established image signing and verification.
- Reuse existing build logic when that reduces real duplication; do not create a parallel validation framework.
- Keep PR/source checks, testing image builds, stable image builds, and human runtime acceptance conceptually separate.
- When stable CI is implemented or changed, prefer promotion of reviewed testing source plus a scheduled rebuild of the current stable source rather than fake commits.
- Do not use release/promotion workflows as a place to patch product code.

## Avoid Duplicate Validation

Do not repeatedly verify packages or facts guaranteed by Home Server Base unless JustVoxel has a specific integration dependency that needs its own check.

A check should prove a JustVoxel assumption, not re-prove the entire parent image.

## Trigger Hygiene

Agent instruction changes should not rebuild the appliance image.

Keep exclusions narrow:

- `AGENTS.md`
- `.agents/skills/**`

Do not broadly exclude application code, build files, tests, system files, or documentation that materially affects an image workflow.

Use source checks for source confidence, full image workflows for image confidence, and human VM testing for real runtime/visual acceptance.
