# go-608 — Design Specification

**Module:** `github.com/Eyevinn/go-608` · **Status:** design spec, ready for `/implement` · **License:** MIT · **Go:** 1.25

A generic Go library for **CTA-608 / CEA-608** captions: encode + decode, `cc_data` + SEI carriage
per ATSC A/53 (AVC & HEVC), wall-clock caption generation, and timed-text (SCC / WebVTT / SRT) I/O.

This document is the **consolidated, self-contained hand-off** for implementation. It reproduces the
public API and the normative build requirements in-line; the per-decision **rationale** lives in the
design notes under [`docs/design/`](docs/design/) and the source research under
[`docs/research/`](docs/research/), which this spec cites but does not restate. Conformance language
(**MUST / SHOULD / MAY**) follows RFC 2119.

The naming: the modern standard is **CTA-608** (the org renamed from CEA to CTA); the core package is
`cta608`. The legacy spelling "CEA-608" persists in prior art and in the mp4ff dependency's API
(`ParseCEA608`, `IsCEA608`) — that spelling is confined to the `carriage` package that wraps mp4ff.

---

## 1. Overview & scope

### 1.1 Goals

- **Encode + decode** CTA-608 caption data, encode-first (authoring → byte pairs is the primary path).
- **Carriage** of `cc_data()` in AVC/HEVC SEI (`user_data_registered_itu_t_t35` → `GA94` → `cc_data`),
  reusing `Eyevinn/mp4ff` for the SEI/NAL layer.
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
carriage/   cc_data / T.35 / SEI / NAL; Codec enum — wraps mp4ff            deps: cta608, mp4ff
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

### 4.2 `carriage` — SEI/`cc_data` carriage, wraps mp4ff ([#6](docs/design/cea608-carriage-seam.md))

```go
type Codec int
const ( CodecAVC Codec = iota; CodecHEVC )      // explicit; never sniffed

// Encode (pure, timing-free; ccCount supplied by schedule). A field pair is 0 or 2 bytes.
func BuildCCData(field1Pair, field2Pair []byte, ccCount int) []byte
// Wrap cc_data as a T.35/GA94 SEI message (codec-identical for AVC/HEVC; no codec here).
func SEIMessage(ccData []byte) sei.SEIMessage
// Serialize one or more SEI messages into a BARE codec NAL (no 4-byte length prefix).
func NALU(codec Codec, msgs ...sei.SEIMessage) []byte
func FrameSEINALU(field1Pair, field2Pair []byte, ccCount int, codec Codec) []byte // one-call
// Decode (thin wrapper over mp4ff sei.ParseCEA608): sample NALUs -> field byte-pair streams.
func FieldPairs(sampleNALUs [][]byte, codec Codec) (field1, field2 []byte, err error)
```

### 4.3 `schedule` — timed tokens → frames ([#7](docs/design/cea608-wallclock-generation.md)/§5.3)

```go
type Frame struct { Field1, Field2 []byte; CCCount int } // per video frame; feed to carriage
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
func NewGenerator(fps float64, cfg Config) *Generator
// Advance one frame; returns the per-frame triple (via the internal scheduler). The caller wraps
// with carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec) and splices.
func (g *Generator) NextFrame(frameWallMS int64) schedule.Frame
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
- go-608 does **not** need its own `cc_data` **parser** on the decode path — `mp4ff/sei.ParseCEA608`
  reads exactly this. go-608 owns only the **builder** (`carriage.BuildCCData`).

### 5.2 SEI carriage — SCTE 128-1 2020

- The `cc_data()` MUST ride in an SEI `user_data_registered_itu_t_t35` (payloadType 4) with
  `country_code=0xB5`, `provider_code=0x0031` (ATSC), `user_identifier="GA94"` (0x47413934),
  `ATSC1_data` with `user_data_type_code=0x03` → `cc_data()`, trailing `0xFF`. This is exactly
  `mp4ff/sei.ITUData.IsCEA608()`; go-608 reuses mp4ff for the SEI wrap/unwrap and EBSP
  emulation-prevention, and prepends the codec NAL header itself (AVC `0x06` / HEVC prefix-SEI 39).
- The payload is **codec-identical** for AVC and HEVC. **Open (informative):** SCTE 128-1 is the
  AVC/cable normative home; a precise HEVC carriage citation (A/72 / ATSC-3.0) is needed only if a
  formal HEVC conformance claim is made.

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

**Encode flow:** `cta608.Serialize` → per-field byte pairs → `schedule` picks `ccCount` + per-frame
pairs → `carriage.BuildCCData(f1,f2,ccCount)` → `carriage.SEIMessage(ccData)` →
`carriage.NALU(codec, msg)` → **bare NAL** → *consumer* prepends the 4-byte length and splices before
the first VCL NALU (into a per-emission copy, before CENC).

**Decode flow:** consumer/mp4ff hands sample NALUs → `carriage.FieldPairs` (reuses
`sei.ParseCEA608`) → `cta608.Decoder.Feed(field1)` → `Screen`.

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
  current cue and opens a new one (empty screen = gap). Roll-up → one cue per scroll step (visible
  lines repeat); dangling end-of-stream cue gets a configurable end.
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
