# Real-provider evidence

Runtime tests require a user-approved isolated workspace and explicit `QG_RUNTIME_AUTHORIZED=1`. The tagged harness checks only installed executable versions; model turns, stdin/EOF, same-ID resume, denied permissions, quota signals, plugin install/removal, and settlement remain unverified until a separately reviewed harness and safe fixtures exist. Do not induce quota exhaustion. Synthetic fixtures do not count as live-provider evidence.

Evidence record for 2026-10-12 (Linux): `agy --version` reported `1.3.3`; `codex --version` reported `codex-cli 0.162.1`. No model turn, plugin installation, or quota request was made. No runtime authorization was supplied. All execution, resume, denial-classification, quota, and autonomous capabilities remain disabled.
