# Roadmap and release gates

## Current candidate

The tested local core, private IPC, simulated provider adapters, native metadata observers, bounded telemetry, and explicit Linux user-service packaging are available as an operational release candidate. Manual sessions remain observation-only. The repository uses the maintainer-approved [MIT License](../LICENSE). This candidate has not been published, and live-provider behavior remains unverified.

## Required before an initial release

- Run provider-specific tests only after explicit authorization, against isolated user-approved workspaces and configurations.
- Verify stdin/EOF, explicit same-ID continuation, permission-denial behavior, provider settlement, and native hook delivery on pinned versions.
- Obtain sanitized naturally occurring quota/reset evidence before implementing quota classification. Never induce a quota limit.
- Verify provider plugin installation and removal in disposable configurations; review hook trust behavior.
- Run service readiness, watchdog, shutdown, and uninstall checks in a disposable systemd user manager.
- Review version-specific evidence and enable each capability independently. Missing evidence keeps execution, denial classification, and autonomous recovery disabled.

## Deferred platform support

macOS and Windows artifacts are compile-only and explicitly runtime-unsupported. Native background service managers for those platforms require separate lifecycle implementations and tests.
