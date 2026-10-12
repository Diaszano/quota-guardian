# Quota Guardian

Quota Guardian is a local supervisor and session observer for AI coding agents. It keeps a durable record of managed turns, exposes local status and control commands, and can discover sessions started directly in Codex or Antigravity.

The core is a durable local state machine with bounded execution evidence. It records whether a provider reported a turn as settled; it does not decide whether the user's task was successful. Native plugins only report session metadata to a running Quota Guardian daemon. They do not start or resume providers, take over manual sessions, grant consent, or add prompts to agent context.

## Install the Codex or Antigravity plugin

Install the Quota Guardian binary first, put `quota-guardian` on `PATH`, and start its daemon. The [installation guide](docs/INSTALLATION.md) explains building the binary, configuration, and the optional Linux systemd user service. Linux is the only supported runtime platform.

From the repository root, choose the plugin for the agent you use:

### Codex

Register the local marketplace in the Codex CLI:

```sh
codex plugin marketplace add "$PWD"
codex plugin marketplace list
```

Restart the ChatGPT desktop app, open Codex's **Plugins Directory**, select the `quota-guardian` marketplace, and install `quota-guardian-observer`. Review and trust its `SessionStart` hook when prompted. These hooks run in local Codex sessions, not cloud sessions.

### Antigravity (agy)

Install and enable the local plugin directory:

```sh
agy plugin install "$PWD/plugins/agy"
agy plugin list
agy plugin enable quota-guardian-observer
agy plugin list
```

Review `plugins/agy/plugin.json` and `plugins/agy/hooks.json` before enabling the plugin.

Both plugins are observation-only. If the daemon is unavailable, the hooks return neutral output and do not take over the agent session. See the detailed [Codex](plugins/codex/README.md) and [Antigravity](plugins/agy/README.md) instructions for removal and provider-specific details.

## Support status

This repository is an **operational release candidate**, not a verified initial release. Linux is the supported runtime platform. No real model turn, permission denial, quota exhaustion, provider resume, or live plugin installation has been validated. Start, resume, denial classification, quota detection, and autonomous recovery remain disabled until reviewed version-specific runtime evidence exists. macOS archives are compile-only and marked runtime-unsupported. See [provider compatibility](docs/PROVIDER_COMPATIBILITY.md) and the [roadmap](docs/ROADMAP.md).

Manual sessions are observed in `observed` mode only. They are not taken over, resumed, or treated as consent. Managed-session background recovery requires separate explicit consent and remains capability-gated.

## Build from source

Requires Go 1.26 or newer. Linux is required for supported runtime use.

```sh
make check
make build VERSION=0.1.0-rc.1
./dist/quota-guardian --help
```

See [installation](docs/INSTALLATION.md) for configuration and systemd setup. The CLI includes `run`, `sessions`, `status`, `doctor`, `daemon`, `observe`, and `version`. `doctor` checks local configuration and provider executables but does not make model requests.

## Safety and privacy

State and runtime files are local and owner-only. Prompts are passed to a provider but are not persisted. Native hooks send allowlisted session/workspace metadata over a same-user Unix socket; transcript, prompt, and tool content are ignored. Observation grants no consent and never triggers provider execution. Read [security](docs/SECURITY.md) before enabling background recovery.

OpenTelemetry metrics are no-op by default. Set `OTEL_METRICS_EXPORTER=otlp` to opt in to OTLP/HTTP export; only bounded event and provider labels are emitted.

## Development

`make test`, `make race`, `make vet`, and `make architecture` are the common checks. CI runs them on Linux. Cross-built macOS artifacts are marked runtime-unsupported; systemd service support is Linux-only. Real-provider and systemd user-manager tests require isolated, authorized environments and are not inferred from simulation.

Licensed under the [MIT License](LICENSE). See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution checks and the reviewable release flow.
