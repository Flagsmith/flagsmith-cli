# Changelog

## [2.0.1](https://github.com/Flagsmith/flagsmith-cli/compare/v2.0.0...v2.0.1) (2026-08-21)


### Bug Fixes

* **ci:** Homebrew cask ships no shell completions ([#102](https://github.com/Flagsmith/flagsmith-cli/issues/102)) ([2d55665](https://github.com/Flagsmith/flagsmith-cli/commit/2d55665ea33b016708c62fd9ff53559175852c23))
* **ci:** npm publish fails with `Cannot find module 'yargs'` ([#101](https://github.com/Flagsmith/flagsmith-cli/issues/101)) ([8bf7dfc](https://github.com/Flagsmith/flagsmith-cli/commit/8bf7dfcc201b1b32d01a9897a6036f323dda8b1f))

## [2.0.0](https://github.com/Flagsmith/flagsmith-cli/compare/v2.0.0-beta.3...v2.0.0) (2026-08-21)


### Features

* Add reason, variant to `flagsmith eval` output ([#74](https://github.com/Flagsmith/flagsmith-cli/issues/74)) ([52d67c3](https://github.com/Flagsmith/flagsmith-cli/commit/52d67c3a5b93de9c45cb81a842d5d7af1a6d9cd5))
* Migrate to the new update-flag contract, and `flag update --weight` ([#92](https://github.com/Flagsmith/flagsmith-cli/issues/92)) ([c0a5cec](https://github.com/Flagsmith/flagsmith-cli/commit/c0a5cec5a540c3b194efd60c3bb31a9210066c8a))


### Bug Fixes

* **auth:** Self-hosted instances dead-end with no usable credential ([#86](https://github.com/Flagsmith/flagsmith-cli/issues/86)) ([777b918](https://github.com/Flagsmith/flagsmith-cli/commit/777b9186fa3838a4e2a1748bad97bf41c04f5e93))
* **install:** Warn when another flagsmith on PATH may shadow the install ([#97](https://github.com/Flagsmith/flagsmith-cli/issues/97)) ([9ad9979](https://github.com/Flagsmith/flagsmith-cli/commit/9ad9979e71127f22dfc7b76e6414ae514ad94e75))


### CI

* Add Renovate configuration ([#72](https://github.com/Flagsmith/flagsmith-cli/issues/72)) ([59772ef](https://github.com/Flagsmith/flagsmith-cli/commit/59772ef4c6146d4558d6413888c50223b61efb7f))
* publish a Homebrew cask on release ([#83](https://github.com/Flagsmith/flagsmith-cli/issues/83)) ([86b189b](https://github.com/Flagsmith/flagsmith-cli/commit/86b189b4833cb07b5dab9403effe0ffb4412da59))
* Publish npm packages ([#96](https://github.com/Flagsmith/flagsmith-cli/issues/96)) ([400d4ef](https://github.com/Flagsmith/flagsmith-cli/commit/400d4ef10e94036d0e9ddf6eb73279ff2cd9a4cb))


### Docs

* Publish the command reference to GitHub Pages ([#100](https://github.com/Flagsmith/flagsmith-cli/issues/100)) ([39ae3cd](https://github.com/Flagsmith/flagsmith-cli/commit/39ae3cdc148fbe7c27bf901e9e1c77a29ce0c391))


### Dependency Updates

* update module golang.org/x/net to v0.56.0 [security] ([#88](https://github.com/Flagsmith/flagsmith-cli/issues/88)) ([110158a](https://github.com/Flagsmith/flagsmith-cli/commit/110158ae584035f9d253c98d195af9b2d1e16f31))
* update module golang.org/x/text to v0.39.0 [security] ([#89](https://github.com/Flagsmith/flagsmith-cli/issues/89)) ([0fcd1d8](https://github.com/Flagsmith/flagsmith-cli/commit/0fcd1d8b620d5401c677d2525bae2278c9c0422b))


### Other

* Graduate to a stable 2.0.0 release ([#98](https://github.com/Flagsmith/flagsmith-cli/issues/98)) ([d7fcce9](https://github.com/Flagsmith/flagsmith-cli/commit/d7fcce9a370ab7c98e71bb801620af4226c5dbc7))

## [2.0.0-beta.3](https://github.com/Flagsmith/flagsmith-cli/compare/v2.0.0-beta.2...v2.0.0-beta.3) (2026-08-11)


### Bug Fixes

* **api:** send a default JSON content type ([#80](https://github.com/Flagsmith/flagsmith-cli/issues/80)) ([21ea585](https://github.com/Flagsmith/flagsmith-cli/commit/21ea585b35698df5d21f746231a193b360d9c89c))

## [2.0.0-beta.2](https://github.com/Flagsmith/flagsmith-cli/compare/v2.0.0-beta.1...v2.0.0-beta.2) (2026-07-31)


### Features

* `flagsmith eval` ([#53](https://github.com/Flagsmith/flagsmith-cli/issues/53)) ([00feae6](https://github.com/Flagsmith/flagsmith-cli/commit/00feae65e312b4350560d5fea8a0dff1cf19cfed))

## [2.0.0-beta.1](https://github.com/Flagsmith/flagsmith-cli/compare/v1.1.0...v2.0.0-beta.1) (2026-07-30)


### ⚠ BREAKING CHANGES

* CLI v2 ([#43](https://github.com/Flagsmith/flagsmith-cli/issues/43))

### Features

* `install.ps1` ([#60](https://github.com/Flagsmith/flagsmith-cli/issues/60)) ([8608fb5](https://github.com/Flagsmith/flagsmith-cli/commit/8608fb58c23753de0b024e80a217a7380c2b2916))
* `install.sh` ([#59](https://github.com/Flagsmith/flagsmith-cli/issues/59)) ([9e65a63](https://github.com/Flagsmith/flagsmith-cli/commit/9e65a63a8a3a3ff6144e6bce8d012d5a2a1897f0))
* CLI v2 ([#43](https://github.com/Flagsmith/flagsmith-cli/issues/43)) ([93ecd2d](https://github.com/Flagsmith/flagsmith-cli/commit/93ecd2d8d2fc8ae88ebae49fe34517de4fc997dd))


### CI

* build release binaries with goreleaser ([#55](https://github.com/Flagsmith/flagsmith-cli/issues/55)) ([0647ea1](https://github.com/Flagsmith/flagsmith-cli/commit/0647ea17361277b083306daf57176af825b355d9))
* configure release-please for the 2.0.0 beta line ([#54](https://github.com/Flagsmith/flagsmith-cli/issues/54)) ([7255b9d](https://github.com/Flagsmith/flagsmith-cli/commit/7255b9d0fd032b95c72b8c88b322179a0a6ce1c6))
* publish multi-arch images to ghcr.io ([#56](https://github.com/Flagsmith/flagsmith-cli/issues/56)) ([ebb84d9](https://github.com/Flagsmith/flagsmith-cli/commit/ebb84d9f42de300c8788a5dcc68b85a84edcbd4d))


### Other

* update copyright holder ([#58](https://github.com/Flagsmith/flagsmith-cli/issues/58)) ([c617925](https://github.com/Flagsmith/flagsmith-cli/commit/c6179254e1e86aec067c1a5866e9e60252563bb6))
