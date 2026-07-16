# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.2.2] - 2026-07-16

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.6.1, which adds the `Defaulter`
  hook (before-validation defaulting on writes) and caches the per-write schema check.

## [0.2.1] - 2026-07-09

### Changed

- Bump the `github.com/dgraph-io/dgdao` dependency to v0.5.4, which pins Dgraph to the
  released v25.3.8 tag rather than a pre-release pseudo-version.

## [0.2.0] - 2026-07-08

### Added

- Initial extraction of the schema versioning and migration engine (`migrate`,
  `migrate/migratecli`) from dgdao
  (https://github.com/dgraph-io/dgdao). Includes the revision chain, phased
  resumable migrations, content-checksum immutability, retyping, struct-snapshot
  scaffolding, history, and drift verification.
