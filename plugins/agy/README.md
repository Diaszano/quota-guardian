# Quota Guardian for Antigravity

This plugin reports Antigravity `Stop`, `PreInvocation`, and `PostInvocation` metadata to a running Quota Guardian daemon. It never continues a stopped agent, changes permissions, starts a provider, or resumes a session. Hook output is fixed and neutral; observation failures are ignored.

Install the Quota Guardian binary first and ensure `quota-guardian` is on `PATH`. Then install this local plugin with the official Antigravity CLI:

```sh
agy plugin install "$PWD/plugins/agy"
agy plugin list
```

Remove it with `agy plugin uninstall quota-guardian-observer`. The provider CLI owns its plugin registry and removal; Quota Guardian does not rewrite that configuration or other entries. The paths shown above remain correctly quoted when the checkout path contains spaces.

The manifest and hook event shapes follow the [Antigravity plugin documentation](https://antigravity.google/docs/plugins) and [hook documentation](https://antigravity.google/docs/hooks). `agy plugin validate plugins/agy` passed with installed CLI 1.3.3. Installation and live hook delivery were not run; verify the installed version and review the plugin before enabling it.
