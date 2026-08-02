# go-608 — Design Specification

**Module:** `github.com/Eyevinn/go-608` · **Status:** design spec, ready for `/implement` · **License:** MIT · **Go:** 1.25

A generic Go library for **CTA-608 / CEA-608** captions: encode + decode, `cc_data` carriage per
ATSC A/53 — SEI for AVC & HEVC, a `metadata_itu_t_t35` OBU for AV1 — wall-clock caption generation,
and timed-text (SCC / WebVTT / SRT) I/O.

This document is the **consolidated, self-contained hand-off** for implementation. It reproduces the
public API and the normative build requirements in-line; the per-decision **rationale** lives in the
design notes under [`docs/design/`](docs/design/) and the source research under
[`docs/research/`](docs/research/), which this spec cites but does not restate. Conformance language
(**MUST / SHOULD / MAY**) follows RFC 2119.

The naming: the modern standard is **CTA-608** (the org renamed from CEA to CTA); the core package is
`cta608`. The legacy spelling "CEA-608" persists in prior art, and used to persist in the mp4ff
dependency's API; as of mp4ff v0.55.0 that API is `ParseCTA608` / `IsCTA608`, so go-608 uses the
modern spelling throughout.

---

## 1. Overview & scope

### 1.1 Goals

- **Encode + decode** CTA-608 caption data, encode-first (authoring → byte pairs is the primary path).
- **Carriage** of `cc_data()` under the same T.35/GA94 payload in every supported codec — a
  `user_data_registered_itu_t_t35` SEI message for AVC/HEVC, a `metadata_itu_t_t35` OBU for AV1 —
  reusing `Eyevinn/mp4ff` for the SEI/NAL and OBU layers.
- **Wall-clock caption generation** — the first milestone, consumed by livesim2 & moqlivemock.
- **Timed-text I/O** — SCC (byte-exact) and WebVTT/SRT (lossy, via a shared cue model), with a
  published plugin seam for future formats (TTML).

### 1.2 Destination & posture

This is a **library** of cooperating packages plus small CLI utilities. The two production consumers
(livesim2, moqlivemock) use the library directly and own access-unit assembly themselves — go-608
hands them bytes to insert, it never muxes ([#4](docs/research/consumer-injection-points.md)).

### 1.3 Out of scope

- **Native CEA-708 / DTVCC** caption authoring & rendering — only 608-carried-via-708 `cc_data`.
- **XDS** (eXtended Data Service, field-2 metadata) — decode skips it, encode never emits it.
- **Text mode (T1–T4)** — decode recognizes the mode switch but does not model/render it.
- **Code changes in livesim2 / moqlivemock** — a separate implementation effort.
- **Scalable AV1** (`OperatingPointIdc != 0`) — see §5.2; a scalable `av01` track is rejected, not
  captioned. **Non-MP4 AV1 containers** — WebM/Matroska and raw `.obu` / `.ivf` elementary streams;
  AV1 carriage here means av01-in-MP4.

---

## 2. Domain model & glossary

Full glossary: [`CONTEXT.md`](CONTEXT.md). Core-model rationale: [#5](docs/design/cea608-core-model.md).

- **Token stream** — the canonical in-memory form: an ordered sequence of typed 608 commands +
  character data. The **spine** both encode and decode pivot on; serializes to/from `cc_data` byte
  pairs. `Token` is a public sum type.
- **Byte pair** — the on-the-wire unit: two 8-bit values (each odd-parity), one control command or up
  to two characters.
- **Screen** — the **sparse** rendered display state: a set of **Rows** (not a 15×32 grid). Derived
  from the token stream on decode; diffed against to produce tokens on encode. **Bidirectional.**
- **Row** — a row index (1–15), a `Displayed` flag, and a sequence of character-runs.
- **Character-run** — a maximal contiguous span sharing one **Pen**.
- **Pen** — styling: foreground `Color`, `Italic`, `Underline`, optional background `Color`. A small
  **comparable value** (`==` works; background is a sentinel `Color`, not a pointer).
- **Caption mode** — per-channel state: **pop-on**, **roll-up-N** (N∈{2,3,4}), **paint-on**. Not a
  property of `Screen`/`Row`.
- **Displayed / non-displayed memory** — 608's double buffer, modeled as the `Displayed` flag on each
  Row (pop-on builds `Displayed=false`, `EOC` promotes).
- **Timed cue / cue list** — the timed-text pivot: `TimedCue{Start,End,Content}` where `Content` **is**
  a `Screen`. WebVTT/SRT are thin serializers of a cue list.

**Model invariants** ([#5](docs/design/cea608-core-model.md)):

1. The token stream is the spine; the `Screen` is a **derived** artifact, materialized only when a
   consumer needs rendered output. Everything is **sparse** (a few lines, never a full grid).
2. The display state is used in **both** directions: decode builds it, encode diffs against it. Diff
   granularity bottoms out at the **character-run within a row**.
3. The token layer is **wire-faithful** (one token ≈ one wire command) so `[]Token` round-trips
   SCC/bytes exactly; the `Screen` is the **lossy** view (absolute column resolved from PAC indent +
   Tab Offset; the raw indent/tab split is not preserved).
4. A core instance is **per-channel**; a thin field layer mux/demuxes the two in-field channels.
5. **Wire concerns and timing are kept out of the pure core** (see §5, §7).

---

## 3. Package architecture

Top-level packages (mp4ff-style). Module `github.com/Eyevinn/go-608`; **no importable root package**
(the last path element `go-608` is not a valid Go identifier — an optional doc-only `package go608`
`doc.go` may provide a godoc landing). Rationale: [#10](docs/design/package-layout.md).

```
cta608/     pure core — tokens, Screen, CaptionBlock, Serialize/Parse, Decoder/Encoder,
            field mux/demux, unexported char/PAC/parity/roll-up tables      deps: — (leaf)
scc/        SCC (Scenarist) read/write — byte-pair container                deps: cta608
cue/        TimedCue + Reader/Writer seam + 608↔cue mapping (Segment/Compile) deps: cta608
webvtt/     WebVTT serializer  (implements cue.Reader/Writer)               deps: cue, cta608
srt/        SRT serializer     (implements cue.Reader/Writer)               deps: cue, cta608
carriage/   cc_data / T.35; SEI+NAL for AVC/HEVC, metadata OBU for AV1       deps: mp4ff
schedule/   timed tokens → per-frame {field1,field2,ccCount}                deps: cta608
generate/   wall-clock Generator                                           deps: cta608, schedule
cmd/        go608-extract · go608-inject · go608-clock · go608-info
internal/   version stamping + shared cmd/mp4 glue
examples/   testdata/
```

**Dependency rules (MUST):**

- `cta608` is a pure leaf — **no heavy dependencies**. Char/PAC/parity/roll-up tables are
  **unexported inside `cta608`**, not a separate package.
- **`carriage` is the only package that imports mp4ff.** It MUST NOT leak into the timing layer.
- `schedule` and `generate` MUST be **format-agnostic** (no import of `cue`/`webvtt`/`srt`) and
  **carriage-free**: they emit the primitive `{field1, field2, ccCount}` triple; `carriage` consumes
  it. `carriage` ⟂ `schedule`/`generate` are **siblings**, joined by the caller. This keeps mp4ff out
  of the timing layer.
- The 608↔cue mapping lives **once** in `cue`; `webvtt`/`srt` only serialize a cue list ↔ file.

```
cta608 (pure) ─┬─ scc
               ├─ cue ─┬─ webvtt
               │       └─ srt
               ├─ carriage (+ mp4ff)   ← only mp4ff importer
               └─ schedule ── generate
```

---

## 4. Public API reference

Signatures are the agreed shape; exact field lists / option structs are finalized at implementation.

### 4.1 `cta608` — pure core ([#5](docs/design/cea608-core-model.md))

```go
// Styling — comparable value type
type Color uint8
const ( ColDefault Color = iota // fg renders white; bg = none
        White; Green; Blue; Cyan; Red; Yellow; Magenta; Black; Transparent )
type Pen struct { Color Color; Italic, Underline bool; Background Color } // == works

// Rendered display: sparse, derived, bidirectional
type Run    struct { Column int; Text string; Pen Pen }        // Column 0..31 absolute
type Row    struct { Index int; Displayed bool; Runs []Run }   // Index 1..15
type Screen struct { Rows []Row }                              // only non-empty rows

// Token stream: public, wire-faithful spine
type Mode uint8
const ( PopOn Mode = iota; RollUp; PaintOn )                   // text mode not modeled
type Token interface { token() }
type Chars          struct { Text string }
type PAC            struct { Row, Indent int; Pen Pen }
type MidRow         struct { Pen Pen }
type TabOffset      struct { Columns int }                     // 1..3
type BackgroundAttr struct { Pen Pen }
type SetMode        struct { Mode Mode; RollUpRows int }       // RCL / RU2-4 / RDC
type Command        struct { Op Op }                           // EOC,EDM,ENM,CR,BS,DER,…

// Wire boundary — parity / doubling / 2-per-pair packing / null frame-alignment all here (§5)
func Serialize(tokens []Token, opts SerializeOptions) []byte   // opts: doubling per field
func Parse(data []byte, opts ParseOptions) ([]Token, error)    // opts: parity validate vs strip

// Decode: tokens -> rendered Screen (stateful, per channel)
type Decoder struct { /* current Screen, mode, pen, cursor */ }
func (d *Decoder) Feed(data []byte) error       // Parse + interpret
func (d *Decoder) Push(tokens []Token)          // interpret only
func (d *Decoder) Screen() Screen               // current displayed rows

// Encode: target Screen -> tokens (stateful, per channel — the one diff engine)
type Encoder struct { /* current Screen, mode */ }
func (e *Encoder) SetScreen(target Screen) []Token   // diff current->target
func (e *Encoder) Apply(b CaptionBlock) []Token      // block -> target Screen -> diff

// Friendly authoring — compiles to a target Screen
type CaptionBlock struct { Lines []Line; Anchor Anchor; Mode Mode; RollUpRows int }
func (b CaptionBlock) Screen() Screen

// Thin field mux/demux — the two in-field channels; XDS skipped, no text mode
func DemuxField(fieldBytes []byte, opts ParseOptions) (ch1, ch2 []Token, err error)
func MuxField(ch1, ch2 []Token, opts SerializeOptions) []byte
```

### 4.2 `carriage` — `cc_data` carriage, wraps mp4ff ([#6](docs/design/cea608-carriage-seam.md))

```go
type Codec int
const ( CodecAVC Codec = iota; CodecHEVC )      // NAL framing only; explicit, never sniffed

// --- codec-free ---
// Encode (pure, timing-free; ccCount supplied by schedule). A field pair is 0 or 2 bytes.
func BuildCCData(field1Pair, field2Pair []byte, ccCount int) []byte

// --- AVC / HEVC: SEI message in a NAL unit ---
// Wrap cc_data as a T.35/GA94 SEI message (codec-identical for AVC/HEVC; no codec here).
func SEIMessage(ccData []byte) sei.SEIMessage
// Serialize one or more SEI messages into a BARE codec NAL (no 4-byte length prefix).
func NALU(codec Codec, msgs ...sei.SEIMessage) []byte
func FrameSEINALU(field1Pair, field2Pair []byte, ccCount int, codec Codec) []byte // one-call
// Sample level: split/join the 4-byte-length-prefixed NAL framing, and splice the SEI
// before the first VCL NAL (appended at the end if the sample has none).
func SampleNALUs(sample []byte) ([][]byte, error)
func PrefixNALUs(nalus ...[]byte) []byte
func IsVCL(nalu []byte, codec Codec) bool
func SpliceSEIBeforeVCL(sample, seiNALU []byte, codec Codec) ([]byte, error)
// Decode (thin wrapper over mp4ff sei.ParseCTA608): sample NALUs -> field byte-pair streams.
func FieldPairs(sampleNALUs [][]byte, codec Codec) (field1, field2 []byte, err error)

// --- AV1: metadata_itu_t_t35 OBU. Parallel surface; none takes a Codec (§5.2, §6) ---
// Wrap cc_data as a complete metadata OBU (header, obu_size, metadata_type, payload,
// trailing_bits). Self-framing, so this returns wire bytes rather than a message value.
func MetadataOBU(ccData []byte) []byte
func FrameMetadataOBU(field1Pair, field2Pair []byte, ccCount int) []byte  // one-call
// Sample level: splice before the first OBU_FRAME / OBU_FRAME_HEADER. Errors if the
// sample has neither — deliberately NO no-anchor fallback (§5.2).
func SpliceOBUBeforeFrame(sample, obu []byte) ([]byte, error)
// Decode: takes the raw sample, not a pre-split OBU list.
func OBUFieldPairs(sample []byte) (field1, field2 []byte, err error)
```

`Codec` is **two-valued and stays that way**: it names NAL framing, which AV1 does not have. The AV1
functions therefore run **parallel** to the SEI ones rather than extending them, and none takes a
`Codec`. A consumer handling all three codecs MUST own its own three-value discriminator — the point
being that it fails to compile rather than compiling into a switch that silently captions nothing for
`av01`. `BuildCCData` and the T.35/GA94 payload are shared by both envelopes unchanged.

### 4.3 `schedule` — timed tokens → frames ([#7](docs/design/cea608-wallclock-generation.md)/§5.3)

```go
type Frame struct { Field1, Field2 []byte; CCCount int } // per video frame; feed to carriage
// FlipTiming: what a pushed batch's TimeMS means for a pop-on transition (one ending in EOC).
// FlipOnTime (default) = the instant the caption becomes VISIBLE (the build is backdated so its
// EOC lands on TimeMS); FlipAfterBuild = the instant transmission STARTS (the pre-v0.8.0
// behaviour, ~0.2-0.5 s late). A batch with no EOC is the visible change itself, never moved.
type FlipTiming int
const ( FlipOnTime FlipTiming = iota; FlipAfterBuild )
func WithFlipTiming(t FlipTiming) Option
type Scheduler struct { /* fps, cc_count policy, per-field pair queues */ }
func NewScheduler(fps float64, opts ...Option) *Scheduler
// Enqueue wall-time-tagged token transitions (from generate or cue.Compile).
func (s *Scheduler) Push(t TimedTokens)
// Emit the frame at the given wall-clock time (drains ≤1 pair/field/frame, pads to cc_count).
func (s *Scheduler) Frame(frameWallMS int64) Frame
```

### 4.4 `generate` — wall-clock generator ([#7](docs/design/cea608-wallclock-generation.md))

```go
type LineSpec struct { Row int; Color string; Kind string } // Kind: "utc" | "media" | …
type Config   struct { Lines []LineSpec }                   // default: row14 utc white, row15 media yellow
type Generator struct { /* fps, build/displayed state, drives a schedule.Scheduler */ }
func NewGenerator(fps float64, cfg Config, opts ...GeneratorOption) *Generator
// Advance one frame; returns the per-frame triple (via the internal scheduler). The caller wraps
// with carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec) and splices.
func (g *Generator) NextFrame(frameWallMS int64) schedule.Frame
// Paint-on instead of pop-on: each second opens with an EDM + RDC and the caption is written
// onto the DISPLAYED screen, so the one-pair-per-frame cadence is the animation (2 chars/frame).
func WithPaintOn() GeneratorOption
// Roll-up in a 2..4 row window: no clear — each second is a CR (scroll) plus its typed lines,
// and the rows above the base row hold the previous seconds (the decoder's, never resent).
func WithRollUp(rows int) GeneratorOption

// Per-unit cues — the segment-oriented counterpart (one call per DASH segment / MoQ group).
// Unit's three fields are INDEPENDENT: a unit's start is not assumed to be Nr x duration
// (durations vary, timelines have gaps, numbering need not start at t=0). Nothing derives
// one from the other — StartMS is the only timing input, Nr is passed through to content.
type Unit    struct { Nr int64; StartMS int64; Frames int }
type UnitCue struct { Lines []cta608.Line }   // empty Lines clears (EDM)
type CueContentFunc func(u Unit, cueIdx int, cueStartMS int64) UnitCue
func NumCues(unitDurMS, targetPeriodMS int64) int
func BuildUnitCues(fps float64, u Unit, targetPeriodMS int64, content CueContentFunc,
	opts ...UnitOption) ([]schedule.Frame, error)
// Move each EOC onto its cue's first frame; the build rides the preceding frames, which for
// a unit's first cue is the PREVIOUS unit. next names that following unit outright.
func WithFlipAtCueStart(next Unit, content CueContentFunc) UnitOption
// Paint-on counterpart: each cue is EDM + RDC + rows, eligible at its slice's first frame, so
// the caption types itself out. Always self-contained per unit — no cross-unit build, hence no
// WithFlipAtCueStart analogue.
func BuildUnitPaintCues(fps float64, u Unit, targetPeriodMS int64,
	content CueContentFunc) ([]schedule.Frame, error)
// Roll-up counterpart: RU2/3/4 + (CR + typed line) per line, in Row order (bottom line last,
// on the base row). Roll-up defines only the NEW line, so what happens between cues is a
// choice: reset (default, EDM on the unit's first frame — self-contained display) or carry.
func BuildUnitRollUpCues(fps float64, u Unit, targetPeriodMS int64, rows int,
	content CueContentFunc, opts ...RollUpOption) ([]schedule.Frame, error)
func WithRollUpCarry() RollUpOption // keep the previous unit's window instead of clearing it
```

> **Cross-note reconciliation:** [#7](docs/design/cea608-wallclock-generation.md) and
> [#4](docs/research/consumer-injection-points.md) sketched `NextFrame` returning `cc_data` (or a full
> SEI NAL) directly. Per [#10](docs/design/package-layout.md) §P3.1 the `cc_data`/NAL build moves to
> `carriage`, so `NextFrame` returns the `{field1,field2,ccCount}` triple and the caller wraps it. This
> keeps mp4ff out of `generate`.

### 4.5 `cue` — shared timed-text intermediate ([#9](docs/design/cea608-webvtt-srt-io.md))

```go
type TimedCue struct { Start, End time.Duration; Content cta608.Screen } // Content REUSES the core Screen

type Reader interface { ReadCues(r io.Reader) ([]TimedCue, error) }       // published plugin seam
type Writer interface { WriteCues(w io.Writer, cues []TimedCue) error }

// 608 -> text: displayed-Screen states over time -> cues (unified screen-change segmentation)
type TimedScreen  struct { Time time.Duration; Screen cta608.Screen }
func Segment(changes []TimedScreen, opts SegmentOptions) []TimedCue        // opts: StreamEnd, DefaultDur

// text -> 608: cues -> merge overlaps by position -> diff -> wall-time-tagged token transitions
type TimedTokens  struct { Time time.Duration; Tokens []cta608.Token }
func Compile(cues []TimedCue) []TimedTokens
```

### 4.6 `webvtt`, `srt` — format serializers ([#9](docs/design/cea608-webvtt-srt-io.md))

```go
func Read(r io.Reader) ([]cue.TimedCue, error)          // implements cue.Reader
func Write(w io.Writer, cues []cue.TimedCue) error      // implements cue.Writer
```

### 4.7 `scc` — Scenarist SCC read/write ([#8](docs/design/cea608-scc-io.md))

```go
type Entry   struct { Frame int; Pairs []byte }         // absolute frame + raw pairs (verbatim)
type SCCFile struct { FPS float64; DropFrame bool; Entries []Entry }

func Read(r io.Reader, opts ...ReadOption) (*SCCFile, error) // infers FPS/DropFrame; WithFPS overrides; accepts ;/:
func Write(w io.Writer, f *SCCFile) error                    // dumb: one Entry -> one line, verbatim

func FrameToTimecode(frame int, fps float64, drop bool) string        // true SMPTE drop-frame
func TimecodeToFrame(tc string, fps float64) (frame int, drop bool, err error)

func (f *SCCFile) TimedPairs() []TimedPair              // flatten: pair[i] -> Frame+i
func GroupPairs(pairs []TimedPair) []Entry              // helper (NOT the writer): bursts -> sparse entries
```

---

## 5. Normative rules & conformance

Folded from [#3](docs/research/normative-rules-608-708-a53.md) (extracted from ANSI/CTA-608-E S-2019,
ANSI/CTA-708-E R-2018, ANSI/SCTE 128-1 2020). These are the build contract for `cta608.Serialize`,
`schedule`, and `carriage`.

### 5.1 `cc_data()` structure — CTA-708-E §4.3

- The builder MUST emit the `cc_data()` syntax of CTA-708-E §4.3 Table 2 (`reserved`,
  `process_cc_data_flag`, `cc_count` (5 bits), `em_data`, then `cc_count` constructs of
  `{marker_bits, cc_valid, cc_type[2], cc_data_1, cc_data_2}`, trailing marker).
- `cc_type`: `00`=608 field 1, `01`=608 field 2, `10`=DTVCC continuation, `11`=DTVCC start. `cc_valid`
  `1` ⇒ the two data bytes are valid.
- **Ordering (MUST):** all 608 constructs (`cc_type` 00/01) appear **first**, before any DTVCC
  constructs. A `cc_valid=0, cc_type=10/11` construct marks the **end of 608 data**.
- go-608 does **not** need its own `cc_data` **parser** on the decode path — `mp4ff/sei.ParseCTA608`
  reads exactly this, and is **envelope-free** (it parses the bytes after the 8-byte T.35 header), so
  the same parser serves the SEI and the OBU path. go-608 owns only the **builder**
  (`carriage.BuildCCData`), which is likewise codec-free.

### 5.2 Carriage envelopes — SEI (SCTE 128-1 2020) and AV1 metadata OBU

**The T.35/GA94 payload is shared by every codec.** The `cc_data()` MUST be carried under
`country_code=0xB5`, `provider_code=0x0031` (ATSC), `user_identifier="GA94"` (0x47413934),
`ATSC1_data` with `user_data_type_code=0x03` → `cc_data()`, trailing `0xFF` — exactly
`mp4ff/sei.ITUData.IsCTA608()`. What varies per codec is only the **envelope** and the **splice**;
the reusable unit across codecs is the SEI message *payload*, not the message and not the NAL unit
([#47](docs/research/av1-metadata-obu-608-layout.md)).

**AVC / HEVC — SEI message.** The payload rides in an SEI `user_data_registered_itu_t_t35`
(payloadType 4). go-608 reuses mp4ff for the SEI wrap/unwrap and EBSP emulation-prevention, and
prepends the codec NAL header itself (AVC `0x06` / HEVC prefix-SEI 39). The message is
**codec-identical** for AVC and HEVC. **Open (informative):** SCTE 128-1 is the AVC/cable normative
home; a precise HEVC carriage citation (A/72 / ATSC-3.0) is needed only if a formal HEVC conformance
claim is made.

**AV1 — `metadata_itu_t_t35` OBU.** The same payload rides in an `OBU_METADATA` (type 5) with
`metadata_type = 4` (ITU-T T.35), followed by `trailing_bits` (a single `0x80`). An OBU carries **no
emulation prevention**, so the payload bytes are written verbatim — the one real encode-side
difference from SEI. On decode, `cc_data` is bounded by `cc_count`, not by `obu_size`, which makes
the reader tolerant of the trailing-bits ambiguity. Source:
[#47](docs/research/av1-metadata-obu-608-layout.md); the envelope itself is mp4ff's
`av1.CreateCTA608MetadataOBU` / `av1.ExtractCTA608` ([#50](https://github.com/Eyevinn/go-608/issues/50)).

- **Placement (MUST):** the caption OBU goes after any sequence-header OBUs and **immediately before
  the first `OBU_FRAME` / `OBU_FRAME_HEADER`**. Placement is not normatively fixed for T.35 — the AV1
  spec's *Metadata ITUT T35 semantics* constrains only the payload bytes — so this is a **choice**,
  made because it is where *Ordering of OBUs* structurally places metadata, because a metadata OBU's
  scope starts where it appears and so must precede the frame it decorates, and because it is the
  structural analog of the SEI rule (after parameter sets, before the first VCL NAL).
- **Anchor on the frame OBU, not on position-from-start.** The mp4 muxer drops temporal-delimiter
  OBUs while IVF keeps them, so a position-based rule would mean different bytes in the two
  containers.
- **No no-anchor fallback (MUST).** Unlike the SEI splice — which appends when a sample has no VCL
  NAL — a temporal unit without a frame OBU is malformed, not merely frameless. It MUST be reported
  as an error.
- **Assignment: one caption OBU per sample, in sample order.** One sample is one temporal unit, and
  the AV1 spec's *Ordering of OBUs* requires each temporal unit to have exactly one shown frame.
  Several frame OBUs in a sample (hidden reference frames) are therefore **not** an ambiguity; only
  the first is the anchor, and a bare `show_existing_frame` frame header is still an anchor.
- **Precondition: `OperatingPointIdc == 0` (non-scalable).** The one-shown-frame invariant is a
  *bitstream* guarantee, but a conditional one: with scalability the spec requires one shown frame
  *per layer* per temporal unit, and "the caption for this sample" stops naming a single picture. A
  scalable `av01` track MUST be rejected rather than captioned on a guess (§1.3).

### 5.3 `cc_count` per frame rate & pair scheduling — CTA-708-E §4.3.6

`cc_count = round((r × t) / 16000)`, `r`=9600 bps (÷1.001 for fractional rates), `t`=frame period ms.
The integer and fractional members of a family yield the **same** count:

| Frame rate | `cc_count` |
|---|---|
| 23.976 / 24 | **25** |
| 25 | **24** (formula-derived; confirm for PAL conformance) |
| 29.97 / 30 | **20** |
| 50 | **12** (formula-derived) |
| 59.94 / 60 | **10** |

- **608 rate cap (MUST):** the 608 datastream MUST NOT exceed `(60/1.001 × 2)` B/s ≈ 119.88 B/s. So
  **≤ 30 fps** → room for one field-1 **and** one field-2 pair per frame; **60 fps** → **one** 608
  pair per frame. `schedule` emits ≤1 pair/field/frame, placed first in `cc_data()`, then pads.
- **Padding (§4.3.5):** construct padding = `cc_valid=0, cc_type=10/11, 0x0000`. A leading
  `cc_valid=0, cc_type=00/01` means "no 608 waveform this field this frame" — **not** padding. Keep
  distinct from the **608-level null pair `0x80 0x80`** (`cc_valid=1`; a 608 no-op that keeps a field
  alive). go-608 MUST NOT conflate these two "nothing here" encodings.
- **`cc_count` policy (decide at implementation):** emit the **full** per-rate `cc_count` with DTVCC
  padding (fixed-allocation; most interoperable — the recommended default) vs. a **minimal** count
  when 608-only. `cc_count` MAY vary frame-to-frame (e.g. 3:2 pulldown alternates 20/30).

### 5.4 Encoder contract — CTA-608-E §D.2

- **Odd parity — MUST.** All bytes forced to odd parity (bit 7).
- **Frame alignment — MUST.** A two-byte control code's bytes MUST sit in one frame's pair; insert a
  null pair as needed.
- **Control-code doubling — SHOULD.** Two-byte control codes (first byte 0x10–0x1F) SHOULD be sent
  twice in successive frames; encoders SHOULD offer a switch, and doubling SHOULD be **disabled by
  default for field 2**. go-608 default: **doubling ON for field 1, OFF for field 2**, overridable.
  (Decoders act on the first valid copy; to get two real CRs on field 2 you must encode three.)

### 5.5 Tables — CTA-608-E (port verbatim)

- **PAC — Table 53.** First byte = row + channel; second byte = color/underline **or**
  indent/underline. **All indent codes force white** (Table 53 note). **Indent range 0–28** (steps of
  4) — honor the full range (media-tools capped at ≤20; that is a tool limit, not the spec).
- **Mid-row — Table 51.** Color index `(b−0x20)/2`, underline `b&1`, italics at the top values.
- **Control codes.** RCL/BS/DER/RU2-4/RDC/TR/RTD/EDM/CR/ENM/EOC/Tab-Offset per §7/Annex F. Control
  codes with first byte `0x01–0x0F` are field-2 (XDS) and MUST NOT be inserted on field 1.
- **Character sets.** Standard (Table 50), special `0x11+0x30..0x3F` (Table 49), extended
  Western-European `0x12/0x13 + 0x20..0x3F` (Tables 5–10, **normative** — Spanish/Misc/French,
  Portuguese/German/Danish). Extended chars use **backspace-and-replace** (emit a standard fallback +
  the 2-byte extended code).
- Handling of runes outside the 608 repertoire is a `cta608.Serialize` concern (see §8 for how the
  lossy timed-text mappings inherit it).

### 5.6 Fields & channels

- Line-21 **field 1** (`cc_type=00`) and **field 2** (`cc_type=01`); each field carries two data
  channels (first-byte high nibble selects the channel). Captions CC1–CC4 / text T1–T4 map onto
  field × channel. The core is per-channel; `DemuxField`/`MuxField` handle the two in-field channels.

---

## 6. Carriage & injection

Rationale: [#6](docs/design/cea608-carriage-seam.md); consumer study: [#4](docs/research/consumer-injection-points.md).

**Encode flow (shared prefix):** `cta608.Serialize` → per-field byte pairs → `schedule` picks
`ccCount` + per-frame pairs → `carriage.BuildCCData(f1,f2,ccCount)` → a `cc_data()` payload. Only
the envelope and the splice differ from there (§5.2):

- **AVC / HEVC:** → `carriage.SEIMessage(ccData)` → `carriage.NALU(codec, msg)` → **bare NAL** →
  `carriage.SpliceSEIBeforeVCL(sample, nalu, codec)`, which adds the 4-byte length prefix and places
  it before the first VCL NAL (into a per-emission copy, before CENC).
- **AV1:** → `carriage.MetadataOBU(ccData)` → **complete OBU** → `carriage.SpliceOBUBeforeFrame(sample,
  obu)`, which places it before the first `OBU_FRAME` / `OBU_FRAME_HEADER`.

**Decode flow:** `carriage.FieldPairs(sampleNALUs, codec)` for AVC/HEVC, `carriage.OBUFieldPairs(sample)`
for AV1 — both reuse mp4ff's envelope-free `sei.ParseCTA608` under the hood — →
`cta608.Decoder.Feed(field1)` → `Screen`.

**Frame assignment (MUST): presentation order, from the track origin.** The *k*-th caption payload
belongs to the *k*-th **displayed** frame — the sample with the *k*-th smallest presentation time —
not the *k*-th sample in decode order. Media time is measured from the **track origin**, the smallest
presentation time in the file, so a subtitle file's `t=0` maps to the first displayed frame whatever
absolute timestamps the container happens to start at. Edit lists are not consulted.

This is one codec-free rule, not a per-codec special case: AVC and HEVC reorder in the *container*
via composition offsets, AV1 reorders inside the *bitstream* (hidden frames plus
`show_existing_frame`) and so always has `pts == dts`, which makes the rule a no-op for AV1 rather
than an exception. Getting it wrong is **not** detectable by a round-trip through go-608's own reader
— a read side that repeats the write side's mistake agrees with itself while every third-party
decoder sees permuted text ([#54](https://github.com/Eyevinn/go-608/issues/54)). Samples are still
*written* in decode order, which the container requires; only the assignment and the timing are in
presentation order.

**Correctness bar:** carriage changes are held to byte-identity against the previous implementation
for already-shipping paths, plus verification with an **independent decoder** —
`ffmpeg -f lavfi -i "movie=FILE.mp4[out0+subcc]"`, which reads AVC, HEVC and AV1 through ffmpeg's own
A/53 path. A captioned real-world `av01` reference could not be obtained with available tooling
([#49](https://github.com/Eyevinn/go-608/issues/49)), so for AV1 this is the interop bar.

**Injection points (informative, [#4](docs/research/consumer-injection-points.md)):** both consumers
own AU assembly and have no 608 code today. livesim2's chunked low-latency path has a per-sample
`FullSample` loop (a cheap first hook); moqlivemock injects into a copy before CENC. go-608 supplies
bytes; it never muxes.

---

## 7. Wall-clock generation (first milestone)

Rationale + validated prototype (`.scratch/proto-wallclock/`): [#7](docs/design/cea608-wallclock-generation.md).

- **Drive model:** pull-by-wall-time — `Generator.NextFrame(frameWallMS)`, one call per video frame.
  Robust to gaps/seeks/VFR; makes drop-frame a non-issue (the caller's wall time already accounts).
- **Content:** two centered lines, configurable — row 14 UTC RFC3339 (white), row 15 media time
  (yellow) by default. Per-second refresh in v1 (sub-second ticking deferred).
- **Mode & timing:** pop-on, **build-ahead** into non-displayed memory, a single `EOC` on the **last
  frame** of the second flips it on — frame-accurate, zero lag.
- **Cadence:** one field-1 pair/frame, `cc_count` padding per §5.3; field 2 unused by default (CC1
  only). The ~23-pair two-line build fits a 1-second refresh at 25/30/50/60; an **overrun guard**
  flags content that exceeds the budget.
- **Centering (608 lowering):** `PAC(row, indent)` + `Tab Offset`; a centered *colored* line is
  `PAC(indent, white)` → Tab Offset → `MidRow(color)` (the mid-row cell shifts text one column; the
  generator compensates).

---

## 8. Timed-text I/O (SCC / WebVTT / SRT)

### 8.1 SCC — byte-exact ([#8](docs/design/cea608-scc-io.md))

SCC is a byte-pair container, a sibling of the SEI carriage: it owns the text file structure +
timecodes only; `cta608` owns all 608 semantics. **Byte-exact round-trip.**

- Canonical time = **absolute integer frame number**; **true SMPTE drop-frame** for 29.97/59.94
  (drop frames 0,1 at each minute except every 10th) — the correctness media-tools/SVTA lack. PAL(25)
  included; fps configurable; drop-frame only on fractional NTSC rates.
- SCC is **sparse** (event list) → `Read` **infers fps** (separator `;`⇒drop; max line-start `FF`),
  with an override and a 29.97 fallback; accepts both `;` and `:`. `Write` is **dumb** (one `Entry` →
  one line, verbatim; the caller decides pairs-per-line); `GroupPairs` is an optional helper.

### 8.2 WebVTT / SRT — lossy, via the shared cue model ([#9](docs/design/cea608-webvtt-srt-io.md))

WebVTT & SRT are **thin serializers** over `cue.TimedCue` (whose `Content` is a `cta608.Screen`); the
608↔cue mapping is written **once** in `cue`. A **lossy, Screen-mediated** sibling of the byte-exact
SCC/SEI containers.

- **608→text:** unified **screen-change segmentation** — a displayed-`Screen` change closes the
  current cue and opens a new one (empty screen = gap); dangling end-of-stream cue gets a
  configurable end.
- **Direct-write modes need a coalescing rule (MUST).** Pop-on builds into non-displayed memory, so
  its display changes once per caption. Roll-up and paint-on write straight to the *displayed*
  screen, so a bare screen-change rule cuts a cue every byte pair — up to two characters — giving 14
  one-frame cues for two roll-up lines. `cue.SegmentOptions.Coalesce` selects the boundary:
  `CoalesceStructural` (default) cuts only at a scroll, an erase, a jump to another row or an
  overwrite, so roll-up yields **one cue per scroll step** and paint-on **one per write burst**;
  `CoalesceNone` is the faithful per-change rendering, and the only mode needing no lookahead.
  A period's cue starts at its **first** change and carries the screen as of its **last**, so the
  completed caption is displayed from when its first characters appeared — timestamping at
  completion instead would leave the typing interval in a gap.
- **Coalescing is gated on the caption mode**, which `cue.TimedScreen.Mode` carries from
  `cta608.Decoder.Mode`. By screen alone a pop-on caption replaced by a longer one is
  indistinguishable from a line being typed, so without the mode the rule would silently merge two
  distinct captions. The zero value is pop-on, which never coalesces.
- **text→608:** **pop-on only** (roll-up authoring out of scope). Overlapping cues are **merged by
  position** at each boundary (union of active cues' Screens; later cue wins same-row conflicts) →
  the core `diff` engine re-flips the pop-on caption.
- **Styling:** preserved, **quantized to 608's 8-color palette**. Out: WebVTT `<c.name>` (+STYLE) /
  SRT `<font color>`, plus `<i>`/`<u>`. In: nearest-of-8 color; **bold dropped**; font/size/region
  dropped; background best-effort (VTT `::cue` bg; SRT none).
- **Positioning:** WebVTT `line:`/`position:`/`align:` ⇄ grid `Row`/`Column`/indent (quantized,
  round-trip approximate). SRT has no standard positioning → 608→SRT drops it (bottom-centered),
  SRT→608 uses a default bottom anchor; position-less WebVTT → same default.
- **Timing boundary:** the mapping stops at **timed tokens/Screens**; frame scheduling is the shared
  `schedule` layer (§4.3), not part of the format packages.
- **Cue times are display times (MUST).** A cue's `Start` is when the caption is **visible**, so the
  pop-on build is transmitted over the frames *before* it (`schedule.FlipOnTime`) rather than starting
  on it. Building *from* the cue start instead makes every caption appear a build-length late —
  measured 367–433 ms at 30 fps, halving at 60 fps — and the error compounds over repeated
  conversions. `schedule.FlipAfterBuild` (`-no-preroll`) restores that pre-v0.8.0 behaviour.
- **The two containers time captions by different conventions**, and this is what reconciles them: an
  SCC entry's timecode is when its first byte pair is **transmitted**, while a WebVTT/SRT cue start is
  when the caption is **displayed**. They differ by exactly the build, so with the build pre-rolled
  SCC → text → SCC returns the original timecodes (the only remaining asymmetry is the terminating
  EDM `cue.Compile` always appends for a dangling final cue). 608 → text is unaffected either way:
  it reports when the decoder actually shows the caption.

### 8.3 Extension seam & TTML (fog)

`cue.Reader`/`cue.Writer` over `[]TimedCue` is **published** — TTML and third-party formats plug in
with zero change to the 608↔cue mapping. **TTML ↔ 608** is deferred fog; it graduates when a TTML need
is confirmed.

---

## 9. Repo conventions & build

Follow [hi264](https://github.com/Eyevinn/hi264); library layout mirrors
[mp4ff](https://github.com/Eyevinn/mp4ff). Decided in [#10](docs/design/package-layout.md).

- **Go 1.25.** Module `github.com/Eyevinn/go-608`. **MIT** license.
- Makefile (`all: check build test`), **golangci-lint**, three GitHub workflows (go / coverage /
  golangci-lint), pre-commit via `venv`.
- `internal/` holds version stamping (`commitVersion`/`commitDate` via LDFLAGS) and shared cmd/mp4
  glue. `cmd/`: `go608-extract`, `go608-inject`, `go608-clock`, `go608-info` (format-only conversion
  is a mode of extract/inject).
- `examples/` (runnable snippets per package) and `testdata/` (sample SCC/WebVTT/SRT, a fragmented
  mp4 carrying 608, and raw `cc_data` vectors for core round-trip tests).

---

## Appendix — sources & full notes

- **Design notes** (rationale, one per decision): [`docs/design/`](docs/design/) — core model, carriage
  seam, wall-clock generation, SCC I/O, WebVTT/SRT I/O, package layout.
- **Research** (informative background, not build requirements):
  - Prior-art survey — [`docs/research/prior-art-608.md`](docs/research/prior-art-608.md) ([#2]):
    SVTA `libs/608` (TS) and media-tools `cea608.py` are one decoder algorithm; media-tools `sccgen.py`
    is the only encoder; dash.js `Cta608Parser`; **SubtitleEdit** (GPL — behaviour cross-check only).
  - Normative rules — [`docs/research/normative-rules-608-708-a53.md`](docs/research/normative-rules-608-708-a53.md) ([#3]).
  - Consumer injection points — [`docs/research/consumer-injection-points.md`](docs/research/consumer-injection-points.md) ([#4]).
- **Glossary:** [`CONTEXT.md`](CONTEXT.md).
- **Planning map:** [Eyevinn/go-608#1](https://github.com/Eyevinn/go-608/issues/1).
