# Installation

## Build from source

Install Go 1.26 or newer and check out this repository:

```sh
make test
make build VERSION=0.1.0-rc.1
install -m 0755 dist/quota-guardian "$HOME/.local/bin/quota-guardian"
```

Ensure `$HOME/.local/bin` is on `PATH`. Linux is the only supported runtime platform. macOS artifacts are cross-builds for inspection and are explicitly marked runtime-unsupported.

## Configuration

Create `~/.config/quota-guardian/config.json` with mode `0600`. Set `QUOTA_GUARDIAN_CONFIG` to an alternate absolute path. See [configuration](CONFIGURATION.md) for all fields. Configure only provider executables and workspaces you control. Do not put API keys, prompts, or environment values in the file.

The daemon creates state under `$XDG_STATE_HOME/quota-guardian` (or `~/.local/state/quota-guardian`) and a private runtime directory. State and runtime directories must be owned by the current user and mode `0700`.

## Foreground daemon

For local development, run `quota-guardian daemon run` in a terminal. `quota-guardian status` checks the IPC readiness endpoint. The daemon recovers persisted state before announcing readiness.

## Optional systemd user service

On Linux with a systemd user manager:

```sh
quota-guardian daemon install
systemctl --user enable --now quota-guardian.service
quota-guardian daemon status
```

Install writes only `~/.config/systemd/user/quota-guardian.service`, refuses to overwrite an existing unowned unit, and reloads the user manager. It does not enable lingering or run as root. To remove an enabled service, stop and disable it first, then remove the owned unit:

```sh
systemctl --user disable --now quota-guardian.service
quota-guardian daemon uninstall
```

Uninstall refuses to remove a unit without Quota Guardian's ownership marker. It never edits unrelated units.

## Native observation plugins

Install the provider CLI and Quota Guardian binary first. Follow the official CLI commands in [Antigravity plugin instructions](../plugins/agy/README.md) or [Codex plugin instructions](../plugins/codex/README.md). Review and trust hook definitions in the provider. Hooks require a running daemon and return neutral provider-specific output if it is unavailable. They only discover metadata; they do not take over existing work.

## Uninstall

Stop the daemon or service, disable the systemd unit if enabled, then run `quota-guardian daemon uninstall`. Remove the binary and configuration separately. To preserve records, back up the state directory before deleting it. The application does not delete user state during uninstall.
