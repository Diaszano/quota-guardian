# Quota Guardian

Quota Guardian supervises managed AI-agent turns and discovers manually launched native sessions. Its core is a durable local state machine with bounded execution evidence. It does not judge whether a task objective was achieved: a completed turn means the provider reported a settled completion.

## Support status

This repository is an **operational release candidate**, not a verified initial release. Linux is the supported runtime platform. Antigravity and Codex have simulated adapters and documented native observation hooks. Their installed versions and hook package formats were checked locally; no real model turn, permission denial, quota exhaustion, provider resume, or live plugin install was exercised. Start, resume, denial classification, quota detection, and autonomous recovery remain disabled until reviewed version-specific runtime evidence exists. See [provider compatibility](docs/PROVIDER_COMPATIBILITY.md) and the [roadmap](docs/ROADMAP.md).

Manual sessions are observed in `observed` mode only. The initial release does not take them over, resume them, or infer consent. Managed-session background recovery is a separate explicit consent and remains capability-gated.

## Build and use

Requires Go 1.26 or newer and Linux for supervised runtime use.

```sh
make test
make race
make vet
make build VERSION=0.1.0-rc.1
./dist/quota-guardian --help
```

See [installation](docs/INSTALLATION.md) for configuration and systemd setup. The CLI accepts `run`, `sessions`, `status`, `doctor`, `daemon`, `observe`, and `version`. Run `quota-guardian --help` for command syntax. `doctor` checks local configuration and provider executables but does not make model requests.

## Safety and privacy

State and runtime files are local and owner-only. Prompts are passed to a provider but are not persisted. Native hooks send allowlisted session/workspace metadata over a same-user Unix socket; transcript, prompt, and tool content are ignored. Observation grants no consent and never triggers provider execution. Read [security](docs/SECURITY.md) before enabling background recovery.

OpenTelemetry metrics are no-op by default. Set `OTEL_METRICS_EXPORTER=otlp` to opt in to OTLP/HTTP export; only bounded event and provider labels are emitted.

## Development

`make test`, `make race`, `make vet`, and `make architecture` are the common checks. CI runs them on Linux. Cross-built macOS artifacts are marked runtime-unsupported; systemd service support is Linux-only. Real-provider and systemd user-manager tests require isolated, authorized environments and are not inferred from simulation.

Licensed under the [MIT License](LICENSE). See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution checks and the reviewable release flow.
