# Configuration

Quota Guardian reads an optional JSON file from `~/.config/quota-guardian/config.json`. Set `QUOTA_GUARDIAN_CONFIG` to use another absolute path. Keep the file owned by your user with mode `0600`; unknown fields and relative executable or settings paths are rejected. Credentials and environment snapshots do not belong in this file.

```json
{
  "state_dir": "/home/alice/.local/state/quota-guardian",
  "runtime_dir": "/run/user/1000",
  "providers": {
    "agy": {
      "executable": "/home/alice/.local/bin/agy",
      "workspace": "/home/alice/project",
      "version": ""
    },
    "codex": {
      "executable": "/home/alice/.local/bin/codex",
      "workspace": "/home/alice/project",
      "version": ""
    }
  }
}
```

Omitted state paths resolve under `$XDG_STATE_HOME` (default `~/.local/state`). The runtime path uses `$XDG_RUNTIME_DIR`; when it is unavailable, the daemon uses a private directory under `~/.cache/quota-guardian/run`. State and runtime directories must be owner-only (`0700`), and the control socket is created at `quota-guardian/control.sock` with mode `0600`.

Recovery consent is off by default. `run --auto-resume` explicitly enables background recovery for that task; it remains bound to the configured execution-context digest. `doctor` only inspects configuration and executable files. It never invokes a provider or makes model requests, and reports runtime capabilities as unavailable until their evidence has been reviewed.
