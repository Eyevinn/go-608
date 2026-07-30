# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.0.0/) and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries stay terse — see the [README](README.md) and the package documentation
on [pkg.go.dev](https://pkg.go.dev/github.com/Eyevinn/go-608) for detail.

## [Unreleased]

### Added

- `schedule.FlipTiming` (`FlipOnTime`, `FlipAfterBuild`) and `schedule.WithFlipTiming`, plus
  `convert.Options.FlipTiming` internally, and a `-no-preroll` flag on `go608-inject` and
  `go608-extract`. See the Fixed entry above.

### Fixed

- **Captions appeared 0.2–0.5 s later than their cue said.** A pop-on caption is two
  transmissions — a build into non-displayed memory, then the `EOC` that flips it on screen
  — and both drain at one byte pair per frame. The build was transmitted *starting* at the
  cue's time, so the caption only became visible a build later: measured **367 ms** for
  "HELLO" and **433 ms** for "SECOND ONE" at 30 fps (longer text appeared later), halving
  at 60 fps.

  `schedule` now backdates the build so its `EOC` lands on the pushed time —
  `schedule.FlipOnTime`, the new default. A WebVTT → 608 → WebVTT round-trip is now
  frame-exact where it used to slide:

  ```text
  in                     1.000→3.000  3.000→5.000  5.000→6.000
  before (v0.7.0)        1.433→3.467  3.467→5.367  5.367→6.000
  after                  1.000→3.000  3.000→5.000  5.000→6.000
  ```

  The old timing compounded — re-converting the output shifted it again (1.000 → 1.367 →
  1.667); the new default is stable across repeated conversions. Ends were always exact,
  because the clearing `EDM` *is* the visible change. A batch that does not end in an `EOC`
  (a bare `EDM`, a roll-up `CR`) is likewise never backdated.

  This also reconciles the two containers' timing conventions: an SCC entry's timecode is
  when its first pair is **transmitted**, a WebVTT cue's start is when the caption is
  **displayed**, and they differ by exactly the build. So **SCC → WebVTT → SCC now returns
  the original timecodes** (`00:00:01:00`, previously `00:00:01:12`), the only remaining
  asymmetry being the terminating `EDM` that `cue.Compile` appends for a dangling final
  cue. 608 → text is unchanged: it always reported when a decoder actually shows the
  caption.

  Affects `go608-inject` (mp4 and the text → SCC path) and `go608-extract`'s text → SCC
  path. `generate.Generator` and `generate.BuildUnitCues` are unaffected — both already
  pushed build and `EOC` as separate batches, which is the same pre-roll done by hand.
  Pass `-no-preroll` (CLI) or `schedule.WithFlipTiming(schedule.FlipAfterBuild)` to
  restore the v0.7.0 timing.

- **A `Decoder` fed one byte pair at a time decoded differently from one fed a whole
  buffer**, corrupting two 608 constructs that straddle a pair boundary. Every timed path
  feeds pair-by-pair — that is what preserves per-frame timing — so this hit
  `go608-extract`, `go608-info`, and any consumer decoding an mp4 or SCC frame by frame.
  `Decoder.Feed` now carries its parse state across calls.

  - **Extended characters lost their backspace-and-replace**, leaving the fallback
    character behind: `CAFÉ ÀU LAIT` decoded as `CAFEÉ AÀU LAIT`. An extended character is
    transmitted as a fallback char pair followed by the two-byte extended code, so in the
    timed path the two always arrive in different `Feed` calls and the backspace had
    nothing pending to remove. It now emits an explicit `BS`, which is what CTA-608-E
    describes on the wire anyway. **Affects every caption mode, pop-on included** — any
    accented letter, curly quote, `©`, `•` or `«»` in an mp4/SCC → WebVTT/SRT conversion.
  - **Doubled control codes were not collapsed across the boundary.** Harmless for pop-on,
    where a second `EOC`/`EDM`/`ENM` is idempotent, but a doubled roll-up `CR` scrolled the
    window **twice** and dropped a line: a 3-row roll-up decoded as
    `r13="BBB"`, `r14=""`, `r15="CCC"` instead of `AAA`/`BBB`/`CCC`.

  `Parse` is unchanged — it still starts from a fresh state, so whole-buffer parsing and
  `Parse`-then-`Push` callers behave exactly as before.

## [0.7.0] - 2026-07-28

### Added

- **AV1 (`av01`) CTA-608 carriage**, end to end. `carriage.MetadataOBU(ccData)` wraps a
  `cc_data()` payload as a `metadata_itu_t_t35` OBU, `carriage.FrameMetadataOBU(field1,
  field2, ccCount)` is the one-call form mirroring `FrameSEINALU`,
  `carriage.SpliceOBUBeforeFrame(sample, obu)` places it in a sample before the first
  `OBU_FRAME` / `OBU_FRAME_HEADER`, and `carriage.OBUFieldPairs(sample)` reads the pairs
  back. `go608-inject`, `go608-extract`, `go608-info` and `go608-clock` accept `av01`
  input. The `cc_data()` and its T.35/GA94 header are shared with the SEI path unchanged;
  only the envelope and the splice differ.

  The AV1 functions are **parallel to** the SEI ones and take no `Codec` —
  `carriage.Codec` names NAL framing, which AV1 does not have, so it stays two-valued.
  A consumer handling all three codecs needs its own three-value discriminator; that is
  deliberate, since adding a third `Codec` value would have left every existing consumer
  switch compiling while quietly captioning nothing for `av01`.

  Scoped to **non-scalable AV1** (`OperatingPointIdc == 0`): one caption OBU per sample
  is well defined only because a temporal unit shows exactly one frame. A scalable
  `av01` track is rejected rather than guessed at.

  Validated with ffmpeg as an independent decoder (`movie=FILE.mp4[out0+subcc]`): an
  injected av01 file comes back byte-identical to the AVC output of the same injection.

- `generate.WithFlipAtCueStart(next)`: an option for `BuildUnitCues` that puts each cue's
  EOC on the first frame of its own slice and transmits the pop-on build over the
  preceding frames, so a caption is displayed over exactly the interval its content names
  instead of appearing 0.5–0.75 s into it. Trades self-contained units for that accuracy —
  see [Per-unit cues](README.md#per-unit-cues-buildunitcues). `BuildUnitCues` is unchanged
  without the option.

- `carriage.SpliceSEIBeforeVCL(sample, seiNALU, codec)`, plus the supporting
  `carriage.SampleNALUs`, `carriage.PrefixNALUs` and `carriage.IsVCL`: the sample-level
  half of the carriage seam — getting a bare SEI NAL unit into an mp4 sample ahead of
  the picture data, and splitting a sample back into NAL units for `FieldPairs`. This
  was previously `internal/mp4io`, unreachable from outside go-608, so every consumer
  had copied it; livesim2 and moqlivemock can now drop their local versions. A sample
  with no VCL NAL unit gets the SEI appended at the end, which leaves the existing NAL
  order untouched.

### Changed

- mp4ff dependency bumped to v0.55.0, and `carriage` now delegates the SEI wire format to
  it: `SEIMessage` uses `sei.CreateCTA608SEIMessage` (so the T.35/GA94 header is mp4ff's
  `sei.CTA608ITUData`, shared with its AV1 metadata-OBU path) and `NALU` uses
  `avc.CreateSEINalu` / `hevc.CreateSEINalu` for the codec NAL header. go-608's own
  contribution is now `BuildCCData` — the `cc_data()` structure — and nothing below it.
  **The emitted bytes are unchanged**, verified byte-for-byte against the previous
  implementation and now pinned by a golden test; `carriage`'s public API is unchanged too.
  Note that v0.55.0 renames CEA-608 to CTA-608 across mp4ff's `sei` package
  (`sei.ParseCEA608` → `sei.ParseCTA608`, `sei.CEA608sei` → `sei.CTA608sei`, and so on,
  with no aliases), which affects code using mp4ff's `sei` package directly alongside
  go-608.

### Fixed

- **Captions were assigned in decode order**, so any B-frame AVC or HEVC input came out
  permuted in third-party decoders — ffmpeg read `Hello, world!` back as
  `llHeo, ldor! w`, measured identically on both codecs. Payloads are now assigned in
  **presentation order**: the k-th payload rides the k-th displayed frame, on both the
  write and the read side. Samples are still written in decode order, which the container
  requires. The internal round-trip was green throughout, because the read side repeated
  the write side's mistake; the new `testdata/bframes-avc.mp4` and
  `testdata/bframes-hevc.mp4` fixtures are the first with non-zero composition offsets.
  AV1 is unaffected — it reorders inside the bitstream, so its composition offsets are
  always 0 — but the rule is codec-free rather than a special case.

- **Subtitle timing ignored the track's presentation-time origin.** `go608-inject` looked
  the scheduler up by absolute decode time, so a track whose first presentation time was
  not 0 (measured: `start_pts=1024`, 66.7 ms at timescale 15360) had every caption shifted
  by that offset. Media time is now measured from the track origin — the smallest
  presentation time in the file — so subtitle-file `t=0` lands on the first *displayed*
  frame. Edit lists remain unconsulted, as before.

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

[Unreleased]: https://github.com/Eyevinn/go-608/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/Eyevinn/go-608/releases/tag/v0.7.0
[0.6.0]: https://github.com/Eyevinn/go-608/releases/tag/v0.6.0
[0.5.0]: https://github.com/Eyevinn/go-608/releases/tag/v0.5.0
