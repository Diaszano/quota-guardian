# Dependencies and Driver Evaluation

This document details third-party dependency evaluations, version pinning rationale, and security/build considerations for Quota Guardian.

## SQLite Driver Evaluation

### Requirements
1. **Target OS & Architecture**: Linux x86_64, aarch64 (systemd user daemon).
2. **Durability & Concurrency**:
   - Must support SQLite WAL mode (`PRAGMA journal_mode = WAL;`).
   - Must support synchronous FULL (`PRAGMA synchronous = FULL;`).
   - Must support foreign key enforcement (`PRAGMA foreign_keys = ON;`).
   - Must support busy timeout configuration (`PRAGMA busy_timeout = 2000;`).
   - Must support single writer serialized execution through `database/sql` pools (`SetMaxOpenConns(1)`).
3. **Build & Operational Burden**:
   - Eliminate CGO dependencies to allow friction-free cross-compilation and statically linked binaries.
   - Robust context cancellation and connection management without CGO runtime crashes or SIGSEGV on goroutine cancellation.
   - Transparent maintenance and security updates.

### Candidates Evaluated

| Driver | CGO Required? | Maintenance / Support | Durability / WAL / FULL | Conclusion |
|---|---|---|---|---|
| `mattn/go-sqlite3` | Yes | Mature, standard reference | Supported | Rejected: Requires C toolchain / libc headers, adds CGO cross-compilation burden, complicates reproducible static builds. |
| `modernc.org/sqlite` | **No** (Pure Go translated via ccgo) | Actively maintained, updated alongside SQLite upstream | Fully supported; passes all SQLite durability and PRAGMA tests | **Selected**: Pinned to `v1.60.1`. Eliminates CGO, fully compliant with `database/sql`, verified WAL and synchronous FULL. |
| `ncruces/go-sqlite3` | No (Wasm/Wasi runtime) | Active, high performance | Supported | Alternative: modernc.org/sqlite was preferred as the widely adopted Go ecosystem standard for pure Go SQLite with no auxiliary wasm binaries. |

### Pinned Selection
- Driver: `modernc.org/sqlite`
- Version: `v1.60.1` (with `modernc.org/libc v1.77.1`)
- Module status: Recorded in `go.mod` and verified in `go.sum`.
