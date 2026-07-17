# SCC (Scenarist_SCC) read/write (design note)

Design note for wayfinder ticket #8. SCC I/O on top of the `cta608` core (#5). References:
`../research/prior-art-608.md` §1.5 (media-tools `scc.py`) / §2.7 (SVTA `SccParser`),
`../research/normative-rules-608-708-a53.md` §5.5 (SCC format), and **SubtitleEdit** (GPL —
consult, don't copy).

## Decisions

### S1 — SCC is a byte-pair container, a sibling of the SEI carriage (#6)

- **Confirmed (T.E.):** the SCC package sits at the **byte-pair level**. It handles the text file
  structure + timecodes only; the `cta608` core (#5 `Parse`/`Serialize`) owns *all* 608 semantics.
  SCC never interprets 608 bytes — exactly the posture of the SEI carriage (#6), just a different
  container (text file at timecodes vs in-band SEI per frame).
- **In-memory model:** an ordered list of entries `{ Timecode; Pairs []byte }`, preserving the SCC
  line grouping (a line = start timecode + its run of byte-pairs, where pair[i] is at frame
  timecode+i). Line-preserving → **byte-exact round-trip**.
- **Read:** file → entries → flatten to per-frame timed byte-pairs → (optionally) core `Parse` →
  tokens/`Screen`. **Write:** timed byte-pairs → group into lines → format.

### S2 — Canonical time = integer frame number; true drop-frame for 29.97; PAL included

- **Confirmed (T.E.):** internal time is an **absolute integer frame number** (count from
  `00:00:00:00`). Exact, drop/non-drop-agnostic once parsed, no float drift. Timecode string ↔ frame
  number is the drop-frame-aware conversion; frame number ↔ `time.Duration` uses fps only when a
  caller needs a real time base (`frames × 1001/30000` for 29.97, `× 1/25` for PAL, etc.).
- **True SMPTE drop-frame (confirmed T.E.):** for **29.97** (and 59.94) implement real drop-frame —
  drop frame counts 0 and 1 at the start of every minute *except* every 10th minute — the correct
  conversion neither media-tools nor SVTA does (#3 gotcha). The `;` separator ⇔ drop-frame.
- **PAL included (confirmed T.E.):** frame rate is **configurable** — **25 (PAL)** and **29.97/30
  (NTSC)** are the primary targets; 50 / 59.94 / 60 configurable too. Drop-frame applies **only** to
  the fractional NTSC rates (29.97, 59.94); integer and PAL rates are always non-drop.

### S3 — Reader infers fps from the timecodes (best-effort), with override + fallback

- **SCC is sparse (T.E.):** it is an *event list* — a line appears only where a caption has byte-pairs
  (a burst of control+char pairs on successive frames), with gaps between captions; **not** one line
  per frame. So the reader normally sees only **line-start timecodes**, and the "watch the second
  tick frame-by-frame" signal only exists in the rare dense file.
- **Confirmed (T.E.):** the reader **infers frame rate from the timecodes** rather than demanding a
  parameter. Practical signals in sparse files: the separator (`;` ⇒ 29.97 drop) and the **max
  line-start `FF`** (`≥25` ⇒ 30-family; `30–49` ⇒ 50; `50–59` ⇒ 59.94/60; only-ever `≤24` ⇒ *likely*
  25/PAL).
- **Because SCC is sparse, ambiguity is common, not an edge case → override + fallback is the safety
  net.** A sparse file whose captions all start at `FF ≤ 24` with `:` is genuinely ambiguous
  (25 vs sparse-30); `30.00` vs `29.97` non-drop is indistinguishable in text. So the reader takes an
  **optional fps override** and falls back to **29.97** (NTSC default) when signals are insufficient.
  Accepts both `;` and `:` — handles media-tools (non-drop) and SVTA (drop) output alike.

### S4 — File model + dumb writer

- **File model (confirmed T.E.):** `SCCFile{ FPS float64; DropFrame bool; Entries []Entry }`,
  `Entry{ Frame int; Pairs []byte }` — `Frame` is an absolute frame number (S2); the file-level
  `(FPS, DropFrame)` make formatting deterministic and round-trip byte-exact. Read fills them from
  inference/override (S3); write takes them from config.
- **Dumb writer (confirmed T.E.):** the writer formats each `Entry` to one line
  (`timecode  pair pair …`) **verbatim** — no grouping, no one-pair-per-frame policy, no idle-gap
  logic. **The caller decides what pairs go on each line**, and may put **multiple pairs on one
  line / associated with one frame** if their use case needs it. Building sparse entries from a flat
  `Serialize` stream (grouping bursts, ending at idle gaps) is a separate, optional **helper** — not
  the writer's job.
- **Write default: drop-frame `;` for 29.97** (configurable), non-drop `:` for 25/30 (S3 interop).

### S5 — SCC API + flatten convention

- **Flatten convention (confirmed T.E.):** `TimedPairs` assigns **pair[i] → `Frame+i`** (standard
  SCC "successive frames" reading). Raw `Entry{Frame,Pairs}` always preserves exact bytes; only the
  flatten-for-core step imposes the successive-frame interpretation (so a caller who packs multiple
  pairs on a line still round-trips byte-exact via the raw entries).

```go
package scc // final name in #10

type Entry   struct { Frame int; Pairs []byte }          // absolute frame + raw pairs (verbatim)
type SCCFile struct { FPS float64; DropFrame bool; Entries []Entry }

// Read — infers FPS/DropFrame (S3); WithFPS(...) overrides; preserves lines exactly. Accepts ;/:
func Read(r io.Reader, opts ...ReadOption) (*SCCFile, error)
// Write — dumb: one Entry -> one line, verbatim (S4). Drop default for 29.97 via SCCFile.DropFrame.
func Write(w io.Writer, f *SCCFile) error

// Timecode (true SMPTE drop-frame, S2)
func FrameToTimecode(frame int, fps float64, drop bool) string
func TimecodeToFrame(tc string, fps float64) (frame int, drop bool, err error)

// Flatten for the core: pair[i] of an entry -> frame Entry.Frame+i
func (f *SCCFile) TimedPairs() []TimedPair                // TimedPair{ Frame int; Pair []byte }
// then feed cta608.Parse / a Decoder for tokens/Screen with per-frame timing.

// Helper (NOT the writer, S4): flat timed stream -> sparse Entries (bursts; new entry after idle gap)
func GroupPairs(pairs []TimedPair) []Entry
```

### Composition

```
SCC file --Read--> SCCFile{Entries} --TimedPairs--> []TimedPair --cta608.Parse--> []Token / Screen
[]Token --cta608.Serialize--> byte-pairs --(schedule to frames)--> GroupPairs --> Entries --Write--> SCC file
```

SCC is a **sibling of the SEI carriage (#6)**: both carry the same field byte-pairs; the core owns
608 semantics. SCC adds only the text format + true drop-frame timecode conversion (the correctness
the media-tools/SVTA references lack). Final package placement decided in #10; SubtitleEdit (GPL) is
a behaviour cross-check only.
