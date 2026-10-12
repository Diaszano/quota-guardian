# Architecture

Quota Guardian uses a ports-and-adapters layout. `internal/domain` contains standard-library-only lifecycle, retry, consent, evidence, and transition rules. `internal/application` coordinates durable attempts, scheduling, observation, and shutdown. Adapters provide SQLite persistence, child-process supervision, provider-specific CLI translation, hooks, and private HTTP over a Unix socket. Infrastructure owns configuration, telemetry, systemd integration, and diagnostics.

```text
CLI / native hook
       │
       ├── private same-user Unix socket ── IPC adapter
       │                                      │
       └── allowlisted hook event ───────────┤
                                              ▼
                                  application coordinator
                                    │              │
                                    ▼              ▼
                              SQLite store    scheduler/provider
```

Managed and observed sessions have separate modes. Hooks can call only the observation route. Observed provider identity is deduplicated by provider, context, and provider session ID; stale timestamps do not move observation time backwards. State transitions remain revision-checked, and terminal states are immutable.

Provider adapters report settled-turn evidence independently from objective success. Retry policy runs only for verified scope and capabilities. Current provider start/resume/denial/quota capabilities are closed because live runtime evidence is not available. Simulated fixtures and official docs are not runtime evidence.

SQLite records local metadata and execution context but never prompt bodies, credential values, process output, or full transcripts. Output attachment stays in bounded memory. Telemetry labels are fixed enums, and export is opt-in.
