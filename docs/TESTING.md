# Testing

## Local checks

```sh
make fmt
make vet
make test
make race
make architecture
make build VERSION=0.1.0-rc.1
git diff --check
```

The CI workflow runs formatting checks, vet, unit tests, race tests, and architecture assertions on Linux. Tests use synthetic provider events, fake runners, and temporary SQLite databases; they do not make model requests.

## Evidence levels

1. Unit and architecture checks prove local code behavior and dependency boundaries.
2. Synthetic fixtures and fake-runner tests prove parsers and coordinator behavior against recorded shapes.
3. Official documentation and local CLI version checks confirm published interface descriptions and binary identity.
4. Only separately authorized `provider_runtime` tests in an isolated workspace count as live-provider evidence.

Current runtime evidence is limited to version checks. No model turn, permission denial, quota exhaustion, live hook delivery, provider resume, or plugin install/remove was run. See `tests/testdata/runtime/README.md` and [provider compatibility](PROVIDER_COMPATIBILITY.md).

## Optional gated checks

`go test -tags=provider_runtime ./tests -run TestProviderRuntime -v` requires `QG_RUNTIME_AUTHORIZED=1`, `QG_RUNTIME_WORKSPACE`, `QG_RUNTIME_AGY_BIN`, and `QG_RUNTIME_CODEX_BIN`. It currently checks executable versions only; it does not enable provider capabilities.

`go test -tags=systemd ./tests -run TestSystemdLifecycle -v` is reserved for an authorized disposable systemd user manager. This environment did not provide one. The generated unit was syntax-checked with `systemd-analyze verify`; lifecycle behavior was not run.

## Release artifacts

`make build VERSION=...` creates an ignored native binary in `dist/`. The release workflow cross-builds Linux binaries and macOS binaries labelled runtime-unsupported, then creates a `SHA256SUMS` file for each artifact bundle. It uploads workflow artifacts only and does not publish a public release.
