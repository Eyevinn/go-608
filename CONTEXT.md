# CONTEXT — go-608 ubiquitous language

Glossary for the CTA-608 caption library. Terms only — no implementation details or decisions
(those live in `docs/design/` and `docs/adr/`). Established during the domain-model grilling (#5).

## Structure

- **Token stream** — the canonical in-memory form of 608 data: an ordered sequence of typed 608
  commands and character data. The spine both encode and decode pivot on. Serializes to/from
  `cc_data` byte pairs.
- **Byte pair** — the on-the-wire unit: two 8-bit values (each odd-parity), carrying either one
  control command or up to two characters.
- **Screen** — the sparse rendered display state: a collection of **Rows**, not a fixed 15×32 grid.
  Derived from the token stream (decode) and diffed against to produce tokens (encode).
- **Row** — one line of the screen: a row index plus a sequence of **character-runs**, and a
  `displayed` flag (see Displayed / Non-displayed memory).
- **Character-run** (run) — a maximal stretch of contiguous characters on a row that share one
  **Pen** (style). Style changes within a line start a new run.
- **Pen** — the styling/attribute state applied to characters: foreground **color**, **italic**,
  **underline**, and an optional **background** color (an extended-decoder feature). A small,
  comparable value. (Flash is not modeled.)

## 608 concepts

- **Field** — an NTSC line-21 data field: **field 1** and **field 2**. Carried in `cc_data` as
  `cc_type` 0 (field 1) and 1 (field 2).
- **Channel** — a logical caption/text service within a field. Captions **CC1–CC4** and text
  **T1–T4** map onto field × in-field-channel; the control byte's high nibble selects the in-field
  channel.
- **Caption mode** — how captions are painted onto the screen:
  - **Pop-on** — a full caption is built off-screen (non-displayed memory) and flipped on at once.
  - **Roll-up** — a window of 2, 3, or 4 rows; new text appears on the bottom row and older rows
    scroll up on a carriage return.
  - **Paint-on** — characters are written directly onto the displayed screen as they arrive.
  - **Text mode** — a separate full-screen text service, independent of captions.
- **Displayed / Non-displayed memory** — 608's double-buffered caption memory. What the viewer sees
  vs. what is being built. Modeled here as a `displayed` flag on each Row rather than two buffers.
- **PAC** (Preamble Address Code) — a control pair that sets a row's position (row + indent) and a
  base style (color/underline or italics) for what follows.
- **Mid-row code** — a control pair that changes style (color/underline/italics) partway along a
  row, starting a new character-run.
- **XDS** (eXtended Data Service) — field-2 metadata service (program/rating info), not caption
  text. **Not supported by the core:** decode skips it, encode never emits it.

## Timed-text I/O concepts (WebVTT / SRT / …)

- **Timed cue** (cue) — a discrete timed-text unit: a presentation window (start/end time) plus its
  displayed content. In go-608 a cue's content **is** a **Screen** (positioned styled rows), so a
  cue is "a Screen shown between two times". The pivot type for all timed-text formats.
- **Cue list** — an ordered sequence of timed cues; the in-memory form every timed-text format
  (WebVTT, SRT, later TTML) reads to and writes from. The 608↔cue-list mapping is written once; the
  formats are thin serializers of the cue list (a lossy, `Screen`-mediated sibling of the byte-exact
  SCC/SEI containers).
- **Format serializer** — the per-format `Reader`/`Writer` over a cue list. WebVTT and SRT ship
  in-tree; the seam is public so TTML and others plug in without touching the 608↔cue mapping.
