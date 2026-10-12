# Repository Governance and Release Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Standardize Quota Guardian development, protect `main`, and publish reviewable, versioned GitHub releases with verified build artifacts.

**Architecture:** Keep Go validation in GitHub Actions and expose the same baseline through Make and pre-commit. Use Release Please to propose version/changelog changes, then run GoReleaser in that same workflow execution when a release is created, avoiding a token-triggered tag workflow. Apply repository metadata and the `main` ruleset through GitHub settings after workflow checks are ready.

**Tech Stack:** Go 1.26, Make, pre-commit, GitHub Actions, Release Please, GoReleaser, GitHub CLI/API.

**Spec:** `docs/superpowers/specs/2026-10-12-repository-governance-release-design.md`

## Global Constraints

- `main` requires a pull request and passing CI, with zero required approvals.
- Keep third-party GitHub Actions pinned to full commit SHAs.
- Keep Linux amd64/arm64 as supported runtime targets; Darwin amd64/arm64 artifacts must say runtime support is unverified.
- Provider start, resume, denial classification, quota detection, and autonomous recovery stay disabled without reviewed runtime evidence.
- Manual sessions remain observation-only; no documentation may claim objective completion is assessed.
- Use MIT with `Copyright (c) 2026 Diaszano`.
- Do not rewrite or discard the pending plugin documentation changes.
- Release Please uses manifest baseline `0.1.0` to create `v0.1.0-rc.1` first; candidate tags remain prereleases until a later deliberate configuration change after runtime evidence review.
- Release Please uses a dedicated least-privilege repository token stored as an Actions secret so automatically created release PRs trigger required CI checks.

## Review Focus

- Release Please's prerelease strategy may generate an unintended tag or release type; verify the generated `v0.1.0-rc.1` tag and GitHub prerelease metadata in a non-publishing validation.
- Release Please-created pull requests must use the dedicated repository token to trigger CI; publish using the release output in the same workflow run rather than a tag-triggered workflow.
- GoReleaser's archive naming must preserve Darwin runtime-unsupported labeling and the Linux supported-platform statement.
- A required GitHub status check must use the actual stable job name (`verify`) and must run on pull requests.
- GitHub settings must allow PRs without approvals but must reject direct pushes, force pushes, and deletion of `main`.

---

### Task 1: License and contributor conventions

**Files:**
- Create: `LICENSE`
- Create: `.editorconfig`
- Create: `.pre-commit-config.yaml`
- Create: `CONTRIBUTING.md`
- Modify: `README.md`, `CHANGELOG.md`, `AGENTS.md`, `docs/ROADMAP.md`, `docs/IMPLEMENTATION_PLAN.md`

**Interfaces:**
- Consumes: Current Make targets and the development verification commands in `AGENTS.md`.
- Produces: MIT licensing; documented conventional commits, branch/PR flow, pre-commit setup, local validation, and accurate release-candidate status.

- [ ] Add standard MIT text with the copyright line fixed by the approved spec.
- [ ] Configure only basic whitespace, newline, YAML, and Go formatting hooks in pre-commit; pin hook repositories to immutable revisions.
- [ ] Document `pre-commit install`, `pre-commit run --all-files`, Conventional Commit prefixes, and required local checks in `CONTRIBUTING.md`.
- [ ] Update license and release-candidate wording in the listed docs without changing provider/runtime claims.
- [ ] Inspect the diff to confirm the existing pending plugin documentation files are untouched.

### Task 2: CI and repository check parity

**Files:**
- Modify: `.github/workflows/ci.yml`, `Makefile`
- Optional create: `.github/workflows/lint.yml` only if the existing workflow cannot keep one stable required job name.

**Interfaces:**
- Consumes: Task 1 pre-commit configuration and current Make checks.
- Produces: Stable required GitHub Actions job `verify` for PRs and `main`, with least privilege and checks matching documented local validation.

- [ ] Update CI triggers to pull requests and pushes to `main`; retain manual dispatch only if useful for diagnostics.
- [ ] Keep one stable `verify` job that checks formatting, vet, tests, race tests, architecture assertions, and pre-commit checks.
- [ ] Set explicit minimal workflow/job permissions and preserve SHA-pinned actions.
- [ ] Add or align a `make check` target with the documented local quality baseline; keep existing target names working.
- [ ] Verify workflow syntax, run pre-commit over tracked files, and run the Go verification commands required by `AGENTS.md`.

### Task 3: Reviewable automated releases

**Files:**
- Modify: `.github/workflows/release.yml`, `CHANGELOG.md`, `CONTRIBUTING.md`
- Create: `release-please-config.json`, `.release-please-manifest.json`, `.goreleaser.yaml`

**Interfaces:**
- Consumes: Conventional commits and the `VERSION` linker variable in `Makefile`.
- Produces: Release Please version/changelog PRs; GoReleaser Linux and labeled Darwin archives, checksums, and GitHub Release uploads when a release is created.

- [ ] Configure Release Please for the Go module and current `0.x` candidate maturity; reconcile the unpublished `0.1.0-rc.1` changelog record without treating it as a tag.
- [ ] Configure manifest baseline `0.1.0`, prerelease versioning with `rc`, and prerelease GitHub Releases; verify the first proposed tag is `v0.1.0-rc.1` for the Go release type before enabling the workflow.
- [ ] Configure Release Please to create a draft prerelease and force-create its version tag; run GoReleaser in the same workflow execution only when Release Please reports a created release, reuse that draft, upload the artifacts, then publish it.
- [ ] Configure GoReleaser for Linux amd64/arm64 and Darwin amd64/arm64 archives, embedding the tag in the version and clearly marking Darwin runtime support as unverified.
- [ ] Generate a checksum file and release notes that preserve current support and runtime evidence limitations.
- [ ] Use the `RELEASE_PLEASE_TOKEN` fine-grained repository secret for Release Please with only contents, issues, and pull-request write access so CI triggers on generated PRs; use `GITHUB_TOKEN` with `contents: write` for GoReleaser publishing. Keep third-party actions SHA-pinned.
- [ ] Document `RELEASE_PLEASE_TOKEN` and its minimum permissions in `CONTRIBUTING.md`; do not enable release PR automation until the secret is configured. Task 1 owns the general contributor guide; this task adds only release-specific secret setup.
- [ ] Validate GoReleaser configuration in snapshot/check mode and inspect the generated artifacts/metadata without publishing a public release.

### Task 4: GitHub contribution and maintenance files

**Files:**
- Create: `.github/ISSUE_TEMPLATE/bug_report.yml`, `.github/ISSUE_TEMPLATE/config.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/dependabot.yml`

**Interfaces:**
- Consumes: Task 1 contribution guidance and Task 2 CI job naming.
- Produces: Reproducible issue/PR intake and scheduled updates for Go modules and GitHub Actions.

- [ ] Add a concise bug report form asking for OS, Go version, reproduction steps, expected/actual behavior, and sanitized logs.
- [ ] Add a PR template with scope and verification checklist aligned to CI.
- [ ] Configure Dependabot for Go modules and GitHub Actions on a weekly schedule, grouped by ecosystem.
- [ ] Validate YAML and template links/wording.

### Task 5: GitHub settings, enforcement, and end-to-end verification

**Files:**
- GitHub repository metadata and settings (external)
- No repository files unless verification reveals a required adjustment.

**Interfaces:**
- Consumes: Stable `verify` CI job and all repository files from Tasks 1–4.
- Produces: Active `main` ruleset and standardized GitHub repository presentation/settings.

- [ ] Set repository description and relevant topics; enable delete-branch-on-merge and set squash as preferred while retaining the already available merge options unless GitHub settings demand a narrower change.
- [ ] Push the implementation branch and open a PR to `main` while the current ruleset is absent; wait until the PR reports the stable `verify` check.
- [ ] Create an active ruleset for `main` requiring a PR and successful `verify`, with zero required approvals, no bypass actors, and blocks for force pushes and branch deletion.
- [ ] Confirm the default branch remains `main`, verify settings with `gh`/GitHub API, and confirm the ruleset is active.
- [ ] Run `git diff --check`, `go vet ./...`, `go test ./...`, `go test -race ./...`, and `go test ./tests -run TestArchitecture` before committing implementation changes.
- [ ] Inspect the complete Git diff and verify no pending plugin installation documentation changes were lost or unexpectedly rewritten.
- [ ] Merge the PR only after the ruleset is active and CI is green.
