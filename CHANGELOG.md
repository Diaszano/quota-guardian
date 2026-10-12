# Changelog

## [0.1.0-rc.1](https://github.com/Diaszano/quota-guardian/compare/v0.1.0...v0.1.0-rc.1) (2026-10-12)


### Features

* add gated antigravity stream adapter ([8bdd8fc](https://github.com/Diaszano/quota-guardian/commit/8bdd8fc5b15e3e74fda7699d9b98a087092c0ee1))
* add gated codex session adapter ([c73378b](https://github.com/Diaszano/quota-guardian/commit/c73378b296dd2d6b991b13f49152a945a8933a14))
* add private idempotent daemon ipc ([1e9939a](https://github.com/Diaszano/quota-guardian/commit/1e9939a0aa6ecfc6652a4e5edf6a9025d7bdf418))
* add private transactional sqlite state ([9f0e29c](https://github.com/Diaszano/quota-guardian/commit/9f0e29ce83ebb99b5700159865769cdfca9753c8))
* coordinate evidence-gated managed recovery ([0d3358d](https://github.com/Diaszano/quota-guardian/commit/0d3358d086cf9a71b0857de1fcdbac3b9eeebc17))
* define session lifecycle and completion evidence ([b56c95a](https://github.com/Diaszano/quota-guardian/commit/b56c95add21449179d36a99adfa5fd1f0e4cb361))
* enforce bounded retry and recovery consent ([4558567](https://github.com/Diaszano/quota-guardian/commit/4558567263a6e7bcf26507daa87e4560e4c58c19))
* expose safe session controls and diagnostics ([7d1bb50](https://github.com/Diaszano/quota-guardian/commit/7d1bb50cd046b168c501a01564ecae1ab9cb3ab5))
* instrument daemon and integrate user service lifecycle ([90e6f14](https://github.com/Diaszano/quota-guardian/commit/90e6f14a9d0b8c72890122c40bcaa45a6d092349))
* observe native sessions without assuming ownership ([7f146a8](https://github.com/Diaszano/quota-guardian/commit/7f146a8dd2c71f1f2e49a4def5233df1e8dd7fef))
* package native session observation plugins ([acb2b09](https://github.com/Diaszano/quota-guardian/commit/acb2b094bea1f2799cbeaabc208faeb3e8e0475d))
* reconcile interrupted attempts without replay ([acb8462](https://github.com/Diaszano/quota-guardian/commit/acb8462eb6cbe6d2aacd37fb5fa935198ac0d91f))
* schedule bounded independent recovery attempts ([5e91cd0](https://github.com/Diaszano/quota-guardian/commit/5e91cd0329fc05e68e1249ced9299c5dbabfcfec))
* serialize recovery attempts and submissions ([097f0da](https://github.com/Diaszano/quota-guardian/commit/097f0da545cbf5501f4fd701ec819d15d85f9063))
* supervise bounded cancellable linux processes ([0c3327f](https://github.com/Diaszano/quota-guardian/commit/0c3327f69355871afa383c3dee7a74e79cb93a0f))


### Miscellaneous Chores

* standardize repository governance and releases ([#1](https://github.com/Diaszano/quota-guardian/issues/1)) ([6e2d507](https://github.com/Diaszano/quota-guardian/commit/6e2d5077e22c3c96543ba9846462be5ed4ce37a7))

## Changelog

## Unreleased candidate record

- Added durable managed-turn state, local IPC, explicit consent, scheduling, and bounded output attachment.
- Added simulated Antigravity and Codex adapters with start, resume, denial, quota, and settlement capabilities disabled pending live evidence.
- Added native manual-session observation plugins; observed sessions cannot be taken over.
- Added opt-in bounded OpenTelemetry metrics and explicit Linux systemd user-service packaging.
- Added CI checks and non-publishing cross-platform build artifacts.

These notes originally described the unpublished `0.1.0-rc.1` candidate; no release tag is implied. Release Please will generate the dated `0.1.0-rc.1` entry for the first release PR.

This candidate is not a verified initial release. Runtime gates and a systemd user-manager test remain outstanding. The repository is licensed under the [MIT License](LICENSE).
