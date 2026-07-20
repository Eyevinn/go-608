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
- `cta608` `Encoder` (the single per-channel diff engine) and `CaptionBlock`
  authoring: `SetScreen`/`Apply` diff the current display into a `[]Token` for
  pop-on (RCL/ENM…EOC), roll-up (append + CR scroll, minimal deltas), and
  paint-on (RDC, direct writes); `CaptionBlock`/`Line`/`Anchor`/`Align` compile
  to a target `Screen`, lowering absolute columns to PAC indent + Tab Offset with
  mid-row compensation for centered colored lines.
- `cta608` `Decoder` (the per-channel inverse of `Encoder`): `Feed`/`Push`
  interpret a byte/token stream into the displayed `Screen`, with pop-on double
  buffering (`EOC`/`EDM`), roll-up `CR` scrolling, and paint-on direct writes;
  `Changed()` signals displayed-Screen changes for cue segmentation. XDS is
  dropped and text mode is recognized but not rendered.
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
- `generate` package: the wall-clock caption `Generator` (first milestone) —
  `NewGenerator`/`NextFrame(frameWallMS)` with `Config`/`LineSpec` (default:
  centered row 14 UTC RFC3339 white, row 15 media time yellow). Pop-on captions
  built ahead into non-displayed memory and flipped with a single `EOC` on the
  second's last frame (frame-accurate, zero-lag), driving `CaptionBlock`/`Encoder`
  and a `schedule.Scheduler`; an `Overran()` guard flags content that can't build
  within the one-second budget.
