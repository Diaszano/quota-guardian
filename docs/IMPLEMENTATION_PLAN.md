# Implementation plan

The detailed [Superpowers implementation plan](superpowers/plans/2026-10-11-quota-guardian.md) implements the [approved design](superpowers/specs/2026-10-11-quota-guardian-design.md).

Status: implementation tasks 1–18 are recorded complete on the isolated implementation branch. The result is a limited operational release candidate, not a verified initial release: provider-runtime capabilities remain disabled, systemd user-manager lifecycle was not exercised, and no license has been selected.

Order: lifecycle and retry invariants; SQLite transactions and crash recovery; Linux process supervision; scheduling and IPC; CLI and provider adapters; native observation and packaging; operational hardening and separately authorized runtime verification; release preparation.

Execution record: TDD was used for implementation tasks and task-level checks are recorded in the SDD ledger. Real provider turns, provider plugin installation, quota exhaustion, and systemd user-manager lifecycle were not run. Public release remains gated on runtime evidence and maintainer license approval.
