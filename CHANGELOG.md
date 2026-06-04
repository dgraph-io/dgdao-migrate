# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Initial extraction of the schema versioning and migration engine (`migrate`,
  `migrate/migratecli`) from the modusGraph fork
  (https://github.com/mlwelles/modusGraph). Includes the revision chain, phased
  resumable migrations, content-checksum immutability, retyping, struct-snapshot
  scaffolding, history, and drift verification.
