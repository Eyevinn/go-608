# WebVTT / SRT ↔ 608 (timed-text I/O) design note

Design note for wayfinder ticket #9 (Design WebVTT ↔ 608 mapping). Built during the grilling with
T.E. Sits on the core `cta608` model (#5) and is a sibling of the SCC I/O note
(`cea608-scc-io.md`) and the SEI carriage note (`cea608-carriage-seam.md`). References:
`../research/prior-art-608.md` §3 (dash.js `Cta608Parser`→`CaptionScreen`→`newCue`), §8
(**SubtitleEdit** — GPL, consult don't copy). **Scope note:** the ticket named WebVTT only; the
design deliberately covers **WebVTT + SRT** and leaves a **pluggable seam** for TTML and others
(scope expansion approved by T.E.; reflected on the map).

## Decisions

### W1 — Symmetric bidirectional interchange; WebVTT + SRT now, TTML-ready

- **Confirmed (T.E.):** both directions are **first-class** — this is a general timed-text ⇄ 608
  interchange, neither direction privileged. Fidelity is "best faithful mapping across a lossy
  boundary", not byte-exact round-trip (contrast SCC, which *is* byte-exact).
- **SRT added (T.E.):** SRT is the simpler sibling of WebVTT — an ordered cue list with light inline
  styling (`<i>`/`<b>`/`<u>`/`<font color>`) and no standard positioning. It rides the same
  intermediate as WebVTT (W2), so it costs little.
- **TTML and others: pluggable, deferred (T.E.):** TTML (IMSC/XML, regions, rich styling) is **not**
  designed here — it plugs into the same seam (W8) later. Its mapping is future fog.

### W2 — One shared cue intermediate; cue content **reuses the core `Screen`**

- **Confirmed (T.E.):** WebVTT and SRT are **thin serializers** over a single public intermediate,
  `TimedCue{ Start, End, Content }`. The hard 608↔cue mapping is written **once**; the formats
  differ only in how they serialize styling and positioning. This is exactly SCC's "container over
  the core" posture (`cea608-scc-io.md` S1), applied to timed text.
- **Content is a `Screen` (T.E.):** a cue's rendered content *is* positioned styled rows — precisely
  what the core already calls a `Screen` (`Row{Index,Displayed,Runs}`, `Run{Column,Text,Pen}`). So
  `TimedCue.Content` **reuses `cta608.Screen`** rather than introducing a parallel styled-text type.
  Consequence: the cue intermediate is "608-flavoured" — its coordinate system is the 15×32 grid, so
  format positioning/styling is quantized to the grid/palette **at the serializer edge** (W5, W6),
  not carried losslessly through the middle.

### W3 — 608→text: unified screen-change segmentation

- **Confirmed (T.E.):** a continuous 608 stream is cut into cues by **one rule for all modes**: every
  change in the **displayed** `Screen` closes the current cue (`End = now`) and, if the new displayed
  screen is non-empty, opens a new cue (`Start = now`). An empty displayed screen (post-`EDM`) is a
  **gap** — no cue. This rides the core decoder's existing "displayed screen changed" signal
  (core note, `Decoder.Screen()` + dirty-diff cue).
- **Mode behaviour falls out of the one rule:**
  - **Pop-on** → one cue per caption (`EOC`→`EDM`/replace): the natural, clean case.
  - **Roll-up** → **one cue per scroll step**; because each `CR` scrolls the window, the visible
    lines **repeat** across successive cues as they move up. Faithful, if verbose. (A readability
    "coalesce roll-up into one cue per utterance" mode was considered and **declined** — the unified
    rule wins on simplicity/correctness.)
  - **Paint-on** → a cue per in-place change.
- **Dangling end-of-stream cue:** a caption still displayed when the stream ends has no natural `End`.
  Resolved with a **configurable end** — a caller-supplied stream-end time, else `Start + a default
  duration`. (Exact default finalized at implementation.)

### W4 — text→608: pop-on only

- **Confirmed (T.E.):** every WebVTT/SRT cue compiles to a **pop-on** caption — built in
  non-displayed memory, flipped on (`EOC`) at `cue.Start`, cleared/replaced at `cue.End`. This
  matches cue semantics exactly (a discrete timed block).
- **Roll-up authoring is out of scope** for text→608. A `roll-up → text → 608` round-trip therefore
  lands as pop-on — **accepted lossy** behaviour. (If a live-captioning authoring use case appears,
  roll-up emission can graduate from fog later.)

### W5 — Styling preserved, quantized to the 608 palette

- **Confirmed (T.E.):** carry `Pen` (color / italic / underline / optional background) across the
  boundary, quantized to what each side can express.
- **608→text (out):**
  - **WebVTT:** color → a `<c.colorname>…</c>` class span with a `STYLE` header block mapping the
    class to a CSS color; italic → `<i>`; underline → `<u>`; background → a `::cue` background
    best-effort.
  - **SRT:** color → `<font color="#rrggbb">`; italic → `<i>`; underline → `<u>`; background dropped
    (no standard SRT support).
- **text→608 (in):**
  - Any CSS/`<font>` color (arbitrary hex) → the **nearest of 608's 8 colors** (nearest-match
    quantization).
  - `<i>`/`<u>` → italic/underline. WebVTT/SRT **bold** (`<b>`) has **no 608 equivalent → dropped**.
  - Font family/size, regions, and other CSS 608 can't hold → **dropped**.
  - Background → best-effort (from WebVTT `::cue` bg where present; SRT has none).
- (The exact class-naming and `STYLE`-block vs fully-inline choice for WebVTT is a serializer detail
  finalized at implementation; the decision here is the **fidelity level**, not the tag syntax.)

### W6 — Positioning: WebVTT ↔ grid (quantized); SRT default-anchors

- **Confirmed (T.E.):** because a cue's content is a `Screen`, placement is already `Row.Index`
  (1–15) and `Run.Column` (0–31). The mapping to/from format positioning happens **at the serializer
  edge**:
  - **WebVTT:** `line:` ⇄ `Row.Index` (1–15); `position:`/`align:` ⇄ `Run.Column`/indent. Quantized
    to the coarse grid ⇒ **round-trip is approximate**, accepted.
  - **SRT:** no standard positioning ⇒ 608→SRT **drops** placement (renders bottom-centered);
    SRT→608 uses a **default bottom-center anchor**.
  - **Position-less WebVTT** cues → same default anchor.
- **No SRT positioning extensions** are invented (no `{\anX}`/coordinate hacks) — keeps SRT to its
  portable common denominator.

### W7 — Overlapping cues merged by position (text→608)

- **Confirmed (T.E.):** WebVTT/SRT allow multiple cues on screen at once; 608 has exactly **one**
  displayed screen. At each cue **start/end boundary**, the target `Screen` = the **union of all
  currently-active cues' Screens**, placed by their positions; **same-row conflicts resolved by cue
  order (later cue wins that row)**. That target is handed to the core's existing
  `diff(current,target)→[]Token` engine, which re-flips the pop-on caption whenever the active set
  changes. Faithful ("both visible"), and it **reuses the diff engine** rather than adding new logic.

### W8 — Published, pluggable format seam

- **Confirmed (T.E.):** `TimedCue` (and the `cta608.Screen` it wraps) are **public**, and the
  format layer is a small **`Reader`/`Writer` interface over `[]TimedCue`**. WebVTT and SRT ship
  in-tree implementing it; **TTML and third-party formats plug in with zero change to the 608↔cue
  mapping**. The shared cue model already *is* the seam — publishing the interface is the small extra
  step that names the extension point.

### W9 — Timing boundary: stop at timed tokens / Screens

- **Confirmed (T.E.):** the mapping layer is timed but **does not schedule frames**. Concretely:
  - **text→608:** cues → merge (W7) → wall-time-tagged **token transitions** (a timed token/Screen
    stream). It stops there.
  - **608→text:** timed byte-pairs from decode/carriage → timed **displayed-Screen** changes →
    cues (W3).
- **Frame scheduling** — mapping the timed token stream onto specific frames with per-frame
  `cc_count`, field cadence, and null-pair frame alignment — is a **separate shared layer, the same
  one the wall-clock generator (#7) needs**, not part of #9. (See "Open / hand-off".)

### W10 — No split; one design note resolves #9

- **Confirmed (T.E.):** the shared-cue architecture makes this **one cohesive design**, not
  separable →608 / 608→ or per-format pieces. This note resolves #9. As the map is **planning-only**,
  implementation is left to the downstream `/implement` effort.

### Inherited / not re-decided here

- **Un-encodable characters:** UTF-8 text with runes outside 608's repertoire (basic + special +
  extended Western-European tables, #3) is the **core `Serialize`'s** concern (#5 — wire concerns
  live in the serializer), inherited here, **not** re-litigated in the mapping.
- **Read-side format detection:** explicit `webvtt.Read`/`srt.Read`, plus an optional sniff (the
  `WEBVTT` magic header distinguishes WebVTT; SRT is header-less). A serializer detail.

---

## Types & API sketch (final placement decided in #10)

```go
import ("io"; "time"; "github.com/Eyevinn/go-608/…/cta608")

// ---- Shared timed-cue intermediate (public pivot, W2) ----
type TimedCue struct {
    Start, End time.Duration   // cue presentation window
    Content    cta608.Screen   // positioned styled rows — REUSES the core Screen
}

// ---- Pluggable format seam (W8) ----
type Reader interface { ReadCues(r io.Reader) ([]TimedCue, error) }
type Writer interface { WriteCues(w io.Writer, cues []TimedCue) error }
// in-tree implementors: webvtt.{Reader,Writer}, srt.{Reader,Writer}; TTML/others plug in later.

// ---- 608 -> text (W3) ----
// A timed sequence of displayed-screen states (from a Decoder driven by timed byte-pairs).
type TimedScreen struct { Time time.Duration; Screen cta608.Screen }
// Segment on displayed-screen change; empty screen = gap; dangling cue gets EndOpts.
func Segment(changes []TimedScreen, opts SegmentOptions) []TimedCue   // SegmentOptions: StreamEnd, DefaultDur

// ---- text -> 608 (W4, W7, W9) ----
type TimedTokens struct { Time time.Duration; Tokens []cta608.Token }
// Merge overlaps by position -> target Screen per boundary -> diff -> timed token transitions.
func Compile(cues []TimedCue) []TimedTokens
// then the SHARED scheduler (with #7): []TimedTokens --schedule--> per-frame cc_data --carriage(#6)-->
```

## Composition

```
608 -> text:
  cc_data/SEI --carriage(#6)--> timed byte-pairs --cta608.Decoder--> []TimedScreen
    --Segment(W3)--> []TimedCue --{webvtt|srt}.Writer(W5,W6)--> .vtt / .srt file

text -> 608:
  .vtt / .srt file --{webvtt|srt}.Reader(W5,W6)--> []TimedCue
    --Compile: merge overlaps(W7) -> diff engine(W4)--> []TimedTokens
    --[shared scheduler, with #7]--> per-frame cc_data --carriage(#6)--> SEI
```

WebVTT/SRT are **siblings of SCC (#8) and the SEI carriage (#6)** — all are containers the core owns
the 608 semantics for. The difference: SCC/SEI are **byte-pair-exact**; WebVTT/SRT are a **lossy
semantic mapping** through the `Screen` (palette/grid quantization, W5/W6), which is why they pivot
on `Screen`/`TimedCue` rather than on raw byte-pairs.

## Open / hand-off

- **Shared timed-tokens → per-frame scheduler** (`cc_count`, field cadence, frame alignment): needed
  by **both** #7 (wall-clock generation) and #9 (compiled subtitles). #7 designed it *for the
  live-clock generator specifically*; a **generic** scheduler for arbitrary timed token streams is a
  cross-cutting concern to place in the **package decomposition (#10)**. Flagged on the map's
  *Not yet specified*.
- **TTML mapping** — future fog behind the W8 seam.
