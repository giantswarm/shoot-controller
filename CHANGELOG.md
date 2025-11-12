# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Add the delete verb in the RBAC rules for secrets. The controller needs delete permission on secrets because it's setting an owner reference with `controller: true` on the secret.

## [0.3.0] - 2025-11-12

### Added

- Create OpenAI API key secret in the reconciled Cluster namespace. This secret will be used by shoot to talk to OpenAI API.

## [0.2.1] - 2025-11-11

### Changed

- Avoid creating namespace, the org-namespace should already be existing.

## [0.2.0] - 2025-11-11

### Changed

- Use serviceaccount to install shoot on the MC itself, not on the Workload Cluster.

## [0.1.0] - 2025-11-11

### Added

- Initial implementation.

[Unreleased]: https://github.com/giantswarm/shoot-controller/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/giantswarm/shoot-controller/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/giantswarm/shoot-controller/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/giantswarm/shoot-controller/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/shoot-controller/releases/tag/v0.1.0
