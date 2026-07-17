# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries stay terse — see the [README](README.md) and the package documentation
on [pkg.go.dev](https://pkg.go.dev/github.com/Eyevinn/go-608) for detail.

## [Unreleased]

### Added

- Repository scaffolding: module layout, build (`Makefile`), CI (go, coverage,
  golangci-lint, govulncheck), pre-commit, and `internal` version stamping.
- `cta608` wire boundary: the public `Token` sum type (`Chars`, `PAC`,
  `MidRow`, `TabOffset`, `BackgroundAttr`, `SetMode`, `Command`) plus the
  `Screen`/`Row`/`Run`/`Pen`/`Color`/`Mode` value types, and `Serialize`/`Parse`
  owning odd parity, control-code doubling, two-per-pair packing, null-pair
  frame alignment, and extended-char backspace-and-replace. Adds `DemuxField`/
  `MuxField` and raw `cc_data` round-trip test vectors.
