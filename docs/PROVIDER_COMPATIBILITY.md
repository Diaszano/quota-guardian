# Provider compatibility research baseline

Investigated on 2026-10-11. This is a research baseline, not a tested integration claim. Installed help confirms command availability; documentation describes contracts that still require version-specific validation.

| Capability | Antigravity CLI | Codex CLI |
| --- | --- | --- |
| Installed version | `agy 1.3.3` | `codex-cli 0.162.1` |
| Headless execution | `--print` / `-p` in installed help | `exec` in installed help |
| Structured output | `--output-format stream-json` in help; documented `init`, `step_update`, `result` events | `--json` in exec resume help; documented JSONL events including `thread.started`, `turn.completed`, `turn.failed` |
| Session identity | Documented `conversation_id` in headless events | Documented `thread_id` in `thread.started` |
| Resume a selected session | `--conversation` in help and official headless documentation | `codex exec resume [SESSION_ID] [PROMPT]` in installed help |
| Plugin management | Installed `plugin` help lists install, uninstall, enable, disable, validate | Installed `plugin --help` lists `add`, `remove`, `list`, `marketplace`; installation not exercised |
| Native observation | Documented hooks include `PreInvocation`, `PostInvocation`, `Stop`; common payload includes `conversationId` and `workspacePaths` | Official plugin documentation describes `SessionStart` hooks and explicit user trust requirements |
| Native quota signal | Documented `Stop` has `terminationReason`, optional `error`, and `fullyIdle`; exact quota messages and reset metadata unverified | Quota-specific hook contract not established in this assessment |
| Live quota detection and recovery | Not tested | Not tested |
| Recovery after terminal/OS restart | Not implemented or tested | Not implemented or tested |
| Safe autonomous takeover of a manually launched session | Not established | Not established |

## Boundaries for the design

Use explicit saved IDs, never the most recent session, for recovery. Resuming context does not prove exactly-once tool execution or safe takeover of another active process. Native observation and managed execution must remain distinct capabilities. Provider permissions and hook trust must be preserved.

Antigravity's documented Stop hook can request immediate continuation. That is not evidence of delayed background recovery, and hooks must not wait through quota cooldowns. Codex plugin installation does not automatically trust bundled hooks.

Do not classify quota exhaustion by searching arbitrary model text or tool output for `429`: source and event context matter. Exact provider error fields, reset timestamps, retry semantics, headless permission handling, and hook payload compatibility remain validation work.

## Evidence

Local read-only checks: `agy --version`, `agy --help`, `agy plugin --help`, `codex --version`, `codex --help`, `codex exec resume --help`. Codex help completed with a warning that it could not create PATH aliases in the restricted filesystem.

Official pages opened during assessment:

- [Antigravity headless execution and continuation](https://www.antigravity.google/docs/cli/headless/)
- [Antigravity hooks and payloads](https://www.antigravity.google/docs/hooks/)
- [Antigravity plugins](https://www.antigravity.google/docs/plugins?tab=cli)
- [Codex non-interactive execution](https://learn.chatgpt.com/docs/non-interactive-mode)
- [OpenAI plugin packaging and hook trust](https://developers.openai.com/plugins/build/plugins)
- [Codex hooks](https://learn.chatgpt.com/docs/hooks)

Documentation is rolling rather than pinned to the installed releases. No live provider turn or quota exhaustion was triggered.

## Design-stage follow-up

Further read-only checks included `codex exec --help` and `codex plugin --help`. The official Codex hooks page documents SessionStart discovery, common session metadata, and asynchronous hooks, but warns that transcript format is unstable. Async completion does not initiate a new turn. These are documentation findings, not tested contracts for the installed binary.

The [design specification](superpowers/specs/2026-10-11-quota-guardian-design.md) separates local-help verification, documented contracts, future simulation coverage, and future runtime verification. No simulated or live integration tests currently exist. Safe autonomous resume remains gated on policy preservation, identity, and settled invocation evidence. Normal-launch discovery does not authorize takeover of a manually running session.
