# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries stay terse — see the [README](README.md) and the package documentation
on [pkg.go.dev](https://pkg.go.dev/github.com/Eyevinn/go-608) for detail.

## [Unreleased]

## [0.6.0] - 2026-07-22

### Added

- `generate.BuildUnitCues(fps, unitFrames, unitStartMS, targetPeriodMS, content)`:
  the shared per-unit cue helper for segment-oriented consumers (one DASH segment
  or MoQ group per unit). It emits a *self-contained* caption per unit — every
  pop-on build and EOC flip stays inside the unit — split into
  `N = NumCues(unitDurMS, targetPeriodMS)` ≈1 s pop-on cues, so a stateless
  per-segment server can generate a segment's captions from the segment alone. The
  caller formats the lines; go-608 owns the build/flip, `cc_count`, and (via
  `carriage`) the SEI carriage. Adds the supporting `NumCues`, `UnitCue`, and
  `CueContentFunc` public API.
- `BuildUnitCues` returns an error (rather than panicking) when a cue's build does
  not fit its slice, or when `fps` is outside the 23.976–60 broadcast caption range
  (`cc_count` out of 2..31).

## [0.5.0] - 2026-07-21

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
- `cue` package: the shared `TimedCue` timed-text intermediate (its `Content`
  reuses `cta608.Screen`) plus the one 608↔cue mapping — `Segment` (unified
  displayed-Screen-change segmentation, gaps, and a configurable dangling end via
  `SegmentOptions`) and `Compile` (pop-on, overlapping cues merged by position
  with the later cue winning a row conflict, driving the core diff engine to
  `TimedTokens`). Publishes the `Reader`/`Writer` plugin seam over `[]TimedCue`
  for WebVTT/SRT/TTML.
- `webvtt` package: a thin WebVTT serializer over `cue` — `Read`/`Write`
  (`cue.Reader`/`cue.Writer`) mapping WEBVTT text ⇄ `[]cue.TimedCue`, with color
  via `<c.name>` classes + a `STYLE` block (nearest-of-8 quantization in), `<i>`/
  `<u>` (bold dropped), best-effort `bg_` backgrounds, and `line:`/`position:`/
  `align:` ⇄ grid Row/Column (position-less cues anchor bottom-center). Semantic,
  quantized round-trip; imports only `cue`/`cta608`.
- `generate` package: the wall-clock caption `Generator` (first milestone) —
  `NewGenerator`/`NextFrame(frameWallMS)` with `Config`/`LineSpec` (default:
  centered row 14 UTC RFC3339 white, row 15 media time yellow). Pop-on captions
  built ahead into non-displayed memory and flipped with a single `EOC` on the
  second's last frame (frame-accurate, zero-lag), driving `CaptionBlock`/`Encoder`
  and a `schedule.Scheduler`; an `Overran()` guard flags content that can't build
  within the one-second budget.
- `cmd/go608-clock`: the first-milestone wall-clock demo — runs `generate` →
  `carriage` → NAL-splice end to end to emit a fragmented mp4 whose frames carry
  the caption. Synthetic single-track AVC output by default, or `-i` splices the
  caption into every frame of an existing single-video-track fMP4 (AVC/HEVC),
  preserving timing; `-fps`, `-line`, `-start`, and overrun reporting. Adds the
  shared `internal/mp4io` glue (video-track lookup, sample NAL split/prefix, and
  SEI-before-VCL splice) reused by the other mp4 commands.
- `srt` package: a thin SRT (SubRip) serializer over the `cue` model — `Read`/
  `Write` (and the `Reader`/`Writer` seam types) map SRT text ⇄ `[]cue.TimedCue`.
  Inline styling is quantized to 608's 8-color palette (foreground ⇄ `<font
  color>`, `<i>`/`<u>` both ways; `<b>` and background dropped) and, since SRT has
  no positioning, cues render bottom-centered and read back bottom-anchored with no
  `{\anX}` hacks. Imports only `cue`/`cta608`; ships `testdata/srt/` fixtures.
- `cmd/go608-info`: debug dumper for the decode stack — for a fragmented mp4
  (`-i`) or a raw `cc_data` byte-pair stream (`-hex`/`-cc-file`) it prints the
  per-unit field byte pairs, the parsed token stream, and the rendered `Screen`
  at each displayed change, selecting field 1 (default) or 2. Deterministic,
  line-oriented output for greppable debugging; reuses `internal/mp4io` +
  `carriage.FieldPairs` → `cta608.Parse` / `cta608.Decoder`.
- `scc` package: byte-exact Scenarist SCC read/write with true SMPTE drop-frame
  timecodes — `SCCFile`/`Entry`, `Read` (fps/drop-frame inference, `WithFPS`
  override, 29.97 fallback, `;`/`:` accepted) and a dumb verbatim `Write`,
  `FrameToTimecode`/`TimecodeToFrame` (drop 0,1 each minute except every 10th for
  29.97/59.94; non-drop for 25/integer rates), plus `TimedPairs` flatten and the
  `GroupPairs` helper. Imports only `cta608`.
- `cmd/go608-extract` and `cmd/go608-inject`: the decode/encode integration
  capstones. Extract pulls 608 out of a fragmented mp4 (`carriage.FieldPairs` →
  `cta608.Decoder` → `cue.Segment` → the writers); inject splices it back in
  (`cue.Compile` → `schedule` → `carriage` → NAL splice); SCC ↔ mp4 is byte-exact
  (raw wire pairs), WebVTT/SRT are faithful quantized cues. Both expose
  format-only conversion (SCC ⇄ WebVTT ⇄ SRT, no mp4) through one shared core,
  `internal/convert` (`ReadCues`/`WriteCues`/`WriteSCCPairs`/`CuesFromUnits`).
  Adds `internal/dump` (the field-pairs/tokens/`Screen` formatter now shared with
  `go608-info`) and extends `internal/mp4io` with a reusable `SpliceFragmented`
  fragment rewriter (also adopted by `go608-clock`) and a `Samples` flattener.

[Unreleased]: https://github.com/Eyevinn/go-608/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/Eyevinn/go-608/releases/tag/v0.5.0
