# Contributing

Quota Guardian is MIT-licensed; see [LICENSE](LICENSE). Requires Go 1.26 or newer. Linux is the supported runtime platform.

## Branches and pull requests

Create a focused branch from `main` and open a pull request describing the change and its verification. Changes to `main` must go through a pull request with passing CI; no approving review is required. Prefer squash merging and use a Conventional Commit title for the resulting commit.

Use `feat:` for features, `fix:` for bug fixes, and `docs:`, `test:`, `refactor:`, `perf:`, `build:`, `ci:`, or `chore:` for other changes. An optional scope follows the prefix, for example `fix(ipc): reject invalid requests`. Mark breaking changes with `!` or a `BREAKING CHANGE:` footer.

## Local checks

Install [pre-commit](https://pre-commit.com/#installation), then run:

```sh
pre-commit install
pre-commit run --all-files
```

The hooks fix whitespace, final newlines, and Go formatting and check YAML syntax. Review and stage any fixes before committing. Run the full baseline before each commit:

```sh
# Format changed Go files, when applicable:
gofmt -w <changed-go-files>
make vet
make test
make race
make architecture
git diff --check
```

CI enforces these checks even when local hooks are absent. Keep `internal/domain` within its standard-library-only boundary and terminal session states immutable. See [AGENTS.md](AGENTS.md) for architecture and evidence rules.

## Releases

Conventional Commits drive a Release Please pull request containing version and changelog updates. Review that PR before merging it. Merging the release PR creates its tag and a draft prerelease; GoReleaser builds archives and checksums in the same workflow run, attaches them, and publishes the prerelease. Candidate versions begin at `v0.1.0-rc.1`; the existing changelog notes are an unpublished candidate record, not a prior release. Subsequent candidates increment the `rc` number.

The initial automation commit carries the one-time `Release-As: 0.1.0-rc.1` footer; preserve that footer in the squash merge message when introducing automation to `main`. The manifest baseline stays `0.1.0`; the footer selects the first candidate without freezing later versions. Do not repeat the footer on subsequent commits. Stable releases require a later deliberate configuration change after runtime evidence review.

Release Please uses the dedicated fine-grained repository token in the Actions secret `RELEASE_PLEASE_TOKEN`, with access limited to this repository and **Contents: write**, **Issues: write**, and **Pull requests: write**. This token lets generated release PRs trigger required CI. Configure and maintain the secret before enabling release PR automation; GoReleaser uses `GITHUB_TOKEN` with `contents: write` for publishing. Its workflow permissions are scoped to that job; Release Please has only read access through `GITHUB_TOKEN`.

Publishing artifacts does not verify provider execution or systemd lifecycle behavior. Provider start, resume, denial classification, quota detection, and autonomous recovery remain disabled pending separately reviewed version-specific runtime evidence. Manual sessions remain observation-only. See [release gates](docs/ROADMAP.md).
