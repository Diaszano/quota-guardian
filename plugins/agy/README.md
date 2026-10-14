# Quota Guardian for Antigravity

This plugin reports Antigravity `Stop`, `PreInvocation`, and `PostInvocation` metadata to a running Quota Guardian daemon. It never continues a stopped agent, changes permissions, starts a provider, or resumes a session. Hook output is fixed and neutral; observation failures are ignored.

### Install from the repository

Install the Quota Guardian binary first, ensure `quota-guardian` is on `PATH`, and start the daemon. From the repository root, install the local plugin directory with the Antigravity CLI:

```sh
agy plugin install "$PWD/plugins/agy"
agy plugin list
```

Review `plugins/agy/plugin.json` and `plugins/agy/hooks.json`. Then enable the plugin and confirm its status:

```sh
agy plugin enable quota-guardian-observer
agy plugin list
```

To remove it, run:

```sh
agy plugin uninstall quota-guardian-observer
```

The provider CLI owns its plugin registry and removal; Quota Guardian does not rewrite that configuration or other entries. The quoted path also works when the checkout path contains spaces.

The manifest and hook event shapes follow the [Antigravity plugin documentation](https://antigravity.google/docs/plugins) and [hook documentation](https://antigravity.google/docs/hooks). `agy plugin validate plugins/agy` passed with CLI 1.3.3. Installation and live hook delivery were not run; review the plugin before enabling it.
