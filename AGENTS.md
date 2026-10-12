# Agent Guidelines - Quota Guardian

This repository adheres to strict architectural boundaries and development discipline.

## Development Verification Checks

Before committing changes, always execute the required verification pipeline:
1. Code formatting: `gofmt -w <changed-files>`
2. Static analysis: `go vet ./...`
3. Test suite: `go test ./...` (and race tests where applicable: `go test -race ./...`)
4. Git hygiene: `git diff --check`
5. Architecture assertions: `go test ./tests -run TestArchitecture`

## RTK Command Execution

This environment uses RTK (Token-Efficient Command Execution) to optimize output token volume.
- Command rewrites are handled by the configured RTK hooks at `PreToolUse`.
- When inspecting test or build output, let the hook condense results.
- In case output is unexpectedly truncated or contradicts exit codes, re-run using `rtk proxy <cmd>`.
- Do not remove or alter RTK configuration.

## Core Architectural Boundaries

- `internal/domain`: May import ONLY Go standard library packages. Absolutely no database/sql, OS process, or adapter packages.
- Session Lifecycle & Evidence:
  - Terminal states (`COMPLETED`, `FAILED`, `CANCELLED`) are strictly immutable.
  - No field may report "objective success". A completed turn means the supervised turn settled with provider-confirmed completion; objective completion is never assessed.
  - Discovered manual sessions cannot be taken over in the initial release.

## Release and evidence claims

- `make test`, `make race`, `make vet`, and `make architecture` are the standard checks; CI also checks formatting and architecture boundaries.
- Synthetic fixtures, official documentation, and binary version checks do not count as live-provider runtime evidence.
- Keep provider start, resume, denial classification, quota detection, and autonomous recovery disabled until separately reviewed version-specific evidence exists.
- The release candidate is not a verified initial release while provider-runtime or disposable systemd lifecycle gates remain untested. The repository uses the maintainer-approved MIT License; see `LICENSE` and `CONTRIBUTING.md`.
