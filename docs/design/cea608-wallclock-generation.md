# Wall-clock caption generation behavior (design note)

Design note for wayfinder ticket #7 — the first milestone livesim2 / moqlivemock consume. Validated
with a runnable prototype: [`.scratch/proto-wallclock/`](../../.scratch/proto-wallclock/) (`go run .`). Builds on the
`cta608` core (#5), the carriage seam (#6), and the consumer study (#4).

W1–W6 are the original decisions, all about the pop-on clock the prototype covers. **W7–W8 were
added later**, for the direct-write caption modes and for what a roll-up unit owes the unit before
it; they extend W4 rather than replace it, and neither is in the prototype.

## Decisions

### W1 — API drive model: pull-by-wall-time

The generator is driven **one call per video frame, with the frame's wall-clock time passed in**:
`NextFrame(frameWallMS int64)`. Not an internal clock. Rationale (#4): both consumers already
compute a per-sample wall-clock at the injection site (livesim2 `StartTimeS*1000 + DTS*1000/ts`;
moqlivemock epoch-anchored `DecodeTime` / `WallclockMS`), and passing it in is robust to gaps,
seeks, and VFR, and makes **drop-frame a non-issue** (the caller's wall time already accounts for it).

### W2 — Content: two centered lines, configurable

- **Row 14:** UTC time **RFC3339** (`2026-07-17T14:23:45Z`) — mirrors livesim2 `timesubs`.
- **Row 15:** a second line (media/segment time in the prototype), rendered **yellow**.
- Both **horizontally centered**. Config selects the lines (row, colour, content kind). One line is
  the minimum; the second is optional but on by default here.

### W3 — Granularity: per-second (v1)

The display refreshes **once per second** (second-precision, like `timesubs`). No sub-second
ticking in v1 (that would require diff-updating only the low digits — deferred).

### W4 — Mode & timing: pop-on, build-ahead, EOC-on-boundary (frame-accurate)

Pop-on. During second S the next second's screen is drawn into **non-displayed memory**; a single
**`EOC` on the last frame of second S** flips it on, so the clock shows second S+1 **exactly** at
S+1 — **frame-accurate, zero lag**. (The simpler build-at-boundary alternative lags ~0.4 s — rejected.)
Pop-on remains the **default**; W7 adds the two direct-write modes as options beside it.

### W5 — Cadence: 1 field-1 pair/frame, `cc_count` padding per fps

One 608 field-1 byte-pair per frame (2 B/field/frame cap); the rest of the per-frame `cc_count`
budget is DTVCC padding. `cc_count = round(600/fps)` → 30→20, 25→24, 29.97→20, 50→12, 60→10 (#3).
Field 2 unused by default (CC1 only). The ~23-pair two-line build **fits a 1-second refresh at all
target rates** (25/30/50/60); a guard flags **overrun** if longer content ever exceeds the budget.

### W6 — Centering & colour are real 608 lowering concerns (surfaced by the prototype)

- **Centering:** `PAC(row, indent)` (indent = multiple of 4) + **`Tab Offset`** (0–3) hits the exact
  start column, e.g. col 6 = indent 4 + tab 2.
- **Colour + centre interaction:** an **indented PAC is always white** (CTA-608-E Table 53), so a
  centred *coloured* line is `PAC(indent, white)` → `Tab Offset` → **`MidRow(colour)`** → chars; the
  mid-row code occupies a cell, shifting the text by one column (the generator compensates).

### W7 — Direct-write modes: the wire cadence *is* the animation

Pop-on (W4) is joined by the two modes that write to **displayed** memory, on both generation paths
(the per-frame `Generator` and the per-unit builders):

- **Paint-on** — `WithPaintOn()` / `BuildUnitPaintCues`. The second (or cue) opens with `EDM` + `RDC`
  on its first frame, then the positioned rows; the caption stands until the next clear.
- **Roll-up** — `WithRollUp(rows)` / `BuildUnitRollUpCues`. No clear: `CR` scrolls a 2–4 row window
  and the new line is written onto the base row, one scroll step per configured line, in `Row` order
  so the window ends up laid out as the rows declare.

**No new pacing machinery is involved, and that is the decision.** `Serialize` packs **two characters
per byte pair** and W5's one-pair-per-frame drain already spaces the pairs a frame apart, so a mode
that writes to the displayed screen reveals two characters per frame and a decoder renders each pair
as it arrives. *One* character per frame was rejected: it means padding every pair with a null,
halving the 608 rate for a difference few viewers would notice and putting the default two lines
(~40 pairs) outside a 1 s budget at 30 fps.

This does not revisit W3 — the **content** still refreshes once per second; only the reveal is
progressive. W5's budget tightens: paint-on's clear costs a pair (~23 pairs for the default two
lines, so 0.77 s of writing at 30 fps and 0.38 s at 60), and roll-up adds a mode entry per cue plus a
`CR` per line (**24 pairs — exactly the 25 fps budget**), making it the most expensive of the three.
The overrun guard reports the overflow as before (`Overran()`; a returned error in the per-unit
builders, which know their slice length up front).

### W8 — Roll-up across unit boundaries: reset by default, carry opt-in

Roll-up is the only mode that transmits **just the new line** and leaves the rows above it to the
decoder, so the per-unit builders must decide what a unit owes the unit before it.
`BuildUnitRollUpCues` **clears the window on the unit's first frame** (`EDM`) by default;
`WithRollUpCarry()` keeps it and scrolls the previous unit's lines instead.

Reset is the default because it is the only behaviour that is a **function of the unit alone** — the
property the per-unit API exists to provide, and the same reasoning that makes paint-on units
self-contained without needing a `WithFlipAtCueStart` counterpart. A receiver that joins, seeks or
starts at a unit then sees exactly what a continuously-running one sees. The cost is a window that
truncates at every boundary: with ~1 s cues it holds at most `unitDurMS/targetPeriodMS` lines, so a
2 s segment cannot fill a 4-row window at all. Carry restores broadcast behaviour and full window
depth, at the price of an order dependency — a joining receiver's window fills over `rows-1` cues,
and a seek shows the pre-seek lines ageing out over the same span. Both self-correct; only one is
provable from the unit.

The emitted **data** differs by exactly that one `EDM`, so a stateless per-segment server serves
either policy without tracking state. `RollUpOption` is deliberately a **separate type** from
`UnitOption`: `WithFlipAtCueStart` is a pop-on concept (there is no flip to move in roll-up) and a
compile error beats a runtime one.

**Validation (W7 + W8).** Both were checked against an independent decoder — ffmpeg's
`ccaption_dec` via `movie=…[out0+subcc]` over captioned H.264 — which shows the progressive reveal
under `-real_time` and distinguishes reset from carry by the single `EDM` pair at the unit boundary.
`go608-clock -mode` and `-unit-mode` drive both paths for exactly this purpose.

## Generator API (validated by the prototype)

```go
type LineSpec struct { Row int; Color string; Kind string } // Kind: "utc" | "media" | ...
type Config   struct { Lines []LineSpec }                    // default: row14 utc white, row15 media yellow

type Generator struct { /* fps, ccCount, per-row build/displayed state, field queue */ }

func NewGenerator(fps float64, cfg Config) *Generator

// NextFrame advances one video frame at frameWallMS and returns that frame's cc_data (via carriage
// #6 BuildCCData). The caller then wraps it with carriage SEINALU(ccData, codec) and inserts it.
func (g *Generator) NextFrame(frameWallMS int64) FrameOut   // FrameOut{ Field1 pair, CCData, Flip, Overrun, ... }
```

The shipped signatures differ — see [SPEC](../../SPEC.md) §4.4 for the contract: `NextFrame` returns
the `{field1, field2, ccCount}` triple and the caller wraps it with `carriage` (§P3.1), and
`NewGenerator` takes variadic `GeneratorOption`s for the W7 modes.

Composition across the stack:
```
[#5 cta608]  CaptionBlock/tokens → Serialize → field byte-pairs   (pure, timing-free)
[#7 here  ]  schedule per frame: build-ahead pop-on, 1 pair/frame, EOC on boundary  (owns wall-clock)
[#6 carriage] BuildCCData(f1,f2,ccCount) → SEINALU(ccData, codec) → bare NAL          (pure, wraps mp4ff)
[#4 consumer] prepend length, splice before first VCL NALU (into a copy, before CENC)
```

## Prototype

`.scratch/proto-wallclock/` — runnable throwaway (`go run .`): a portable `Generator` behind a
line-driven TUI. Shows, per frame, the emitted byte-pair + `cc_count` and an ASCII render of the two
centred rows building in non-displayed memory and flipping on the boundary. Sample (fps=30, after 1s):

```
screen (displayed):
  14 |      2026-07-17T14:23:45Z      |     (white)
  15 |         MEDIA 00:00:01         |     (yellow)

  f0   94 20  RCL     Resume Caption Loading
  f1   94 52  PAC     PAC row 14 indent 4 (white)
  f2   97 a2  TO      Tab Offset 2
  f3.. 32 b0  CHARS   "20" ...            (line builds one pair/frame)
  f29* 94 2f  EOC     End Of Caption (flip on the second boundary)
```

## Deferred / open

- **Sub-second ticking** (W3) — would diff-update low digits; revisit if needed.
- **Field 2 / CC-channel selection** — CC1/field-1 only by default (#6/#4).
- **Longer content** — the overrun guard flags builds that don't fit 1 s at a given fps; if hit,
  shorten lines, drop a line, or spread the refresh over >1 s.
- **Exact content formats & config surface** — finalized alongside the CLI tool in #10.
