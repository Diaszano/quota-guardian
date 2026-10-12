# Security and privacy

## Local boundary

Quota Guardian assumes the local account is trusted. The daemon holds a same-user Unix socket with mode `0600` and rejects peer UIDs that do not match its owner. State and runtime directories are owner-only. SQLite stores session metadata, execution-context identity, consent revisions, and lifecycle evidence.

Prompts are ephemeral inputs to managed provider commands. They are not written to SQLite or telemetry. Provider output is retained only in bounded in-memory buffers. The application does not store transcripts, credential values, or environment values. Provider error text is reduced to allowlisted categories before persistence or user display.

## Consent and session ownership

Recovery consent defaults off. `run --auto-resume` is explicit and bound to the execution-context digest. Revoking consent prevents future retries. Background recovery is not evidence that the objective was completed.

Native hooks pass only provider/session IDs and an absolute workspace path. Payload size is capped at 1 MiB and hook work at 200 ms. Hook commands receive fixed neutral responses on parse, daemon, or timeout failures. They have no IPC methods for start, resume, cancel, consent, or permission changes. Discovered manual sessions cannot be taken over.

Do not enable provider execution or autonomous recovery based on synthetic fixtures, version checks, or plugin schema validation. Review the current [runtime evidence record](PROVIDER_COMPATIBILITY.md) before enabling any capability.

## Telemetry

No metric exporter is created unless `OTEL_METRICS_EXPORTER=otlp` is set. OTLP/HTTP uses standard OpenTelemetry endpoint configuration. Only fixed lifecycle-event and provider enums are metric attributes. Prompts, paths, session IDs, outputs, and provider error text are never metric labels. Export flush has a two-second shutdown bound and export errors do not block lifecycle completion.

## systemd

Service integration is user-scoped. It rejects root invocation, writes an owner-marked unit atomically, refuses to overwrite an existing unowned file, and does not enable lingering. The service uses `Type=notify`, `UMask=0077`, cgroup cleanup, and watchdog notifications only while scheduler and storage checks are fresh.

## Reporting

Do not include credentials or private workspace content in bug reports. Provide provider version, operating system, event category, and sanitized evidence. Naturally occurring quota failures may be shared after removing prompts, IDs, paths, and tokens; do not intentionally exhaust a quota for testing.
