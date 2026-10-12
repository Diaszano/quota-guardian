# Quota Guardian Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Linux-first quota-aware supervisor whose durable recovery is safe under concurrency and crashes, with independently verified native session observation.

**Architecture:** One daemon owns SQLite, scheduling, and managed invocations. Provider-independent domain/application code uses injected ports; adapters implement SQLite, processes, IPC, and verified provider contracts. Unknown capabilities fail closed, and observed manual sessions cannot be taken over.

**Tech Stack:** Go standard library, SQLite via a maintained Go driver selected in Task 3, `log/slog`, OpenTelemetry, Linux Unix sockets and systemd user services. No application dependencies are installed during planning.

**Spec:** [Approved design with requested refinements](../specs/2026-10-11-quota-guardian-design.md).

**Status:** Proposed plan for user review. All checkboxes represent future work. Specification refinement and planning are authorized; implementation is not.

## Global Constraints

- Linux is the first supported runtime. Portable interfaces and cross-compilation do not constitute macOS or Windows runtime support.
- Domain imports only the standard library. Application imports domain and ports, never provider adapters, SQL drivers, or OS-specific process code.
- SQLite is owned exclusively by the daemon. Clients and hooks use IPC, never open the database.
- No resume without explicit ID; no schedule earlier than verified reset; no automatic retry of generic errors.
- Manual-session takeover is explicitly unsupported in the first release.
- `COMPLETED` means the supervised turn settled with provider-confirmed completion and no detected blocker.
- CLI copy must say "Turn completed; objective completion not assessed."
- No original prompt, credentials, raw output, arbitrary argv, or environment snapshot in durable state.
- Retry defaults: margin 30 seconds, base 60 seconds, cap 1 hour, additive jitter 0–10% of fallback delay, five resumes, seven-day deadline, four concurrent invocations.
- Lease default: 30 seconds, renewed every 10 seconds. Expiry does not authorize a replacement process.
- Observer limits: 1 MiB input, 200 ms total delivery budget, no disk queue, no cooldown sleeps.
- IPC: private Unix socket, 1 MiB request limit, 64 connections, 5-second control admission deadline; state directories 0700 and files/socket 0600.
- No permission bypass, private provider APIs, destructive installation, deliberate quota exhaustion, or unapproved real model usage.
- Evidence labels are local-help, official-documentation, simulated, runtime-verified, and unavailable; none implies another.
- This is greenfield. The only current files are assessment/design/compatibility documentation. No application code, module, fixtures, or test suite exists.

## Review Focus

1. A permission denial followed by a nominal success/exit zero must remain attention, never success (Tasks 1, 12).
2. Stderr fills while stdout delivers the final event: drain concurrently without deadlock or false completion (Tasks 6, 12).
3. IPC acknowledgement is lost and the same submission key is retried: return the existing task without replaying a prompt (Tasks 4, 10).
4. A quota error arrives before resumed-running confirmation: absent identity or settled-execution evidence must block rescheduling (Tasks 1, 7).
5. Workspace/executable/policy changes during cooldown must invalidate recovery consent rather than run under changed context (Tasks 7, 11).

---

## Execution order and file ownership

Keep a single dependency-ordered plan because each subsystem depends on the same lifecycle and persistence contracts. Milestone gates yield testable artifacts: core library (1–5), simulated supervisor (6–10), safe CLI and provider adapters (11–13), observation packages (14–15), operational release candidate (16–18). Do not split the core into independently evolving interface designs.

After approval, use `using-git-worktrees` to create an isolated implementation worktree based on the committed planning branch. Establish the toolchain and run the then-existing baseline; an absent Go module is an expected greenfield condition, not a passing suite. Do not edit global agent configuration.

| Tasks | Owned files/responsibility | Can run concurrently after dependencies? |
| --- | --- | --- |
| 1–2 | `internal/domain/`, initial `go.mod`, architecture tests | Sequential; shared types |
| 3–5 | `internal/adapters/persistence/`, storage ports | Sequential; transactional contract |
| 6 | `internal/adapters/process/`, fake CLI helper | May overlap 3–5 after Task 2 contracts |
| 7–9 | `internal/application/`, fake clock/provider | Sequential coordinator/scheduler integration |
| 10–11 | IPC, configuration, CLI, composition root | Sequential |
| 12 and 13 | `internal/adapters/agy/` and `internal/adapters/codex/` | Separate implementers after Task 11; root integrates composition/docs |
| 14–15 | Observation application service, hooks, native packages | After core safety gate; no overlapping hook edits |
| 16 | OTel, systemd, daemon lifecycle | Sequential shared composition wiring |
| 17–18 | Real-provider verification and release/docs | Verification may remain blocked; release capability labels must reflect that |

Each implementer receives the spec, this plan, prerequisite interfaces, exact owned paths, and the instruction that others share the codebase: preserve others' changes. Fresh reviewers assess spec compliance and code quality after each task. Root handles shared `go.mod`, composition-root, and compatibility-document integration for parallel provider work. No subagents are dispatched during planning.

## Contract ledger

Use the following names consistently. Domain values are defined in Tasks 1–2; ports in Tasks 3 and 6. Avoid introducing second representations of the same state.

- `domain.Session`: `ID`, `SubmissionKey`, `Provider`, `ProviderSessionID`, `ContextID`, `Mode`, `State`, `Revision`, `Fence`, `Context`, `Consent`, `RetryCount`, `RetryPolicy`, `NextRetryAt`, `DeadlineAt`, `CancelRequested`, `TurnOutcome`, `LastError`, `CreatedAt`, `UpdatedAt`, `LastObservedAt`.
- `domain.ExecutionContext`: executable path, provider version, absolute cwd/workspace identity, profile reference, allowlisted sandbox/approval values and environment names, digest; no secret values.
- `domain.Attempt`: ID/session ID, number, kind, phase, owner epoch, fence, lease deadline, nullable process identity, start/finish times, outcome code. `domain.ProcessIdentity`: PID, boot ID, start ticks.
- `domain.Evidence`: `ExpectedSessionID string`, `ConfirmedSessionID string`, `Settled bool`, `TurnCompleted bool`, `PermissionBlocked bool`, `Failure Failure`, `ObservedAt time.Time`. `Failure`: category, allowlisted code, optional `ResetAt`, optional verified `Scope`.
- `domain.Transition`: expected revision/state, destination, event ID/source, attempt ID/fence, evidence. `domain.Disposition`: destination, turn outcome, blocker and optional next retry. `domain.Consent`: enabled/background flags, revision, approved context digest and times.
- `domain.RetryPolicy`: margin/base/cap, jitter fraction, max retries and deadline duration. `domain.RetryDecision`: eligible time or fixed attention reason. `domain.Scope`: provider plus complete verified dimension map/evidence version; unknown is nil, never inferred.
- `ports.Capabilities`: version, independently evidenced observe/start/resume/autonomous flags and denial/settlement support. `ports.Event`: kind, source, provider/session ID, evidence, time, delivery ID. `ports.Outcome`: evidence and invocation exit code, with no objective-success field.
- `ports.StartRequest`: session ID, attempt, execution context, ephemeral prompt. `ports.ResumeRequest`: session ID, provider session ID, attempt, context; no original prompt. Provider signatures match design section 5.
- `ports.Clock`: `Now() time.Time`, `NewTimer(time.Duration) ports.Timer`; Timer: `C() <-chan time.Time`, `Stop() bool`. `ports.Jitter`: `Float64() float64` in [0,1].
- `ports.NewSession`: validated identity/context/consent/policy plus submission key, never prompt. `ports.ClaimRequest`: session ID, expected revision/consent revision, owner epoch, current time, lease duration, explicit one-shot authorization flag. `ports.AttemptResult`: attempt ID/fence, evidence and disposition.
- Repository errors: `ErrConflict`, `ErrNotFound`, `ErrUnsupported`, `ErrUncertain`, `ErrBusy`; do not leak raw SQL/provider payloads to users.

All times persist as UTC epoch milliseconds; use nullable values where absence matters. Do not serialize zero timestamps as evidence. The plan extends the design's illustrative store interface with explicit lease/reconciliation methods rather than requiring unrestricted updates.

Exact storage signatures for `internal/ports/store.go` (the ledger defines referenced values; `SessionFilter` has optional state/provider/mode and bounded limit/cursor):

```go
type SessionStore interface {
    Create(context.Context, NewSession) (domain.Session, error)
    Get(context.Context, string) (domain.Session, error)
    List(context.Context, SessionFilter) ([]domain.Session, error)
    Apply(context.Context, domain.Transition) (domain.Session, error)
    ClaimStart(context.Context, ClaimRequest) (domain.Attempt, bool, error)
    ClaimDue(context.Context, ClaimRequest) (domain.Attempt, bool, error)
    MarkLaunching(context.Context, domain.Attempt) error
    RecordIdentity(context.Context, domain.Attempt, domain.ProcessIdentity) error
    Renew(context.Context, domain.Attempt, time.Time) error
    FinishAttempt(context.Context, AttemptResult) error
    Reconcile(context.Context, string, time.Time) error
    Cancel(context.Context, string, int64) (domain.Session, error)
    SetConsent(context.Context, string, int64, domain.Consent) (domain.Session, error)
}
```

Test assertions below use Go expressions or named test cases with exact expected outcomes; they are requirements for test bodies, not existing tests. For example Task 1's core regression test must contain the equivalent of:

```go
got, err := domain.Decide(domain.Resuming, domain.Evidence{
    ExpectedSessionID: "session-a", ConfirmedSessionID: "session-a",
    Settled: true, Failure: domain.Failure{Category: domain.Quota},
})
if err != nil || got.State != domain.QuotaExhausted {
    t.Fatalf("settled repeated quota: got %+v, err %v", got, err)
}
```

## Common task verification and commits

For every code task below: write the named test and assertions, run its stated command to observe the expected RED failure, implement the minimal behavior, run it GREEN, refactor without changing behavior, then repeat the focused command. A compile failure for the named missing API is valid initial RED; unrelated setup failures are not. Save evidence of each stage in the task report, without sensitive output.

Before each commit run `gofmt -w` on that task's Go files, `go vet ./...`, `go test ./...`, and `git diff --check`. Stage only the exact task-owned files and use its stated Conventional Commit subject. Tests need no artificial sleep; subprocess/OS tests use bounded synchronization and deadlines. After a milestone, run `go test -race ./...`. Stop and use systematic-debugging on unexpected failures; do not change assertions merely to pass.

### Task 1: Establish the module and lifecycle evidence rules

**Dependencies:** Plan approval and execution worktree. **Acceptance:** valid transitions and conservative outcome classification pass independent of providers.

**Files:** Create `go.mod`, `.gitignore`, `AGENTS.md`, `internal/domain/session.go`, `internal/domain/evidence.go`, `internal/domain/session_test.go`, `tests/architecture_test.go`. Ignore generated `dist/` and local test artifacts, not source fixtures.

**Interfaces:** Produce ledger Session/Evidence/Transition/Disposition types; `domain.Decide(state State, evidence Evidence) (Disposition, error)` and `domain.ValidateTransition(from, to State) error`. Enumerate lifecycle values from the spec and turn outcomes separately.

- [ ] **RED:** Add `TestCompletionRequiresEvidence`, `TestPermissionBlockerWins`, `TestQuotaBeforeRunning`, and table coverage of every allowed/disallowed transition. Assertions: `Decide(Running, Evidence{Settled:true}).State != Completed`; completed-turn + settled + no blocker => Completed; same evidence + PermissionBlocked => NeedsAttention; Resuming + matching nonempty IDs + settled quota => QuotaExhausted; missing/mismatched ID or unsettled quota => NeedsAttention. Assert no field reports objective success. Architecture test rejects provider/SQL/OS adapter imports in core.
- [ ] **Observe RED:** `go test ./internal/domain ./tests -run 'Test(Completion|Permission|Quota|Transition|Architecture)' -v`; expected missing API or failing behavioral assertion. Create only the module/test harness needed to run RED. Module path: `github.com/Diaszano/quota-guardian`; select the oldest currently supported Go release compatible with chosen dependencies and record the tested version, without assuming the local 1.27 toolchain is a floor.
- [ ] **GREEN:** Implement lifecycle and evidence rules, including cancelled/terminal immutability and separate `turn_outcome`; document development checks in AGENTS while preserving the user's RTK reference.
- [ ] **REFACTOR/verify:** Repeat focused command, run common checks; assert architecture test inspects production imports, not fixture imports.
- [ ] **Commit:** `feat: define session lifecycle and completion evidence`.

### Task 2: Define retry policy, consent, and quota scope isolation

**Dependencies:** 1. **Acceptance:** deterministic eligible times; unknown scopes never combine; consent cannot come from observation.

**Files:** Create `internal/domain/retry.go`, `internal/domain/consent.go`, `internal/domain/retry_test.go`, `internal/domain/consent_test.go`; modify `internal/domain/session.go`.

**Interfaces:** `domain.NextRetry(now time.Time, failure Failure, priorResumes int, policy RetryPolicy, jitter float64, deadline time.Time) RetryDecision`; `domain.CanResume(s Session, now time.Time, capsVerified bool, oneShot bool) error`. Define Scope and ExecutionContext from ledger.

- [ ] **RED:** `TestRetryPolicy` asserts no-reset first delay = 90s at jitter 0 (60s base + 30s margin), second = 150s, capped fallback = 3630s, explicit reset 8h away remains reset+30s. Assert fifth exhausted resume budget blocks a sixth; reset after seven-day deadline blocks. Add overflow/NaN/jitter bounds/ambiguous timestamp cases, clock rollback, and `TestUnknownScopesStayIndependent` (different sessions with nil scope never affect each other).
- [ ] **Observe RED:** `go test ./internal/domain -run 'Test(Retry|UnknownScopes|Consent)' -v`; expected missing retry/consent rules.
- [ ] **GREEN:** Implement checked timing, fixed reason codes, consent revision/context binding, cancellation precedence; one-shot authorization permits one due attempt but never enables autonomous consent. Only confirmed quota enters fallback; auth/network failures do not.
- [ ] **REFACTOR/verify:** Repeat focused tests and common checks. Add `TestConsentCannotBeGrantedByObservation` and `TestContextChangeInvalidatesConsent` to the same run.
- [ ] **Commit:** `feat: enforce bounded retry and recovery consent`.

### Task 3: Create private SQLite storage and migrations

**Dependencies:** 2. **Acceptance:** crash-durable schema opens privately and fails closed on incompatible/corrupt state.

**Files:** Create `internal/ports/store.go`, `internal/adapters/persistence/sqlite.go`, `internal/adapters/persistence/migrate.go`, `internal/adapters/persistence/migrations/001_initial.sql`, `internal/adapters/persistence/sqlite_test.go`, `docs/DEPENDENCIES.md`; modify `go.mod`, create `go.sum` as needed.

**Interfaces:** `persistence.Open(ctx context.Context, stateDir string) (*Store, error)`, `(*Store).Close() error`; `ports.SessionStore` exposes design Create/Apply/ClaimDue/FinishAttempt/List plus `Get(ctx,id)`, `ClaimStart(ctx,req)`, `MarkLaunching(ctx,attempt)`, `RecordIdentity(ctx,attempt,identity)`, `Renew(ctx,attempt,until)`, `Reconcile(ctx,epoch,now)`, `Cancel(ctx,id,revision)`, `SetConsent(ctx,id,revision,consent)`. All return errors and typed values, never accept SQL or arbitrary maps.

- [ ] **RED:** `TestOpenPrivateState`, `TestMigrationsFailClosed`: temporary state has mode 0700, database/WAL files 0600, foreign keys on, journal WAL, synchronous FULL; corrupt/newer/checksum-mismatched schema and symlink/foreign-owned directories are rejected. Validate schema includes `turn_outcome`, submission key, revision/fence, all design tables, CHECK/FK/index constraints.
- [ ] **Observe RED:** `go test ./internal/adapters/persistence -run 'Test(Open|Migrations)' -v`; expected missing Open/schema.
- [ ] **GREEN:** Evaluate maintained SQLite drivers against supported Go/Linux targets, WAL/FULL, cancellation, backup, CGO/cross-build burden, security/support status; record official evidence and exact pinned version in DEPENDENCIES. Prefer a maintained pure-Go driver if all gates pass; a failed evaluation is reported before changing the design. Use `database/sql`, bounded 2-second busy timeout, one writer connection, transactional embedded migrations, no implicit destructive repair.
- [ ] **REFACTOR/verify:** Repeat focused tests and common checks; query actual PRAGMAs on the opened connection, not configured string constants.
- [ ] **Commit:** `feat: add private transactional sqlite state`.

### Task 4: Implement atomic attempts and idempotent submissions

**Dependencies:** 3. **Acceptance:** races cannot produce duplicate open attempts, stale completions, or prompt replay.

**Files:** Create `internal/adapters/persistence/sessions.go`, `internal/adapters/persistence/attempts.go`, `internal/adapters/persistence/attempts_test.go`; modify `internal/ports/store.go` only to resolve concrete typed results agreed here.

**Interfaces:** Implement Create/Apply/Get/List/ClaimStart/ClaimDue/MarkLaunching/RecordIdentity/Renew/FinishAttempt/Cancel/SetConsent. `Create` returns the existing session for an existing submission key; it never overwrites its context. All attempt mutations condition on token and expected state; claim returns `(Attempt, bool, error)`.

- [ ] **RED:** `TestConcurrentClaims` runs 32 claimers and asserts exactly one claim/open attempt/count increment. `TestStaleFenceRejected` asserts an old finish/renew changes no row. `TestSubmissionRetryReturnsExisting` asserts one task and no replaced context even with a different repeated payload. Add transaction rollback and cancel/consent-vs-claim race tests.
- [ ] **Observe RED:** `go test -race ./internal/adapters/persistence -run 'Test(Concurrent|Stale|Submission|Rollback|Cancel)' -v`.
- [ ] **GREEN:** Use BEGIN IMMEDIATE, conditional writes, unique indexes, event+state+attempt transaction boundaries, monotonic fencing, nullable identity. Persist retry count at resume claim, not start. Store no prompt or prompt hash; idempotency keys are single-use and bound to the first accepted submission.
- [ ] **REFACTOR/verify:** Repeat focused command/common checks; assert database dump contains none of seeded prompt/token sentinels.
- [ ] **Commit:** `feat: serialize recovery attempts and submissions`.

### Task 5: Reconcile crashes and maintain durable state

**Dependencies:** 4. **Acceptance:** waiting tasks retain timing; all unfinished old-epoch attempts block replay.

**Files:** Create `internal/adapters/persistence/reconcile.go`, `internal/adapters/persistence/maintenance.go`, `internal/adapters/persistence/reconcile_test.go`, `internal/adapters/persistence/maintenance_test.go`.

**Interfaces:** Implement `Reconcile(ctx, epoch string, now time.Time) error`; add `(*Store).Backup(ctx context.Context, destination string) error`, `(*Store).Prune(ctx context.Context, before time.Time) error`.

- [ ] **RED:** `TestRestartPreservesWaiting` asserts reset/count/consent unchanged after reopen. `TestRestartUnfinishedAttemptsUncertain` covers claimed/launching/active, including nil PID and exit-before-commit; all become uncertain/attention with no new claim. `TestExpiredLeaseDoesNotReleaseAttempt` asserts expiry alone still blocks. Backup restores committed WAL data; retention never deletes active/uncertain state.
- [ ] **Observe RED:** `go test ./internal/adapters/persistence -run 'Test(Restart|Expired|Backup|Retention)' -v`.
- [ ] **GREEN:** Reconcile in transactions before readiness; use driver-supported online backup; prune terminal events older than 30 days without deleting session/idempotency identity. Never raw-copy a live database or close uncertain attempts automatically.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; run core milestone race suite `go test -race ./...`.
- [ ] **Commit:** `feat: reconcile interrupted attempts without replay`.

### Task 6: Implement the Linux process runner

**Dependencies:** 2 (can overlap storage work). **Acceptance:** bounded, cancellable execution drains both streams and reaps owned children without signaling stale PIDs.

**Files:** Create `internal/ports/provider.go`, `internal/ports/process.go`, `internal/ports/clock.go`, `internal/adapters/process/runner_linux.go`, `internal/adapters/process/runner_unsupported.go`, `internal/adapters/process/runner_test.go`, `internal/testutil/fakecli/main.go`.

**Interfaces:** Provider/Clock contracts from ledger; `ports.ProcessRunner.Run(ctx context.Context, spec CommandSpec, onStart func(domain.ProcessIdentity) error, onRecord func(StreamRecord) error) (ProcessResult,error)`. CommandSpec contains resolved executable, argv, cwd, approved environment and stdin reader; ProcessResult contains exit code, reaped flag, settlement confidence. StreamRecord identifies stdout/stderr. Non-Linux adapter returns ErrUnsupported.

- [ ] **RED:** `TestDrainBothStreams` floods stderr while returning a stdout result and asserts no deadlock; `TestCancellationReapsGroup`, `TestStalePIDNeverSignalled`, `TestCallbackFailureCancels`, `TestArgumentsRemainLiteral`, and `TestOversizeRecord` assert bounded termination and no shell interpolation. Fake CLI modes: success, quota-reset, quota-unknown, interrupted, invalid-id, no-resume, repeated-quota, auth, soft-denial, blocked-child, flood-output, wrong-id.
- [ ] **Observe RED:** `go test ./internal/adapters/process -run 'Test(Drain|Cancellation|Stale|Callback|Arguments|Oversize)' -v`.
- [ ] **GREEN:** Spawn a process group, call onStart before accepting execution as managed, drain pipes concurrently with 1 MiB record limit and bounded queues, close stdin deterministically, cancel then kill/reap under context deadlines. Prefer held pidfd for direct process signaling where available; never act on a stale persisted PID. Descendant/remote execution uncertainty must remain explicit in ProcessResult.
- [ ] **REFACTOR/verify:** Repeat focused/common checks and `go test -race ./internal/adapters/process`; no unconditional wall-clock sleeps in coordination tests.
- [ ] **Commit:** `feat: supervise bounded cancellable linux processes`.

### Task 7: Coordinate managed starts, resumes, and terminal evidence

**Dependencies:** 4–6. **Acceptance:** persistence precedes execution; repeated quota and permission blockers settle safely.

**Files:** Create `internal/application/coordinator.go`, `internal/application/coordinator_test.go`, `internal/testutil/provider.go`; modify `internal/ports/provider.go` for final request values only.

**Interfaces:** `application.NewCoordinator(store ports.SessionStore, providers map[string]ports.Provider, clock ports.Clock) *Coordinator`; `Start(ctx, req ports.StartRequest) error`, `Resume(ctx, sessionID string, oneShot bool) error`, `Cancel(ctx, sessionID string) error`. Provider map is constructor-injected, never global. Runtime evidence is reconciled once at settlement.

- [ ] **RED:** `TestNoLaunchBeforeCommit`, `TestRepeatedQuotaBeforeRunning`, `TestMissingResumeIdentity`, `TestSoftDenialWins`, `TestContextChangeBlocksResume`. Assert matching explicit ID + reaped/settled quota produces waiting and retains incremented count; requested ID alone, wrong ID, live child, or external execution uncertainty produces attention/open uncertain attempt. Successful turn + permission blocker => attention, no success metric. Inject commit failure and assert zero starts.
- [ ] **Observe RED:** `go test ./internal/application -run 'Test(NoLaunch|RepeatedQuota|MissingResume|SoftDenial|ContextChange)' -v`.
- [ ] **GREEN:** Persist launch intent and capture identity, latch permission/parse blockers, gate capabilities/context immediately before spawn, settle attempts transactionally. On write failure cancel owned work. Start accepts prompt only in memory; resume never receives it. Observed mode always returns ErrUnsupported for takeover.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; add barrier-controlled cancel-vs-spawn test and verify a cancelled request cannot be relaunched.
- [ ] **Commit:** `feat: coordinate evidence-gated managed recovery`.

### Task 8: Add scheduling, consent revocation, and lease renewal

**Dependencies:** 7. **Acceptance:** due work is fair and bounded, independent sessions progress, failed renewal never causes parallel work.

**Files:** Create `internal/application/scheduler.go`, `internal/application/scheduler_test.go`, `internal/testutil/clock.go`, `internal/infrastructure/clock.go`.

**Interfaces:** `application.NewScheduler(store ports.SessionStore, coordinator *Coordinator, clock ports.Clock, jitter ports.Jitter, limit int) *Scheduler`; `Run(ctx context.Context) error`. Store listing supplies due candidates; claims remain the sole authority.

- [ ] **RED:** `TestSchedulerIndependentSessions`, `TestNoEarlyRetry`, `TestClockJump`, `TestLeaseFailureCancelsWithoutReplacement`, `TestConsentRevocation`. Assert max active=4, claim order by due time then creation/ID, no quota API polling, recheck within 30s, renewal at 10s against 30s lease; fifth budget blocks sixth attempt. Advance fake time, never sleep through cooldown.
- [ ] **Observe RED:** `go test -race ./internal/application -run 'Test(Scheduler|NoEarly|ClockJump|LeaseFailure|ConsentRevocation)' -v`.
- [ ] **GREEN:** Schedule only eligible managed sessions; limit workers, avoid zero-delay loops, cancel on failed renewal, recompute after wake. Unknown scope stays session-local; verified multi-window reset uses maximum applicable deadline. Quota-window evidence must be supplied by adapter, never guessed from provider name.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; verify waiting consent-off sessions expose blocker and do not spin.
- [ ] **Commit:** `feat: schedule bounded independent recovery attempts`.

### Task 9: Test daemon ownership and real crash windows

**Dependencies:** 8. **Acceptance:** competing daemon and kill-at-boundary tests prove no duplicate attempt after restart.

**Files:** Create `internal/adapters/persistence/lock_linux.go`, `internal/adapters/persistence/lock_unsupported.go`, `internal/adapters/persistence/lock_test.go`, `internal/application/lifecycle.go`, `tests/crash_recovery_test.go`, `internal/testutil/crashdaemon/main.go`. Unsupported-platform files return ErrUnsupported; they do not implement service guarantees.

**Interfaces:** `persistence.AcquireLock(stateDir string) (io.Closer,error)`; `application.Recover(ctx context.Context, store ports.SessionStore, epoch string, now time.Time) error`. Crash helper exposes test-only synchronization pipes, not production IPC crash endpoints.

- [ ] **RED:** `TestSecondDaemonCannotStart`, `TestCrashWindows`, `TestWaitingSurvivesRebootIdentity`: subprocess barriers at claim, launch intent, spawn, identity recorded, exit-before-commit; kill and reopen DB, assert uncertainty blocks every unfinished attempt while waiting due time survives. Lock descriptor must not leak to children.
- [ ] **Observe RED:** `go test ./tests ./internal/adapters/persistence -run 'Test(SecondDaemon|CrashWindows|WaitingSurvives|Lock)' -v`.
- [ ] **GREEN:** Hold advisory lock for full lifetime, reconcile before scheduling/readiness, model changed boot identity without relying on a physical reboot, and refuse stale-socket cleanup without ownership.
- [ ] **REFACTOR/verify:** Repeat focused/common checks and `go test -race ./...`; report these as simulated reboot/process-crash tests, not real OS reboot certification.
- [ ] **Commit:** `test: enforce daemon crash and ownership invariants`.

### Task 10: Expose protected IPC and bounded output attachment

**Dependencies:** 9. **Acceptance:** local authentication, idempotency, and backpressure hold for the simulated daemon.

**Files:** Create `internal/adapters/ipc/server_linux.go`, `internal/adapters/ipc/server_unsupported.go`, `internal/adapters/ipc/client.go`, `internal/adapters/ipc/protocol.go`, `internal/adapters/ipc/server_test.go`, `internal/application/output.go`, `internal/application/output_test.go`.

**Interfaces:** `ipc.Listen(ctx context.Context, runtimeDir string, handler http.Handler) (net.Listener,error)`, `ipc.NewHandler(service *application.Coordinator, store ports.SessionStore) http.Handler`, `ipc.NewClient(socketPath string) *Client`; typed client Run/Inspect/List/Resume/Cancel/SetConsent/Health methods. `/v1/observations` is added in Task 14.

- [ ] **RED:** `TestPeerUIDRejected`, `TestOversizeRequest`, `TestUnknownControlField`, `TestLostAckIdempotency`, `TestSlowSubscriberDoesNotBlock`. Assert mode 0600/parent0700, limit 64, body1MiB, timeout5s, no TCP, no `/tmp` fallback, same submission key => one process. Test unauthorized UID predicate plus actual peer credential extraction; do not claim a multi-user integration test without it.
- [ ] **Observe RED:** `go test -race ./internal/adapters/ipc ./internal/application -run 'Test(Peer|OversizeRequest|UnknownControl|LostAck|SlowSubscriber)' -v`.
- [ ] **GREEN:** Implement typed HTTP/JSON routes with strict controls; map conflicts to 409, validation to 400, unsupported to 422, overload to 503. Output ring limit 256 records/1MiB per session, attachment reports drops, cancellation is explicit, disconnect is not cancel. Return fixed sanitized errors.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; slow client and malformed request must not impede health or another session.
- [ ] **Commit:** `feat: add private idempotent daemon ipc`.

### Task 11: Add configuration, CLI, and diagnostics

**Dependencies:** 10. **Acceptance:** a user can exercise fake-provider lifecycle through the CLI; unsupported real capabilities are visible and closed.

**Files:** Create `cmd/quota-guardian/main.go`, `cmd/quota-guardian/main_test.go`, `internal/infrastructure/configuration/config.go`, `internal/infrastructure/configuration/config_test.go`, `internal/infrastructure/doctor.go`, `docs/CONFIGURATION.md`; modify composition files only here/root integration thereafter.

**Interfaces:** `configuration.Load(path string) (Config,error)` using standard-library JSON; `run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int`; `infrastructure.Doctor(ctx context.Context, cfg Config) Report`. Config defaults and validation match spec, with absolute provider paths and safe settings references.

- [ ] **RED:** `TestCLICompletionCopy` asserts exact turn-completion text and no objective-success claim; `TestDoctorNoModelCalls`, `TestContextDigestChange`, `TestCLIConsentDefaultOff`, `TestCLICtrlCCancels`, invalid duration/path/permission tests. Explicitly allow `--prompt -` to read caller stdin; supplied prompt argv exposure remains a user choice.
- [ ] **Observe RED:** `go test ./cmd/quota-guardian ./internal/infrastructure/... -run 'Test(CLI|Doctor|Context|Config)' -v`.
- [ ] **GREEN:** Wire run/session/status/doctor/daemon-run commands. Default stdout is human-readable, `--json` returns stable fields including turn outcome and capability evidence. Ctrl-C requests cancellation; transient disconnect never replays. Resolve XDG directories, reject unsafe paths/config fields, never load credentials. Doctor uses versions/help/connectivity checks only.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; `go build ./cmd/quota-guardian`; fake CLI end-to-end with private temporary config/state/runtime directories.
- [ ] **Commit:** `feat: expose safe session controls and diagnostics`.

### Task 12: Implement Antigravity adapter with stdin and denial handling

**Dependencies:** 11; can parallel 13 under separate ownership. **Acceptance:** simulated contracts pass, real recovery remains disabled until runtime gates pass.

**Files:** Create `internal/adapters/agy/provider.go`, `internal/adapters/agy/parser.go`, `internal/adapters/agy/provider_test.go`, `internal/adapters/agy/testdata/contracts.json`, `internal/adapters/agy/testdata/events.jsonl`; modify `docs/PROVIDER_COMPATIBILITY.md` through root integration.

**Interfaces:** `agy.New(runner ports.ProcessRunner, context domain.ExecutionContext, evidence ports.Capabilities) *Provider`; implement ports.Provider; `Parse(stdout, stderr []byte) ([]ports.Event,error)` is fixture helper only, production streaming parser shares the same decoder.

- [ ] **RED:** `TestStreamInput` asserts JSON-encoded `{event:"user",message:{content:prompt}}` on stdin, no prompt in argv, no `-p`, both stream-json options, stdin close after one message. `TestSoftDenialExitZero` asserts denial+SUCCESS+exit0 => PermissionBlocked, not successful disposition. `TestResumeQuotaBeforeInit` tests missing identity => attention. Fuzz malformed/oversized records and literal newlines/quotes in prompts.
- [ ] **Observe RED:** `go test ./internal/adapters/agy -run 'Test(StreamInput|SoftDenial|ResumeQuota|Classification|Capabilities)' -v`.
- [ ] **GREEN:** Implement documented init/step/result events, verified provider-error parsing, separate stderr denial classification, explicit conversation ID resume construction. No guessed exact denial regex: until sanitized version-specific evidence is available, capability remains false. Distinguish synthetic quota fixtures from captured evidence in contracts.json; unknown formats fail closed. EOF/resume/settlement/policy contracts must be proven by Task 17 before autonomous enablement.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; fake executable tests cover all scenario names from Task 6; reject auth/network/tool-text 429 false positives.
- [ ] **Commit:** `feat: add gated antigravity stream adapter`.

### Task 13: Implement Codex adapter and capability evidence

**Dependencies:** 11; can parallel 12. **Acceptance:** explicit thread identity and completion parsing work under simulation without granting unverified runtime capability.

**Files:** Create `internal/adapters/codex/provider.go`, `internal/adapters/codex/parser.go`, `internal/adapters/codex/provider_test.go`, `internal/adapters/codex/testdata/contracts.json`, `internal/adapters/codex/testdata/events.jsonl`; root integrates `cmd/quota-guardian/main.go` and compatibility docs after both provider tasks.

**Interfaces:** `codex.New(runner ports.ProcessRunner, context domain.ExecutionContext, evidence ports.Capabilities) *Provider`; implement ports.Provider and same fixture Parse signature as Task 12, scoped to its own package.

- [ ] **RED:** `TestThreadIdentity`, `TestTurnCompletionNotObjective`, `TestResumeExplicitID`, `TestUnknownVersionFailsClosed`, `TestExternalExecutionUnsettled`. Assert JSONL thread.started ID persisted; turn.completed alone before settled exit cannot complete; mismatched ID blocks; no --last/ephemeral/bypass flags; no original prompt on resume (only the disclosed continuation message if the verified interface requires it); unsupported capability spawns nothing.
- [ ] **Observe RED:** `go test ./internal/adapters/codex -run 'Test(Thread|Turn|Resume|UnknownVersion|External|Classification)' -v`.
- [ ] **GREEN:** Implement versioned `exec --json` and explicit resume with verified stdin transport, structured turn/error events, source-aware classification; no private rollout DB or transcript scanning. A CLI exit does not establish that external daemon work settled; capability remains unavailable until verified.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; both provider packages plus `go test -race ./...` after root integration. Compatibility records keep simulation/runtime status separate.
- [ ] **Commit:** `feat: add gated codex session adapter`.

### Task 14: Support native manual-session observation

**Dependencies:** 12–13 and core milestone race checks. **Acceptance:** verified-format hooks discover sessions without wrappers; no observer can authorize execution.

**Files:** Create `internal/application/observe.go`, `internal/application/observe_test.go`, `internal/adapters/hooks/agy.go`, `internal/adapters/hooks/codex.go`, `internal/adapters/hooks/observer.go`, `internal/adapters/hooks/observer_test.go`; modify `internal/adapters/ipc/protocol.go`, `internal/adapters/ipc/server_linux.go`, `cmd/quota-guardian/main.go`.

**Interfaces:** `(*Coordinator).Observe(ctx context.Context, event ports.Event) error`; `hooks.Handle(ctx context.Context, provider string, in io.Reader, out io.Writer, client ObservationClient) error`, where `ObservationClient.Observe(ctx,event) error` is the only transport permission exposed to hooks.

- [ ] **RED:** `TestManualSessionDiscovered`, `TestObservationCannotTakeOver`, `TestStaleHookCannotReopen`, `TestObserverDaemonDown`, `TestObserverDeadline`, `TestObservationCannotGrantConsent`. Assert supported metadata creates observed mode, fixed neutral response, bounded1MiB/200ms, zero provider calls, no disk spool; transcript/prompt/tool payload ignored.
- [ ] **Observe RED:** `go test ./internal/adapters/hooks ./internal/application -run 'Test(Manual|Observation|StaleHook|Observer)' -v`.
- [ ] **GREEN:** Normalize only verified documented fields; provide native lifecycle observation independently of resume capability. Accept no control fields/permission decisions; add observation IPC route with commit-before-ack and idempotent events. Hook transport failures return provider-neutral success, not agent-context warnings.
- [ ] **REFACTOR/verify:** Repeat focused/common checks, duplicate/out-of-order/unknown-provider fixtures; runtime observation remains separately labelled pending Task 17.
- [ ] **Commit:** `feat: observe native sessions without assuming ownership`.

### Task 15: Package official native plugins safely

**Dependencies:** 14. **Acceptance:** manifests match verified provider schemas and install/remove preserves unrelated settings in isolated tests.

**Files:** Create `plugins/agy/plugin.json`, `plugins/agy/hooks.json`, `plugins/agy/README.md`, `plugins/codex/.codex-plugin/plugin.json`, `plugins/codex/hooks/hooks.json`, `plugins/codex/README.md`, `.agents/plugins/marketplace.json`, `tests/plugin_contract_test.go`. Official current formats require the Codex manifest under `.codex-plugin/` and a marketplace manifest for local installation; keep Antigravity's root `plugin.json` and `hooks.json` as documented.

**Interfaces:** Plugin commands invoke the Task 14 observer only; no recovery worker or automatic consent. Official provider mechanisms own installation; no custom global configuration writer.

- [ ] **RED:** `TestPluginContracts` validates event names, neutral response behavior, command quoting for install paths containing spaces, no bypass/control actions, bounded timeout. `TestPluginUninstallPreservesUnrelatedConfig` uses isolated fixture configurations and verifies unrelated entries byte-for-byte or semantically where the provider rewrites formatting.
- [ ] **Observe RED:** `go test ./tests -run 'TestPlugin' -v`; expected missing packages/contracts.
- [ ] **GREEN:** Fetch pinned/current official schemas, record provenance, produce minimal manifests using official plugin path variables. Document exact version-validated install/trust/uninstall commands; unvalidated installation instructions are marked provisional. Never install into the developer's real CLI configuration as a test.
- [ ] **REFACTOR/verify:** Repeat focused/common checks; execute `agy plugin validate plugins/agy` if its read-only validator supports this path; record actual result. Codex package validation uses its documented mechanism, not an invented validate flag. Real install/uninstall is Task 17.
- [ ] **Commit:** `feat: package native session observation plugins`.

### Task 16: Add telemetry and systemd lifecycle integration

**Dependencies:** 15. **Acceptance:** metrics are bounded, readiness follows recovery, shutdown respects deadlines, and service installation is explicit.

**Files:** Create `internal/ports/telemetry.go`, `internal/infrastructure/observability/telemetry.go`, `internal/infrastructure/observability/telemetry_test.go`, `internal/infrastructure/systemd/service.go`, `internal/infrastructure/systemd/service_test.go`, `packaging/systemd/quota-guardian.service.in`, `tests/systemd_test.go`; modify `cmd/quota-guardian/main.go`, `internal/application/coordinator.go`, `internal/application/scheduler.go`, `go.mod`, `go.sum`.

**Interfaces:** `observability.New(ctx context.Context, cfg Config) (ports.Telemetry, func(context.Context) error,error)`; `systemd.Render(executable string) ([]byte,error)`; `systemd.Install(ctx context.Context, unitDir, executable string) error`; `systemd.Control(ctx context.Context, action string) error` accepts start/stop/status only. Telemetry methods record bounded event enums, never raw maps.

- [ ] **RED:** `TestMetricAttributesBounded`, `TestSecretsNeverLogged`, `TestExporterDisabled`, `TestSystemdUnit`, `TestReadinessAfterReconcile`, `TestShutdownBudget`, `TestInstallerPreservesExistingUnit`. Assert notify/watchdog/service defaults from spec, 10s child grace, total30s stop, 2s telemetry flush; do not emit watchdog health when storage/scheduler is stuck.
- [ ] **Observe RED:** `go test ./internal/infrastructure/... -run 'Test(Metric|Secrets|Exporter|Systemd|Readiness|Shutdown|Installer)' -v`.
- [ ] **GREEN:** Pin official OTel modules, inject instrumentation, no exporter by default; implement notify and watchdog, signal shutdown, explicit user-service installation/control/removal. Add CLI `daemon install|uninstall`; existing unowned unit causes conflict, no silent overwrite or lingering/root operation. Escape systemd executable path syntax rather than shell quoting. Cancellation affects owned work only.
- [ ] **REFACTOR/verify:** Focused/common checks; `go test -tags=systemd ./tests -run TestSystemdLifecycle -v` only in a disposable user manager. Otherwise report skip and do not claim lifecycle runtime verification. `systemd-analyze --user verify` on a rendered test unit checks syntax where available.
- [ ] **Commit:** `feat: instrument daemon and integrate user service lifecycle`.

### Task 17: Verify real-provider capabilities without fabricating support

**Dependencies:** 12–16. **Acceptance:** every shipped capability has an explicit evidence record; missing authorization/evidence leaves it disabled without blocking independent documentation work.

**Files:** Create `tests/provider_runtime_test.go`, `tests/provider_runtime_gate_test.go`, `tests/testdata/runtime/README.md`; modify both provider `testdata/contracts.json`, `docs/PROVIDER_COMPATIBILITY.md`, `plugins/agy/README.md`, `plugins/codex/README.md`.

**Interfaces:** Real tests are build-tagged `provider_runtime`, require `QG_RUNTIME_AUTHORIZED=1`, and use isolated user-approved workspaces/configurations. Runtime capability registry accepts reviewed version-specific records, never enables features from synthetic tests or a user bypass toggle.

- [ ] **RED:** `TestRuntimeGateDefaultsClosed` runs without external access and asserts no real command can invoke a model by default. Add explicit cases for stdin/EOF, same-ID resume, denied permission despite zero exit, observed manual lifecycle, install/remove preservation, and settlement evidence. Naturally occurring quota fixture provenance is separate from an intentionally induced limit.
- [ ] **Observe RED:** `go test ./tests -run TestRuntimeGateDefaultsClosed -v` for the untagged gate harness in `tests/provider_runtime_gate_test.go`. External tests require authorization before execution; a skipped test is not GREEN capability evidence.
- [ ] **GREEN:** Implement harness and provenance records. After explicit approval, run `QG_RUNTIME_AUTHORIZED=1 go test -tags=provider_runtime ./tests -run TestProviderRuntime -v`. Do not intentionally consume a usage quota to test exhaustion. Ask for an authorized naturally captured failure sample if needed, sanitize before storing. Enable each capability only after its own required evidence; observation may pass while autonomous recovery remains unavailable.
- [ ] **REFACTOR/verify:** Ordinary tests/common checks pass even when no provider is installed. Record provider version, OS, date, checks run/skipped, fixture origin, and unresolved gates. Never report runtime coverage from fake binaries.
- [ ] **Commit:** `test: separate provider runtime verification from simulation`.

### Task 18: Document, review, and prepare release artifacts

**Dependencies:** 1–17 artifacts; external tests may be explicitly unavailable. **Acceptance:** reproducible checks/builds and honest support claims; initial-release completion cannot be claimed with unmet required runtime gates.

**Files:** Create `README.md`, `docs/ARCHITECTURE.md`, `docs/INSTALLATION.md`, `docs/SECURITY.md`, `docs/TESTING.md`, `docs/ROADMAP.md`, `CHANGELOG.md`, `Makefile`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`; modify `AGENTS.md`, `docs/CONFIGURATION.md`, `docs/IMPLEMENTATION_PLAN.md`, `docs/PROVIDER_COMPATIBILITY.md`.

**Interfaces:** `make test` => `go test ./...`; `make race` => `go test -race ./...`; `make vet` => `go vet ./...`; `make build` builds `./cmd/quota-guardian` into ignored `dist/`. Version supplied by linker variable in composition root; semantic version release record distinguishes experimental capability gates. No automatic public release during implementation.

- [ ] **RED:** Add `tests/release_contract_test.go`: documented command help matches CLI, build version injected, unsupported platforms fail explicitly, fixtures never include credentials, architecture checks pass. Check generated artifact list/checksums using assertions, not a test that merely mirrors Makefile text.
- [ ] **Observe RED:** `go test ./tests -run 'Test(Release|DocumentedCommands|Version)' -v`.
- [ ] **GREEN:** Write complete English operation/security/uninstall guides, installation commands actually exercised, future public-API-only manual adoption path, turn-vs-objective distinction, simulated/runtime support matrix, and roadmap blockers. CI runs formatting/vet/unit/race with pinned actions and minimal permissions; releases build Linux artifacts/checksums and cross-build other targets labelled runtime-unsupported. No license choice is silently invented; ask maintainer to select/approve an open-source license before public release.
- [ ] **REFACTOR/verify:** `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./cmd/quota-guardian`, `test -z "$(gofmt -l cmd internal tests)"`, `git diff --check`; validate workflow syntax with available tooling. Execute documented local install/uninstall in a disposable directory. Report real integration/systemd skips prominently.
- [ ] **Review/commit:** Request independent spec and security/code review; resolve findings using receiving-code-review and repeat affected tests. Commit `docs: prepare verified installation and release workflow`; use verification-before-completion and finishing-a-development-branch to present integration choices, without unapproved publication or destructive cleanup.

## Coverage and execution gates

| Requirement | Tasks |
| --- | --- |
| Turn completion versus objective; denial precedence | 1, 7, 11–13, 18 |
| Greenfield baseline, independent domain, dependency discipline | 1–3 |
| Atomic transitions, durable leases, crash/reboot waiting | 3–5, 7–9 |
| Safe process lifecycle, cancellation, no replay | 6–11 |
| Repeated quota during RESUMING | 1–2, 4, 7, 12–13 |
| Provider/model/account/window isolation | 2, 8, 12–13 |
| First-release native observation, no autonomous manual takeover | 7, 14–15, 17–18 |
| Protected IPC, consent, private/redacted state | 3–4, 7, 10–11, 16 |
| systemd, OTel, diagnostics | 11, 16–18 |
| Fail-closed version gates and independent evidence levels | 11–15, 17–18 |

Core safety milestone must pass race/crash tests before native packaging starts. Provider runtime blockers cannot be resolved by relaxing assertions or permission policy. Deliver the tested core and verified observation capability even if safe autonomous provider recovery is not yet established; describe that as a limited release candidate, not completion of the full initial-release objective.

Self-review performed during planning: mapped the spec to tasks, checked dependency order/file ownership, aligned type names, included each Review Focus test, and distinguished proposed verification commands from executed tests. Schema/ports are design decisions to implement, not existing code. No implementation command in this plan has been executed during planning.

## Proposed execution strategy

Preserve the user's requested TDD and subagent-driven execution: one implementer per task with fresh spec-compliance and code-quality review, integrating prerequisites before dependants. Parallelize only Task 6 against 3–5 and Tasks 12/13 after their shared contracts settle. Keep one slot available for integration review. Root owns shared files and full-branch review.

After plan approval: invoke worktree setup, then subagent-driven-development (or executing-plans if the user changes the method). Run required baseline checks, complete tasks with recorded RED/GREEN evidence and commits, and pause only for missing real-provider usage authorization, unresolved design-changing evidence, or release approvals. Planning approval alone does not authorize implementation.
