# Changelog

## [0.7.0](https://github.com/e6qu/zzira/compare/v0.6.0...v0.7.0) (2026-09-03)


### Features

* complete Jira-style issue navigator ([725d13c](https://github.com/e6qu/zzira/commit/725d13c43b07956ce736d097f02f59c76647ba79))

## [0.6.0](https://github.com/e6qu/zzira/compare/v0.5.0...v0.6.0) (2026-09-03)


### Features

* redesign Jira workspace and harden accessibility ([b75afbf](https://github.com/e6qu/zzira/commit/b75afbf3172dbe4461385e81775f0e057fa307f3))


### Bug Fixes

* address CodeQL review findings ([7380386](https://github.com/e6qu/zzira/commit/7380386314c9ebbd73f240138d47e29a1faf5d4d))
* harden security, sync, and offline reliability ([84d1a5a](https://github.com/e6qu/zzira/commit/84d1a5a30ffea09709891f1df7b4b3363345cf34))

## [0.5.0](https://github.com/e6qu/zzira/compare/v0.4.0...v0.5.0) (2026-09-01)


### Features

* publish a live monitoring observation ([b3dd9ac](https://github.com/e6qu/zzira/commit/b3dd9ace74d277318b28e384ad7c61ef0aede9ae))
* render the shauth identity contract markers ([b2a73aa](https://github.com/e6qu/zzira/commit/b2a73aaf0cef0409eeeba192c3c6f529ab1ad3d4))


### Bug Fixes

* revoke sessions by the sid a back-channel logout token actually names ([056c980](https://github.com/e6qu/zzira/commit/056c980c4ace75e92045a39f32dace1e90a57870))
* the post-logout redirect bridge must redirect, not render ([be40840](https://github.com/e6qu/zzira/commit/be408402fde2632f81151699c55ac98026f7707f))

## [0.4.0](https://github.com/e6qu/zzira/compare/v0.3.0...v0.4.0) (2026-09-01)


### Features

* auto-provision an admin for ZZIRA_BOOTSTRAP_ADMIN_EMAIL ([5ce494e](https://github.com/e6qu/zzira/commit/5ce494e1ac9221656748ae9cc2875dc85b2b1c86))

## [0.3.0](https://github.com/e6qu/zzira/compare/v0.2.4...v0.3.0) (2026-09-01)


### Features

* add /auth/validation and OIDC back-channel logout ([19a82f7](https://github.com/e6qu/zzira/commit/19a82f7350f8ddd1b6043de95a3603f7759830dc))

## [0.2.4](https://github.com/e6qu/zzira/compare/v0.2.3...v0.2.4) (2026-08-31)


### Bug Fixes

* scope user lookups to workspace members ([025983a](https://github.com/e6qu/zzira/commit/025983acbe81deae3abdf7823db845bb0a63f1f4))

## [0.2.3](https://github.com/e6qu/zzira/compare/v0.2.2...v0.2.3) (2026-08-31)


### Bug Fixes

* avoid logging configured workspace input ([bfafe73](https://github.com/e6qu/zzira/commit/bfafe7311cd290357ce01cfc4aa3f5ab318dae47))
* bind sync to configured workspace ([f19377a](https://github.com/e6qu/zzira/commit/f19377aa45af687f42b5921aa0118ed9afbbed9d))
* complete explicit workspace configuration ([16fa205](https://github.com/e6qu/zzira/commit/16fa205b52e1d387b8b821453dc79f9bd7dd2954))
* require explicit serving workspace ([ea3d80d](https://github.com/e6qu/zzira/commit/ea3d80d43fbec763a363bdc2d6256521da621753))

## [0.2.2](https://github.com/e6qu/zzira/compare/v0.2.1...v0.2.2) (2026-08-31)


### Bug Fixes

* isolate webhook delivery by workspace ([13650d9](https://github.com/e6qu/zzira/commit/13650d936ac51eb2ce0097b780951a4061d4a67e))

## [0.2.1](https://github.com/e6qu/zzira/compare/v0.2.0...v0.2.1) (2026-08-31)


### Bug Fixes

* enforce agile boundaries and webhook retries ([b5ac745](https://github.com/e6qu/zzira/commit/b5ac74545e56fb97fb27117656e74e25c9800f19))
* harden access boundaries and releases ([1427212](https://github.com/e6qu/zzira/commit/1427212c61dbb8605175763a05a43174453685d1))
* protect browser mutations and visibility reads ([de91442](https://github.com/e6qu/zzira/commit/de9144242d9d74a94f5536675e08b3a5faa835ff))
* scope filters and control-plane access ([477666d](https://github.com/e6qu/zzira/commit/477666d005deb81df0f835e596d3202a035342de))
* serialize concurrent migrations ([d0e8b84](https://github.com/e6qu/zzira/commit/d0e8b84f5bc8ba5e7370d83166b33dacd6eb4b37))

## [0.2.0](https://github.com/e6qu/zzira/compare/v0.1.0...v0.2.0) (2026-08-31)


### Features

* automate releases and add OIDC SSO ([a486b2e](https://github.com/e6qu/zzira/commit/a486b2e3adb97f99e41a732ce081f1451b89ea66))
* make web UI accessible and themeable ([657e29b](https://github.com/e6qu/zzira/commit/657e29ba2c33e2b0c6e958a4ff50824a9bfeb3e1))


### Bug Fixes

* align navigation and API input handling ([6f42548](https://github.com/e6qu/zzira/commit/6f42548767e498c2640a3da8a9a6203df9936f0b))
* allow online edits before replica sync ([529db98](https://github.com/e6qu/zzira/commit/529db98c4251e79f192c0e4bd71fa627b5af3933))
* bind offline dialog commands before hydration ([f46eebd](https://github.com/e6qu/zzira/commit/f46eebde36d252e6ecadb057fb13a880d058a3b2))
* correct broken log lines from previous commit ([ce16348](https://github.com/e6qu/zzira/commit/ce163486649c7cd01fd6a65bc2866bda98c950a9))
* decode worker message events ([4d766d3](https://github.com/e6qu/zzira/commit/4d766d39722d332a05e27aefbc9754ff963853f7))
* dispatch local sync worker commands ([96f5e93](https://github.com/e6qu/zzira/commit/96f5e93390cf383e77fb310cbd204258aa1b398d))
* harden offline worker startup and scan scope ([5384a72](https://github.com/e6qu/zzira/commit/5384a7277387966c2dd88044296b04bf28ea4777))
* hydrate replica-rendered issue forms ([69b66d2](https://github.com/e6qu/zzira/commit/69b66d24dd1a25e3430e396874df5d840314b37a))
* make immediate offline edits durable ([a4ac6e8](https://github.com/e6qu/zzira/commit/a4ac6e8b2a983f79b2905e263323f96b3f3cce14))
* render offline edit dialog from replica ([eb76b15](https://github.com/e6qu/zzira/commit/eb76b15a71134c0a8034de9f6c4350b1690bea1f))
* retain wasm worker message handler ([8abfe9a](https://github.com/e6qu/zzira/commit/8abfe9a1bbf87f4ea4e18fb10775c782b916c6e9))
* route offline dialog submits through outbox ([8871f45](https://github.com/e6qu/zzira/commit/8871f45eb3e70f8186da31106d9cfac0331e62be))
* scope agile resources to workspace ([b03ff72](https://github.com/e6qu/zzira/commit/b03ff724192e4c545618ba9e4f1d0dd2202dd693))
* scope project selection and surface upload errors ([da8d355](https://github.com/e6qu/zzira/commit/da8d3557b7397fd92afea50c6dc6fb7c8ffd4c62))
* scope replica worker to replica views ([9156734](https://github.com/e6qu/zzira/commit/9156734eef808fe10a2e006225bfdb50a12a9871))
* seed replica from server-rendered issue ([f7926f7](https://github.com/e6qu/zzira/commit/f7926f7588631e6d057c1674f955245329e7d46d))
* stop replica view render feedback loop ([0ea9dc7](https://github.com/e6qu/zzira/commit/0ea9dc7ea4faa5501c1bafad9dd719709287fb5f))
* upgrade go-jose security dependency ([11b19e9](https://github.com/e6qu/zzira/commit/11b19e9d00e323410b941da315571a8a5f97ce8f))
