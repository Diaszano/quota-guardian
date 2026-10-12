# Quota Guardian for Codex

This plugin observes Codex `SessionStart` metadata through the supported command-hook format. It returns `{}` and does not add model context, grant consent, control a turn, or start/resume a provider. Quota Guardian must be installed and available on `PATH`.

From the repository root, register its local marketplace and install the plugin using the official Codex CLI:

```sh
codex plugin marketplace add "$PWD"
codex plugin add quota-guardian-observer@quota-guardian
```

Remove it with `codex plugin remove quota-guardian-observer@quota-guardian`; remove the marketplace separately with `codex plugin marketplace remove quota-guardian` if desired. Codex owns its plugin configuration and removal; Quota Guardian does not rewrite that configuration or unrelated entries. The paths shown above remain correctly quoted when the checkout path contains spaces.

Plugin hooks require local Codex execution and explicit hook trust. They are not deployed to cloud-orchestrated sessions. The manifest, marketplace, and hook paths follow the [Codex hook documentation](https://learn.chatgpt.com/docs/hooks) and [plugin documentation](https://learn.chatgpt.com/docs/plugins). Runtime installation was not run against a real Codex CLI here.
