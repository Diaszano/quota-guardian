# Repository Governance and Release Automation Design

## Goal

Standardize development and repository maintenance for Quota Guardian, protect `main`, and automate versioned GitHub releases while retaining an explicit review point before each version is published.

## Agreed requirements

- The repository's primary branch is `main`.
- Changes to `main` must arrive through a pull request, and the CI check must pass. No approving review is required.
- Use the MIT License. The copyright holder is `Diaszano`, based on the repository owner.
- Use the full release automation option: conventional commits drive automated changelog/version PRs and merged release PRs publish GitHub Releases with build artifacts.
- Preserve the current release-candidate status and all provider/runtime evidence caveats. Automation must not describe simulated behavior as verified or turn on currently disabled provider capabilities.

## Current state

The project is a Go 1.26 module. `.github/workflows/ci.yml` already runs Linux checks; `.github/workflows/release.yml` currently builds cross-platform artifacts on tags or manual dispatch but does not publish a GitHub Release. `Makefile` provides test, race, vet, architecture, format, and build targets. `CHANGELOG.md` records `0.1.0-rc.1` as an unpublished candidate. No license or pre-commit configuration is present. GitHub's default branch is `main`; the repository has no active branch protection/ruleset. CI and release actions are SHA-pinned today.

## Design

### Development conventions

Keep Go formatting, vetting, unit/race tests, architecture assertions, and `git diff --check` as the required quality baseline. Make the CI check a single stable required status check for pull requests and pushes to `main`; use least-privilege workflow permissions. Keep the existing SHA-pinning convention for third-party actions.

Add a small `.pre-commit-config.yaml` for whitespace, end-of-file, YAML, and Go formatting checks. Document how to install and run it; CI remains authoritative and must run the same repository checks so a missing local hook cannot bypass validation. Add `.editorconfig` for basic whitespace/newline consistency and a `CONTRIBUTING.md` describing branches, conventional commit prefixes, pull requests, local checks, and release flow.

### Versioning and release publishing

Use Conventional Commits with Release Please's Go release type to open/update a release PR containing the version and changelog updates. Merging that PR creates the version tag and GitHub Release. This makes version changes reviewable and prevents every push to `main` from publishing a release. Configure release automation for the current `0.x`/release-candidate maturity; stable `1.0.0` requires a later deliberate maintainer decision and must not be inferred from runtime evidence. Because GitHub suppresses workflow runs caused by its built-in `GITHUB_TOKEN`, Release Please must use a dedicated least-privilege repository token stored as an Actions secret so its PR receives the required CI check. GitHub Actions must also be allowed to create pull requests in repository settings.

When Release Please reports that it created a release, run GoReleaser in that same workflow execution, using the resulting tag. This avoids relying on a tag-push workflow started by the workflow token, which GitHub may suppress. GoReleaser builds the CLI for Linux amd64 and arm64, creates archives and checksums, and publishes them to the GitHub Release. It may retain Darwin cross-builds only if their artifact names and release notes state that runtime support is unverified; Linux remains the only supported runtime. Do not claim provider execution, systemd lifecycle, signing, or notarization evidence that the project has not collected. Release credentials use the repository token with only the permissions required for the release job. Keep release actions pinned to full commit SHAs.

The existing candidate note in `CHANGELOG.md` must be reconciled with the automated changelog without presenting `0.1.0-rc.1` as already published. The manifest baseline is `0.1.0`; prerelease versioning must create `v0.1.0-rc.1` as the first tag, with subsequent candidate releases incrementing the `rc` number. The existing changelog entry remains an unpublished candidate record and is not treated as a published tag. Stable releases remain out of scope until the runtime evidence gates are reviewed and the release configuration is deliberately changed.

### License and repository presentation

Add the standard MIT license text with copyright `Copyright (c) 2026 Diaszano`, update README and contributor/release documentation to remove the statement that no license is selected, and identify the project as MIT-licensed. Do not change support claims in README: Linux runtime support, simulated provider adapters, observation-only manual sessions, and capability gates remain as currently documented.

Set a concise GitHub repository description and relevant topics, enable automatic deletion of head branches after merge, and keep squash merging as the preferred/recommended merge method. Add issue and pull-request templates that request reproducible context and verification evidence. Add a Dependabot configuration for Go modules and GitHub Actions.

### Main branch policy

Create an active ruleset targeting `main` that requires a pull request and the stable CI status check, blocks force pushes and branch deletion, and requires zero approvals. Do not add signed-commit requirements, merge queue, deployment gates, or restrictions that were not requested. Configure the repository's default branch as `main` if it is not already.

## Scope and files

Expected repository changes include:

- `.github/workflows/ci.yml` and `.github/workflows/release.yml` for CI/release permissions and publishing flow.
- `.goreleaser.yaml` (or current GoReleaser-supported config filename), `.release-please-manifest.json`, and `release-please-config.json` for builds and versioning.
- `.pre-commit-config.yaml`, `.editorconfig`, and `CONTRIBUTING.md` for local standards.
- `LICENSE`, `README.md`, `CHANGELOG.md`, and `AGENTS.md` for licensing and accurate development/release guidance.
- `.github/ISSUE_TEMPLATE/*`, `.github/PULL_REQUEST_TEMPLATE.md`, and `.github/dependabot.yml` for maintenance conventions.
- GitHub repository settings, metadata, and a `main` ruleset for repository-level presentation and enforcement.

Existing uncommitted plugin installation documentation changes are outside this design's file scope and must be preserved without being rewritten or discarded.

## Verification and acceptance

- The existing Go verification commands still pass, including `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go test ./tests -run TestArchitecture`.
- CI validates formatting and the same required checks on pull requests and `main`.
- The release configuration can build the Linux artifacts and checksum output from a test tag without publishing a public release; a controlled workflow verification confirms tag-to-GitHub-Release publishing and correct prerelease metadata before the first public release.
- Release artifacts include checksums and correctly state supported platforms; no runtime support claim exceeds existing evidence.
- The MIT license is present and all project docs agree on its status.
- A pull request without approvals can merge after CI succeeds; direct pushes, force pushes, and deletion of `main` are blocked by the active ruleset.
- Pre-commit hooks run successfully across tracked files and are documented for contributors.
- GitHub metadata, templates, Dependabot, and merge settings match this specification.

## Risks and limits

Release Please requires a dedicated repository token as an Actions secret so CI runs on its automatically created PRs; the maintainer must configure this secret and allow Actions to create pull requests before enabling automated release PRs. Release Please and GoReleaser require careful alignment of tag, changelog, and prerelease behavior. GitHub repository rulesets and metadata are external settings and need authenticated GitHub API access; their final active state must be checked after configuration. Releasing binaries does not establish provider or Linux systemd runtime verification.
