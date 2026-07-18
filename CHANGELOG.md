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
- `carriage` package: `cc_data` / T.35 / SEI / NAL carriage for AVC & HEVC —
  `BuildCCData`, `SEIMessage`, `NALU`, `FrameSEINALU`, `FieldPairs`, and the
  `Codec` enum, wrapping mp4ff. Ships a fragmented-mp4 `testdata/` fixture.
- `schedule` package: the shared timing layer mapping wall-time-tagged token
  transitions onto per-frame `{Field1, Field2, CCCount}` triples —
  `NewScheduler`, `Push`, `Frame`, the `TimedTokens` input, and the
  `CCCountPolicy` (full per-rate `round(600/fps)` with DTVCC padding by default
  vs. minimal). Owns the one-pair-per-field-per-frame cadence, the 608 rate cap
  (one 608 pair per frame above 30 fps), and frame alignment. Carriage-free
  (imports only `cta608`).
