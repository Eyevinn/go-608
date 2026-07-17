# Core `cta608` domain model (design note)

Working design note for wayfinder ticket #5 (Design the core CEA-608 domain model). Built
incrementally during the domain-modeling grilling. Feeds the SCC, WebVTT, carriage-seam (#6) and
wall-clock (#7) tickets. Companion research: `../research/prior-art-608.md`,
`../research/normative-rules-608-708-a53.md`.

## Decisions

### D1 — The command/token stream is the spine (not a screen grid)

The canonical in-memory representation is a **typed 608 token/command stream**, not a rendered
screen grid.

- **Rationale (T.E.):** real captions are typically ~2 lines and rarely full 32-column width, so a
  15×32 grid of styled cells is too heavy a structure to sit at the center. All surveyed prior art
  (SVTA `libs/608`, media-tools) centers on a grid *because they are decoders reconstructing the
  display*; go-608 is encode-first (wall-clock generation is the first milestone), and the
  encode-natural shape is authoring → command stream → bytes.
- **Consequence:** encode serializes tokens → byte pairs; decode parses byte pairs → tokens; the
  screen/display is a **derived** artifact, materialized only when a consumer needs rendered output.
- **Derived principle — keep everything sparse:** even the decode-side rendered display is NOT a
  15×32 array; it is a sparse set of positioned, styled text runs (a few lines). This principle
  propagates to the display type, the caption-block authoring type, and the WebVTT/SCC mappings.

### D2 — A lightweight display/line state exists, used bidirectionally (for diff-encoding)

Although the token stream is the spine, the model still needs a **rendered state** — but sparse and
line-oriented, not a grid.

- **Rationale (T.E.):** to encode we need the current state of a line so we can **emit only the
  difference**. Live/roll-up captioning appends to the bottom line and scrolls; paint-on patches in
  place — rebuilding and re-sending the whole caption each frame is wrong. The encoder diffs the
  desired state against the current state and emits the minimal token sequence.
- **Consequence:** the display state is used in **both** directions — decode *builds it up* by
  interpreting tokens; encode *diffs against it* to produce tokens. It is the same sparse,
  line-based type in both.
- **Open (being grilled):** the granularity of the state and its diff (whole sparse screen / per
  line / per character-run within a line), whether it is one shared type, and whether 608's
  double-buffered memory (displayed vs non-displayed, for pop-on's build-then-flip) is modeled
  explicitly or kept as a transient interpreter detail.

### D3 — Display state = sparse screen of rows; row = character-runs; double buffer is a row flag

- **Shape (confirmed T.E.):** the display state is a **sparse screen** — a collection of **rows**,
  not a 15×32 array. A **row** is a sequence of **character-runs** (a run = contiguous text sharing
  one `Pen`). Diff granularity bottoms out at the **character-run within a row** (so live roll-up can
  append "world" after "hello " without resending the row).
- **Encode vs decode state:** "maybe similar at least" (T.E.). Provisionally **one shared type**;
  split into encode-state vs decode-state only if a concrete divergence forces it. Revisit.
- **Double buffer as a row property (T.E.):** rather than two `Screen` objects (displayed +
  non-displayed memory), each **row carries a `displayed` flag**. Pop-on writes build rows with
  `displayed=false`; `EOC` promotes them to `displayed=true` and drops the previously-displayed
  rows. Rendering considers only `displayed` rows. Roll-up/paint-on mutate displayed rows directly.

### D4 — `Pen` is a small comparable value struct; runs carry absolute column

- **`Pen` (confirmed T.E.):** `{ Color (foreground), Italic, Underline }`. **Flash dropped**
  (`FON` rarely used). **Background optional** — an extended-decoder feature, not core; carried as
  an optional attribute. To keep `Pen` a **comparable value struct** (so `==` works, no aliasing —
  prior-art doc §6.2), background is a sentinel `Color` value (e.g. `BgNone`), **not** a pointer.
  Colors are a typed enum (`White,Green,Blue,Cyan,Red,Yellow,Magenta`; +`Black,Transparent` for
  background), never strings.
- **Position (confirmed T.E.):** each run carries its **absolute column 0–31**, resolved from PAC
  indent (0–28) + Tab Offset (0–3) at parse/build time. The raw "indent-4 PAC vs indent-0 + tab-4"
  distinction is **not** preserved on round-trip — accepted.
- **Resulting display types (provisional):**
  ```go
  type Screen struct { Rows []Row }            // sparse; only non-empty rows
  type Row    struct { Index int; Displayed bool; Runs []Run }  // Index 1..15
  type Run    struct { Column int; Text string; Pen Pen }       // Column 0..31
  type Pen    struct { Color Color; Italic, Underline bool; Background Color }
  ```

### D5 — The token stream is public; character data is a `Chars` run token; wire concerns live in the serializer

- **Public spine (confirmed T.E.):** `Token` is a **first-class public type** (a Go interface
  implemented by concrete command structs — `PAC`, `MidRow`, `Chars`, and the control commands
  `RCL/EOC/EDM/ENM/CR/RU2/RU3/RU4/RDC/TR/RTD/BS/DER/TabOffset/BackgroundAttr`). Users can build and
  inspect `[]Token` directly — good for tooling, tests, and SCC round-tripping.
- **`Chars{ Text string }` run token (confirmed T.E.):** character data is a run, not per-character.
  Mirrors the display char-run.
- **Wire concerns live in the serializer — resolves ticket Q6 (error resilience).** Odd parity,
  control-code **doubling**, 2-chars-per-pair packing, extended-char backspace-and-replace filler,
  and null-pair **frame alignment** are all in the `Serialize([]Token) → []byte` /
  `Parse([]byte) → []Token` boundary — **not** in the token model. Tokens are logical and
  byte-agnostic. Per #3: parity + frame-align are mandatory; doubling is an option (default on for
  field 1, off for field 2).

### D6 — Per-channel core; thin field mux/demux above; no XDS

- **Per-channel instance (confirmed T.E.):** a decoder/encoder instance represents **one channel**
  (e.g. CC1). A thin **field** layer above it demuxes a field's byte stream into its channels
  (control-byte high nibble selects the in-field channel) on decode, and muxes a channel's output
  into a field byte stream on encode. Matches encode-first reality (you almost always generate CC1).
- **No XDS (confirmed T.E.):** XDS is **out of the core**. Decode skips field-2 XDS control codes
  (`0x01–0x0F`); encode never emits them. → **Action on #5 close:** move the map's "XDS in/out of
  scope?" item from *Not yet specified* to *Out of scope*.

### D7 — Mode is per-channel state; text mode out of scope

- **Mode = channel state (confirmed T.E.):** a channel is in exactly one of **pop-on**,
  **roll-up-N** (N ∈ {2,3,4}), **paint-on** at a time, switched by control tokens
  (`RCL`/`RU2/3/4`/`RDC`). The decoder tracks current mode; the authoring caption block declares its
  mode; roll-up carries its row count. Mode is **not** a property of `Screen`/`Row`.
- **No text mode (confirmed T.E.):** the core does not model/render text mode; decode recognizes the
  `TR`/`RTD` switch but ignores text-mode content; encode never generates it. → **Action on #5
  close:** add text mode to the map's *Out of scope*.

### D8 — `CaptionBlock` compiles to a target `Screen`; encode = diff; timing stays out of the core

- **Single lowering path (confirmed T.E.):** the encoder's fundamental operation is
  `diff(current Screen, target Screen) → []Token`. A friendly **`CaptionBlock`** (a few text lines +
  anchor + style + mode) is a **convenience constructor** that compiles to a target `Screen`; power
  users can build a `Screen` directly. All mode-specific token generation lives in the one diff
  engine.
- **Timing out of the core (confirmed T.E.):** the core `cta608` types are **timing-agnostic** — no
  `time.Duration`/PTS. The **generation layer (#7)** attaches presentation times and does the
  frame-rate/`cc_count` scheduling (#3). Keeps the core dependency-free and pure.

---

## Core data types (pure `cta608` package — no heavy deps)

```go
// ---- Styling (comparable value type) ----
type Color uint8
const ( ColDefault Color = iota // foreground renders as White; background = none
        White; Green; Blue; Cyan; Red; Yellow; Magenta; Black; Transparent )
type Pen struct { Color Color; Italic, Underline bool; Background Color } // == works

// ---- Rendered display: sparse, derived, bidirectional (D2/D3/D4) ----
type Run    struct { Column int; Text string; Pen Pen }        // Column 0..31 absolute
type Row    struct { Index int; Displayed bool; Runs []Run }   // Index 1..15; Displayed = D3 flag
type Screen struct { Rows []Row }                              // only non-empty rows

// ---- Token stream: public, wire-faithful spine (D1/D5) ----
type Mode uint8
const ( PopOn Mode = iota; RollUp; PaintOn )                   // text mode not modeled (D7)
type Token interface { token() }
type Chars         struct { Text string }                     // a character run (D5)
type PAC           struct { Row, Indent int; Pen Pen }         // wire PAC (indent XOR color per spec)
type MidRow        struct { Pen Pen }
type TabOffset     struct { Columns int }                      // 1..3
type BackgroundAttr struct { Pen Pen }                         // optional feature (D4)
type SetMode       struct { Mode Mode; RollUpRows int }        // RCL / RU2-4 / RDC
type Command       struct { Op Op }                            // EOC,EDM,ENM,CR,BS,DER
```

Token layer is **wire-faithful** (one token ≈ one wire command) so `[]Token` round-trips SCC/bytes
exactly; the `Screen` is the **lossy** derived view (absolute column, per D4). _(Exact Go encoding
of the sum type and the precise `PAC` variant fields — indented PACs are always white — finalized at
implementation time.)_

## Encode / decode API sketch

```go
// Wire boundary — all parity / doubling / 2-per-pair packing / null frame-alignment here (D5, #3)
func Serialize(tokens []Token, opts SerializeOptions) []byte   // opts: doubling on/off per field
func Parse(data []byte, opts ParseOptions) ([]Token, error)    // opts: parity validate vs strip

// Decode: tokens -> rendered Screen (stateful, per channel — D6)
type Decoder struct { /* current Screen, mode, pen, cursor */ }
func (d *Decoder) Feed(data []byte) error      // Parse + interpret
func (d *Decoder) Push(tokens []Token)         // interpret only
func (d *Decoder) Screen() Screen              // current displayed rows
// (emits a change signal when the displayed screen differs — dirty-diff cue, prior-art §2.1)

// Encode: target Screen -> tokens (stateful, per channel — D8 diff engine)
type Encoder struct { /* current Screen, mode */ }
func (e *Encoder) SetScreen(target Screen) []Token   // diff current->target -> tokens
func (e *Encoder) Apply(b CaptionBlock) []Token      // convenience: block -> target Screen -> tokens

// Friendly authoring (D8) — compiles to a target Screen
type CaptionBlock struct { Lines []Line; Anchor Anchor; Mode Mode; RollUpRows int }
func (b CaptionBlock) Screen() Screen

// Thin field mux/demux (D6) — the 2 in-field channels; XDS skipped (D6), no text mode (D7)
func DemuxField(fieldBytes []byte, opts ParseOptions) (ch1, ch2 []Token, err error)
func MuxField(ch1, ch2 []Token, opts SerializeOptions) []byte
```

**Package layering:** pure `cta608` (types above + char/PAC/parity tables + Serialize/Parse/Decoder/
Encoder) → thin field mux/demux → [#6] mp4ff-backed carriage (SEI/`cc_data` ↔ field byte pairs) →
[#7] generation (timing, wall-clock, per-frame `cc_count` scheduling). Only the first is #5's scope.

## Actions when closing #5 (map updates)

- Move **"XDS in/out of scope?"** from *Not yet specified* → *Out of scope* (D6).
- Add **text mode** to *Out of scope* (D7).
- The SCC / WebVTT / package-decomposition fog items are now unblocked by this model and can
  graduate to tickets (they build on `Token`/`Screen`/`CaptionBlock`).

