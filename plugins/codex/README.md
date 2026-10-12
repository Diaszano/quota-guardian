# Quota Guardian for Codex

This plugin observes Codex `SessionStart` metadata through a local command hook. It returns `{}` and does not add model context, grant consent, control a turn, or start/resume a provider. Quota Guardian must be installed and available on `PATH`, and its daemon must be running for observations to be recorded.

### Install in Codex desktop

From the repository root, register the checked-in local marketplace with the Codex CLI:

```sh
codex plugin marketplace add "$PWD"
codex plugin marketplace list
```

Restart the ChatGPT desktop app, open Codex's **Plugins Directory**, select the `quota-guardian` marketplace, and install `quota-guardian-observer`. Review and trust the hook definition when Codex prompts you. Lifecycle hooks are supported for manually installed Codex desktop plugins; they do not run in cloud-orchestrated sessions.

To uninstall the plugin, remove it from Codex's Plugins Directory. To remove the registered marketplace source from the CLI afterward, run:

```sh
codex plugin marketplace remove quota-guardian
```

Codex owns its plugin configuration and removal; Quota Guardian does not rewrite that configuration or unrelated entries. The manifest, marketplace, and hook paths follow the [Codex plugin documentation](https://developers.openai.com/plugins/build/plugins). The CLI command registers the marketplace; plugin installation and live hook delivery have not been tested in this repository.
