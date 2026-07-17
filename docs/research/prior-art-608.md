# Prior-art research: CEA-608 caption implementations

Design-research input for `github.com/Eyevinn/go-608` (CEA-608 encode + decode + `cc_data`/SEI carriage).
This document extracts *reusable design knowledge* from the actual source of three prior implementations,
plus the carriage seam already provided by `mp4ff`. It is written for the engineer who will design the
Go domain model next.

Every non-obvious claim is cited. Remote citations are GitHub blob permalinks pinned to a commit; local
citations are `path:line`. Line numbers for the Python and TypeScript files were read from raw files
downloaded from the pinned commits below, so `file:line` matches the permalink.

Pinned commits used for permalinks:
- DASH-IF media-tools: `28561749d42252d12ee1d65afcfdc4c6fb3ece8f` (default branch `master`)
- SVTA common-media-library: `85e17dbd6eeb6815b4a02c391143ce88b8e24549` (default branch `main`)
- dash.js (integration layer): `44253abd45b3a9a261d37a0a6766cefae3c7db2d` (branch `development`)
- dash.js original `externals/cea608-parser.js`: tag `v4.7.4`
- DASH-IF livesim2: `1738fef49ec76472eadd80dd7b0005f0928c25d7`

## 0. TL;DR source inventory and lineage

| Subject | What it actually is | Encode? | Decode? | Carriage? |
|---|---|---|---|---|
| media-tools `cea608.py` | CEA-608 **decoder** (byte pairs -> screen model -> WebVTT) | no | yes | no |
| media-tools `sccgen.py` | CEA-608 **encoder** (`.ccs` mnemonics -> `.scc` byte pairs) | **yes** | no | no |
| media-tools `scc.py` | SCC file reader (`SccParser`) + writer (`SccWriter`) | scc I/O | scc I/O | no |
| media-tools `cea708.py` | CEA-708 DTVCC service-block skimmer (context only) | no | partial | no |
| SVTA `libs/608` (TypeScript) | The dash.js `cea608-parser.js` decoder, refactored into modules | no | yes | yes (SEI/`cc_data`) |
| dash.js `src/streaming/text/*` | Integration layer that drives the SVTA parser | no | glue | glue |
| `mp4ff/sei` (Go, local) | SEI type-4 -> raw field-1/field-2 byte pairs | no | no | **yes** |
| livesim2 | DASH live source simulator — **no CEA-608 at all** (see 4) | - | - | - |

Key lineage fact: dash.js no longer ships `externals/cea608-parser.js`. `MediaPlayer.js` now imports
`Cta608Parser` from the npm package `@svta/common-media-library`
(`/Users/tobbe/proj/github/ev/dash.js/src/streaming/MediaPlayer.js:78`). The SVTA `libs/608` module is a
direct, attributed port of the DASH-IF file — every file header says so, e.g.
`Row.ts:3-5` cites `Dash-Industry-Forum/dash.js/blob/development/externals/cea608-parser.js`. So "the
DASH-IF cea608.js decoder" and "the SVTA TypeScript decoder" are the *same algorithm*; the SVTA copy is the
maintained one and is the primary reference here. The media-tools `cea608.py` decoder is an independent,
slightly older Python sibling with the *same architecture* but a few meaningful differences (parity, buffer
width, control-code coverage) called out throughout.

`Cea608` vs `Cta608`: CEA-608 was renamed CTA-608 when CTA absorbed CEA. The two names denote the same
spec; SVTA uses `Cta608*`, media-tools/mp4ff use `Cea608`/`CEA608`.

---

## 1. DASH-IF media-tools (Python)

Path prefix (remote): `https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/`
Local downloaded copies read for this report live under the session scratchpad as `mt_*.py`.
The code is Python 2 (`print` statements, `has_key`, `1L`, `unicode()`), so it is a *reference*, not a
drop-in.

### 1.1 Decoder `cea608.py` — in-memory model

Grid constants: `NR_ROWS = 15`, `NR_COLS = 32` (`cea608.py:35-36`). The screen is exactly 15x32.

Object graph (bottom-up):
- `PenState` (`cea608.py:186-221`): `foreground` (str color name), `underline`, `italics`,
  `background` (str), `flash` (bool). Defaults: white on black, everything off (`cea608.py:195-200`).
- `Utf8Char` (`cea608.py:224-259`): a `uchar` string plus a `PenState`. This is the per-cell attribute
  record — char + full pen state per cell (not a bitfield).
- `Row` (`cea608.py:262-363`): `uchars[NR_COLS]`, a cursor `pos`, and a *current* `currPenState` that is
  applied to characters as they are inserted. Note `move_cursor` with `rel_pos > 1` paints the skipped
  cells with the current pen state (`cea608.py:309-311`) — tab/indent carries styling.
- `CaptionScreen` (`cea608.py:366-483`): `rows[NR_ROWS]`, `curr_row` (0-based, starts at 14),
  `nr_roll_up_rows`.
- `Cea608Channel` (`cea608.py:487-645`): holds `displayed_memory`, `nondisplayed_memory`, a `write_screen`
  pointer, `mode`, and an `outputFilter`. This is one caption channel (CC1..CC4).
- `Cea608FieldProcessor` (`cea608.py:659-918`): owns **two** `Cea608Channel`s (`cea608.py:666`),
  `current_channel`, `last_cmd`, and per-field statistics. `add_data((b0,b1), time)` is the entry point.

Modes: the string enum `("MODE_ROLL-UP","MODE_POP-ON","MODE_PAINT-ON","MODE_TEXT")` (`cea608.py:490`).
Text mode is explicitly *not* rendered (`cc_TR`/`cc_RTD` set the mode but comment "Text mode not
supported", `cea608.py:583-591`).

Channels vs fields: a *field* (NTSC line-21 field 1 or 2) carries *two* channels. `Cea608FieldProcessor`
is per-field and instantiates channel 1 and channel 2 (`cea608.py:666`). In `cc_data`, `cc_type==0` is
field 1, `cc_type==1` is field 2 (see 8). Within a field the control-byte high nibble selects the channel
(`0x1x` = ch1, `0x1x` with +8 = ch2), decoded in `parse_char`/`parse_cmd` (`cea608.py:872-877`,
`cea608.py:752-755`).

XDS: **not handled.** Field-2 class codes `0x01..0x0F` fall through every parser and hit the
"Couldn't parse" warning (`cea608.py:737`). Same in SVTA. If go-608 wants XDS it must design it fresh.

### 1.2 Mode machinery (pop-on / roll-up / paint-on)

- **Pop-on** (`cc_RCL`): `set_mode("MODE_POP-ON")` points `write_screen` at `nondisplayed_memory`
  (`cea608.py:519-520`). `cc_EOC` (End Of Caption) *swaps* displayed and non-displayed memory
  (`cea608.py:610-622`) — the classic double-buffer flip.
- **Roll-up** (`cc_RU(n)`): sets `write_screen = displayed_memory`, mode roll-up, and records `n`
  (`cea608.py:566-572`). `cc_CR` (Carriage Return) calls `roll_up()` (`cea608.py:599-603`).
- **Paint-on** (`cc_RDC`): writes directly to `displayed_memory` and flushes on every char insert
  (`cea608.py:534-536`).

Roll-up base-row math (**worth copying carefully**, `cea608.py:457-467`):
```
top_row_index = curr_row + 1 - nr_roll_up_rows
top_row = rows.pop(top_row_index)   # remove the top window row
top_row.clear()
rows.insert(curr_row, top_row)      # reinsert (blank) at base row -> everything shifts up one
```
`set_pac` clamps the base row so the roll-up window never runs off the top:
`if new_row < nr_roll_up_rows-1: new_row = nr_roll_up_rows-1` (`cea608.py:434-437`).

### 1.3 Parity (media-tools does it properly)

`PARITY_CHECK_TABLE = (0,1,1,0,1,0,0,1,1,0,0,1,0,1,1,0)` — a 16-entry nibble popcount-parity table
(`cea608.py:649`). `odd_parity_check(byte)` returns true iff the high-nibble parity != low-nibble parity
(`cea608.py:651-655`), i.e. the whole byte has odd parity. The decoder **drops any pair where either byte
fails odd parity** (`cea608.py:706-708`) before masking to 7 bits (`cea608.py:709`). This is stricter than
the SVTA/dash.js decoder, which ignores parity entirely (see 2.3).

### 1.4 Encoder `sccgen.py` — the only encode-side source found

`sccgen.py` (`Assembler` class) is the CEA-608 **encoder**: it reads a `.ccs` mnemonic file and writes a
`.scc` byte-pair file. This is the most valuable encode reference in the whole survey.

Character encoding (`print_uchar`, `sccgen.py:219-241`) — inverse of the decode table via
`utf8_to_byte = {v:k for byte_to_utf8}` (`sccgen.py:61`):
- `byte < 0x80`: emit a single code (buffered, paired with the next char) (`sccgen.py:223-225`).
- `0x80 <= byte < 0x90` (special chars, hi=`0x11`): emit pair `(0x11, byte-0x50)`, **no filler**
  (`sccgen.py:226-228`).
- `0x90 <= byte < 0xB0` (extended set, hi=`0x12`): **emit a filler char `0x23` ('#') first**, then
  `(0x12, byte-0x70)` (`sccgen.py:229-233`).
- `0xB0 <= byte` (extended set, hi=`0x13`): filler `0x23` then `(0x13, byte-0x90)` (`sccgen.py:234-238`).
The filler exists because extended chars auto-backspace on the decoder (see 6); the comment says so:
"All symbols include automatic BS, so we must insert extra char" (`sccgen.py:230-231`). Using `#` as the
fallback glyph is a documented shortcut ("not always optimal", `sccgen.py:232`); a real encoder should pick
a sensible ASCII approximation of the accented letter.

Byte-pair packing (`sccgen.py:243-267`):
- `print_code` buffers a lone char in `buffer_code`; the next char completes the pair (`sccgen.py:243-249`).
- `print_pair` first flushes any dangling buffered char (`handle_dangling_char`, `sccgen.py:251-255`) —
  so a control code forces the pending single char out, padded with a `0x00` low byte
  (`dangling_pair = (buffer_code, 0)`, `sccgen.py:253`). After parity that `0x00` becomes `0x80`.
- Every emitted byte goes through `fix_parity` (`sccgen.py:257-260`): `if not odd_parity: byte ^= 0x80`.

**Control-code doubling is deliberately disabled** in the encoder: `sccgen.py:207` and `sccgen.py:212`
both carry a commented-out repeat with the note "This repeat was important for analog transmission. Not
needed any longer." Design signal: for SEI/file carriage, do *not* double control codes by default; make
it an option.

PAC encode table (`pac_row_codes`, `sccgen.py:62-65`) — 1-based row -> `(byte1, byte2_base)`, channel 1:
```
row  1 (0x11,0x40)   row  2 (0x11,0x60)   row  3 (0x12,0x40)   row  4 (0x12,0x60)
row  5 (0x15,0x40)   row  6 (0x15,0x60)   row  7 (0x16,0x40)   row  8 (0x16,0x60)
row  9 (0x17,0x40)   row 10 (0x17,0x60)   row 11 (0x10,0x40)   row 12 (0x13,0x40)
row 13 (0x13,0x60)   row 14 (0x14,0x40)   row 15 (0x14,0x60)
```
PAC color/underline: `color_code = 2*colors.index(color)`; `byte2 = base + color_code + underline`
where `colors = (white,green,blue,cyan,red,yellow,magenta,italics)` (`sccgen.py:66,160-165`). PAC indent:
`byte2 = base + 0x10 + indent/2 + underline`, indent must be a multiple of 4, <= 20 (`sccgen.py:146-153`).
Midrow encode: `(0x11, 0x20 + 2*colorIndex + underline)` (`sccgen.py:169-178`). Background encode:
`(0x10, 0x20 + 2*bgIndex (+1 if semi))`, transparent = `(0x17,0x2d)`, and a filler glyph is inserted first
(`sccgen.py:182-196`). Black foreground: `(0x17, 0x2e (+1 underline))` (`sccgen.py:197-203`).

### 1.5 SCC file handling (`scc.py`)

Format reference the authors used: `theneitherworld.com/mcpoodle/SCC_TOOLS/DOCS/SCC_FORMAT.HTML`
(`scc.py:5`).

Reading (`SccParser`, `scc.py:40-85`):
- First line must equal `"Scenarist_SCC V1.0"` (`scc.py:54`).
- Then repeating: one blank line, one data line (`scc.py:55-63`). Data line is
  `HH:MM:SS:FF<whitespace>hhhh hhhh ...` where each `hhhh` is a 4-hex-digit byte pair
  (`scc.py:73-81`).
- Note: `SccParser` forwards the raw timecode *string* as the sort key to `add_data`; it does not itself
  convert to seconds.

Writing (`SccWriter`, `scc.py:87-160`) — the timecode generator worth noting:
- `calc_time_string(pts)` (`scc.py:111-126`) converts a 90 kHz PTS to `HH:MM:SS:FF` assuming exactly
  30 Hz: `frames = remainder / 3000` (90000/30) and emits with **all-colon (non-drop) separators**
  (`"%02d:%02d:%02d:%02d"`, `scc.py:126`). So media-tools *writes* non-drop 30fps SCC.
- `DataSorter` (`scc.py:173-202`) keeps `(time, [pairs])` sorted with a trailing "overlap" window
  (default 5) held back before flush, to absorb B-frame reordering. Same pattern is reused by the CEA-708
  parser. This is a good idea for go-608's SEI-extraction path (decode order != display order).

### 1.6 `cea708.py` (context only)

`Cea708Parser` (`cea708.py:39-151`) skims DTVCC. It documents the `cc_type` semantics that go-608's
carriage layer must respect: `cc_type==3` starts a DTVCC packet, `cc_type==2` continues it
(`cea708.py:88-91`); `cc_type` 0/1 are the CEA-608 fields handled elsewhere. Out of scope for go-608's
608 core but relevant if the same `cc_data` demux is shared.

---

## 2. DASH-IF cea608.js decoder (SVTA `common-media-library/libs/608`, TypeScript)

Path prefix (remote): `https://github.com/streaming-video-technology-alliance/common-media-library/blob/85e17dbd6eeb6815b4a02c391143ce88b8e24549/libs/608/src/`
Original file this was ported from (still fetchable at the tag):
`https://github.com/Dash-Industry-Forum/dash.js/blob/v4.7.4/externals/cea608-parser.js`.

### 2.1 Module decomposition (a ready-made package layout)

The SVTA split is a good template for Go package boundaries:

| File | Responsibility | Go analogue |
|---|---|---|
| `Cta608Parser.ts` | byte-pair demux + command/PAC/midrow/char dispatch for one field's 2 channels | `Decoder` / `FieldDecoder` |
| `Cta608Channel.ts` | one channel: mode state machine, displayed/non-displayed buffers | `Channel` |
| `CaptionScreen.ts` | 15-row screen; PAC placement, roll-up, background insert | `Screen` |
| `Row.ts` | one row of cells; cursor, insert, backspace, tab paint | `Row` |
| `StyledUnicodeChar.ts` | one cell: rune + `PenState` | `Cell` |
| `PenState.ts` / `PenStyles.ts` | per-cell style struct | `Pen` / `Style` |
| `PACData.ts` | decoded PAC (`row,indent,color,underline,italics`) | `PAC` struct |
| `SccParser.ts` | SCC reader | `scc` reader |
| `utils/*.ts` | the lookup tables (rows, chars, background colors), parity-free helpers | `internal` tables |
| `extractCta608DataFromSample.ts`, `findCta608Nalus.ts`, `extractCta608Data.ts`, `utils/seiHelpers.ts` | SEI/`cc_data` carriage | `cc_data` / `sei` package |

Data model specifics (same shape as media-tools, TS types):
- `PenStyles` (`PenStyles.ts:8-14`): `foreground: string|null`, `underline`, `italics`,
  `background: string`, `flash`.
- `StyledUnicodeChar` (`StyledUnicodeChar.ts:46-76`): `uchar` (default `' '`) + `PenState`.
- `PACData` (`PACData.ts:6-12`): `{row, indent: number|null, color: string|null, underline, italics}`.
- `CaptionModes` (`CaptionModes.ts`): union `'MODE_ROLL-UP' | 'MODE_POP-ON' | 'MODE_PAINT-ON' | 'MODE_TEXT' | null`.
- `Channels` (`Channels.ts`): `0 | 1 | 2`. `SupportedField` (`SupportedField.ts`): `1 | 3` — the parser is
  built for field 1 (CC1/CC2) or field 3-labelled (CC3/CC4); constructor builds channels
  `[null, ch(field), ch(field+1)]` (`Cta608Parser.ts:70-77`).
- `CueHandler` output seam (`CueHandler.ts:8-12`): `newCue(start,end,screen)`, optional `reset()`,
  optional `dispatchCue()`. The channel calls `newCue` only when the displayed screen changed vs the last
  emitted screen (`Cta608Channel.ts:289-317`). This dirty-diff cue emission is a good model for go-608's
  decode output.

### 2.2 Screen / row differences vs media-tools

- **`NR_COLS = 100`** (`utils/NR_COLS.ts`), not 32; `NR_ROWS = 15` (`utils/NR_ROWS.ts`). The row buffer is
  100 wide so out-of-bounds writes are tolerated rather than dropped (`Row.ts:61-65`, `Row.ts:138-160`).
  media-tools uses a hard 32 and logs+drops overflow (`cea608.py:323-325`). Design choice for go-608:
  32 visible columns, but pick a buffer policy (clamp vs wide buffer) deliberately.
- SVTA `CaptionScreen.setPAC` adds **roll-up carry-over** logic that media-tools lacks: when a PAC moves the
  base row in roll-up mode, it copies the previous `nrRollUpRows` rows from `lastOutputScreen` into place if
  they were already shown (`CaptionScreen.ts:140-168`). media-tools' `set_pac` just moves the cursor
  (`cea608.py:431-444`). If you port the base-row handling, port SVTA's, not media-tools'.
- Roll-up itself is identical splice logic: `topRowIndex = currRow + 1 - nrRollUpRows`, splice out, clear,
  splice back at `currRow` (`CaptionScreen.ts:205-219`).

### 2.3 Parity: SVTA/dash.js ignores it

`Cta608Parser.addData` masks each byte with `& 0x7f` and never validates parity
(`Cta608Parser.ts:90-91`). There is no parity table anywhere in `libs/608`. `(0,0)` pairs are skipped as
padding (`Cta608Parser.ts:100-102`). Contrast media-tools 1.3 and mp4ff (which preserves parity bits).
Implication for go-608: decide the policy explicitly — validate-and-drop (media-tools), or strip-and-trust
(dash.js). Encoding must always *set* odd parity (sccgen 1.4).

### 2.4 Control-code doubling: SVTA is the cleaner model

At the top of `addData`, for any byte where `a` is in `0x10..0x1F` (a control code), `hasCmdRepeated(a,b)`
compares against the last stored command; if identical, the pair is dropped and the history is reset to
`null` so a *third* identical pair would be processed (`Cta608Parser.ts:115-130`,
`utils/hasCmdRepeated.ts`, `utils/setLastCmd.ts`). Non-control pairs reset the history
(`Cta608Parser.ts:146-148`). This single, uniform doubling rule covers commands, PACs, midrow and
background alike. media-tools instead handles doubling *only* inside `parse_cmd` and `parse_pac`
(`cea608.py:746-749`, `cea608.py:821-823`) — see 9 (media-tools bug).

### 2.5 Dispatch order and command tables

`addData` tries, in order: `parseCmd` -> `parseMidrow` -> `parsePAC` -> `parseBackgroundAttributes` ->
`parseChars` (`Cta608Parser.ts:132-165`). Identical order in media-tools (`cea608.py:716-724`).

- `parseCmd` (`Cta608Parser.ts:186-257`): global/misc commands. Trigger bytes
  `a in {0x14,0x1C,0x15,0x1D}` with `b in 0x20..0x2F`, or `a in {0x17,0x1F}` with `b in 0x21..0x23`
  (Tab Offset). `b` -> method map: `0x20 RCL, 0x21 BS, 0x22 AOF, 0x23 AON, 0x24 DER, 0x25/26/27 RU2/3/4,
  0x28 FON, 0x29 RDC, 0x2A TR, 0x2B RTD, 0x2C EDM, 0x2D CR, 0x2E ENM, 0x2F EOC`
  (`Cta608Parser.ts:200-248`; command bodies in `Cta608Channel.ts:156-262`). Channel from `a`:
  `0x14/0x15/0x17 -> ch1`, else ch2 (`Cta608Parser.ts:197`).
- `parseMidrow` (`Cta608Parser.ts:266-296`): `a in {0x11,0x19}`, `b in 0x20..0x2F`. Interpreted in
  `ccMIDROW` (`Cta608Channel.ts:264-287`): `underline = b&1`; `italics = b >= 0x2E`; if not italics
  `foreground = [white,green,blue,cyan,red,yellow,magenta][b/2 - 0x10]`, else white.
- `parsePAC` (`Cta608Parser.ts:305-333`): `a in 0x11..0x17 or 0x19..0x1F` with `b in 0x40..0x7F`, or
  `a in {0x10,0x18}` with `b in 0x40..0x5F`. Row comes from four lookup tables (2.6). Second byte decoded
  in `interpretPAC` (`Cta608Parser.ts:342-380`): `idx = b-0x40` or `b-0x60`; `underline = idx&1`;
  `idx<=0x0D -> color = [white,green,blue,cyan,red,yellow,magenta,white][idx/2]`;
  `idx<=0x0F -> italics + white`; else `indent = ((idx-0x10)/2)*4`.
- `parseBackgroundAttributes` (`Cta608Parser.ts:444-472`): `a in {0x10,0x18}, b in 0x20..0x2F` ->
  background color `backgroundColors[(b-0x20)/2]` (+`_semi` if `b` odd); `a in {0x17,0x1F}, b==0x2D` ->
  transparent bg; `b==0x2E/0x2F` -> foreground black (+underline if `0x2F`). Applied via
  `setBkgData` which does backspace + setPen + insert-space (`CaptionScreen.ts:191-199`).
- `parseChars` (`Cta608Parser.ts:389-435`): channel from `a` (`a>=0x19 -> ch2, charCode1 = a-8`). If
  `charCode1 in 0x11..0x13` it is a **special/extended** char: `oneCode = b + 0x50` (hi 0x11) /
  `b + 0x70` (hi 0x12) / `b + 0x90` (hi 0x13) (`Cta608Parser.ts:402-424`). Else if `a in 0x20..0x7F`,
  1 or 2 basic chars (`[a]` if `b==0`, else `[a,b]`, `Cta608Parser.ts:425-427`).

### 2.6 Row (PAC) lookup tables — decode

Four tables map the PAC first byte to a 1-based row, split by channel and by `b`-range low/high
(`utils/rowsLowCh1.ts`, `rowsHighCh1.ts`, `rowsLowCh2.ts`, `rowsHighCh2.ts`). Channel-1 combined:

| PAC byte1 | b in 0x40..0x5F (low) -> row | b in 0x60..0x7F (high) -> row |
|---|---|---|
| 0x10 | 11 | (n/a) |
| 0x11 | 1 | 2 |
| 0x12 | 3 | 4 |
| 0x13 | 12 | 13 |
| 0x14 | 14 | 15 |
| 0x15 | 5 | 6 |
| 0x16 | 7 | 8 |
| 0x17 | 9 | 10 |

Channel 2 is the same rows with byte1 +8 (`0x18..0x1F`). This matches media-tools `rows_low_ch1` etc.
(`cea608.py:39-42`) and inverts sccgen's `pac_row_codes` (1.4). **Port these verbatim.**

### 2.7 SCC parser (SVTA)

`SccParser.parse` (`SccParser.ts:48-69`): optional header `"Scenarist_SCC V1.0"`; then every other line
must be blank, data lines are `timecode  hhhh hhhh ...` (`SccParser.ts:71-89`). `timeConverter`
(`SccParser.ts:91-100`) splits on `:` and expects the frame count after a **`;`** (drop-frame notation):
`(30*(3600*h + 60*m + s) + frames) * 1001/30000`. It returns 0 for any timecode without a `;`
(non-drop `HH:MM:SS:FF` -> `split(':')` length 4 -> falls through). See 10 for the resulting
interoperability gotcha with media-tools' non-drop writer.

---

## 3. Carriage layer (SEI / `cc_data`) — SVTA + mp4ff

### 3.1 mp4ff (local) — the seam go-608 will build on

`/Users/tobbe/proj/github/ev/mp4ff/sei/sei4.go`. mp4ff already parses SEI **type 4**
(`user_data_registered_itu_t_t35`) and, when it is CEA-608, hands back the raw field bytes:
- `ITUData.IsCEA608()` checks `countryCode==0xB5 && providerCode==0x31 && userIdentifier==0x47413934
  ("GA94") && userDataTypeCode==0x03` (`sei4.go:32-37`).
- `CEA608sei{ Field1 []byte, Field2 []byte }` (`sei4.go:88-93`).
- `ParseCEA608(payload)` (`sei4.go:118-150`): `ccCount = payload[0] & 0x1F`, skip a reserved byte, then
  `ccCount` triplets of `(flags, ccData1, ccData2)`; `ccValid = flags & 0x04`, `ccType = flags & 0x03`;
  keep pairs where `ccValid` set and `(ccData1&0x7f)+(ccData2&0x7f) != 0`; append to Field1 (`ccType==0`)
  or Field2 (`ccType==1`). **Parity bits are preserved** in the returned bytes (`sei4.go:133-135`).

So the seam mp4ff gives go-608 is: **two `[]byte` streams of concatenated 2-byte cc pairs (parity intact),
one per NTSC field, per access unit.** go-608's decoder therefore takes `(pts, []byte)` per field — exactly
the SVTA `addData(time, byteList)` shape. go-608 does *not* need its own SEI type-4 parser for the AVC/HEVC
mp4ff path; it needs the byte-pair -> screen decoder and the encode/`cc_data`-build direction.

### 3.2 What SVTA adds that mp4ff already covers (so don't duplicate)

For completeness (dash.js needs this because it has no Go mp4ff): `findCta608Nalus.ts` and
`extractCta608DataFromSample.ts` scan NAL units (AVC 1-byte / HEVC 2-byte / VVC 2-byte headers via
`extractNalUnitType`, `utils/seiHelpers.ts:86-133`), strip emulation-prevention `00 00 03`
(`utils/seiHelpers.ts:1-16`), find the type-4 SEI (`isCea608Sei`, `utils/seiHelpers.ts:18-44`, same magic
as mp4ff), and unpack `cc_data` into `fieldData[0]`/`fieldData[1]` (`extractCta608Data.ts:10-39`,
`utils/seiHelpers.ts:135-169`). The `cc_data` unpacking is byte-identical in logic to mp4ff's
`ParseCEA608`. **go-608 should reuse `mp4ff/sei` here and not re-implement NAL scanning.**

### 3.3 cc_data structure (canonical, cross-checked)

From mp4ff `ParseCEA608` (`sei4.go:118-150`) and SVTA `extractCta608Data`/`seiHelpers`
(`extractCta608Data.ts:10-39`, `utils/seiHelpers.ts:150-165`):
```
user_data_type_structure (after the 8-byte GA94/0x03 identifier):
  byte 0: [1 bit process_em_data_flag][1 bit process_cc_data_flag][1 bit additional_data_flag]
          [5 bits cc_count]          <- only cc_count (& 0x1F) is used
  byte 1: em_data / reserved         <- skipped
  then cc_count * 3 bytes:
    byte: [5 bits marker=0b11111][1 bit cc_valid][2 bits cc_type]   <- read cc_valid = &0x04, cc_type = &0x03
    cc_data_1 (with odd parity)
    cc_data_2 (with odd parity)
  trailing 0xFF marker byte
cc_type: 0 = NTSC line-21 field 1 (CC1/CC2), 1 = field 2 (CC3/CC4),
         2 = DTVCC packet data, 3 = DTVCC packet start
```
Padding pairs are `cc_valid==0` and/or `(0x80,0x80)` (i.e. `0x0000` after masking parity). The encode
direction (build `cc_data`) is *not present in any surveyed source* except implicitly as the inverse — go-608
must design it: pick `cc_count` (typically fixed per frame rate: e.g. 20 bytes -> `cc_count` derived per
CTA-708 for the video frame rate), interleave field-1/field-2 pairs, and pad with `0x8080` to fill unused
slots.

### 3.4 Frame-rate scheduling of byte pairs (decode side)

`Cta608Parser.addData` advances the timestamp per pair: `time = lastTime + 0.5*i*1001/30000`, where `i`
steps by 2, i.e. **each cc pair = one 29.97 Hz frame** (`Cta608Parser.ts:95-98`). The dash.js integration
layer feeds one field's pairs at their sample CTS (see 3.5). For encode, the inverse rule: emit exactly
2 bytes per field per video frame; a caption is spread across as many frames as it has pairs; unused frames
are `0x8080` padding.

### 3.5 dash.js integration layer (local, glue reference)

`/Users/tobbe/proj/github/ev/dash.js/src/streaming/text/TextSourceBuffer.js`:
- Two field parsers, one per NTSC field (`embeddedCea608FieldParsers[0..1]`, set up at
  `TextSourceBuffer.js:491-507`); each `new Cta608Parser(fieldNr, {newCue}, null)`.
- `_extractCea608Data` (`TextSourceBuffer.js:537-...`) pulls `cc_data` per sample via
  `extractCta608DataFromSample`, tags each with `sample.cts` plus a same-cts tiebreak index, and **sorts by
  CTS** (decode order != display order) (`TextSourceBuffer.js:547-573`).
- Then per field, `fieldParser.addData(cts/timescale, ccData)` (`TextSourceBuffer.js:476-484`).
- `newCue` turns a `CaptionScreen` into cues / HTML (`TextSourceBuffer.js:509-529`).
This confirms the pipeline go-608 should expose: `SEI/cc_data -> per-field byte streams (sorted by
composition time) -> per-field decoder (2 channels each) -> cue/screen callback`.

---

## 4. livesim2 — no CEA-608 support (confirmed)

The local `/Users/tobbe/proj/github/ev/livesim2` is **not** the DASH simulator: the git top-level is
`/Users/tobbe/proj/github/ev` (a `.git` sits there; go-608 has no own `.git`), and that `livesim2`
directory contains only `mws23/` and `rfp/` (2023 workshop material) — no `go.mod`, no README. So it was
ignored, and the real repo was inspected on GitHub instead.

Real repo `Dash-Industry-Forum/livesim2` @ `1738fef` full recursive tree was searched. Findings:
- **No path matches `608`, `cea`, or `caption` anywhere** in the tree.
- Subtitle support is WebVTT/TTML only: `cmd/livesim2/app/timesubs.go`, `timesubs_wvtt.go`, and
  `templates/stpptime.xml` / `stpptimecue.xml` (STPP/TTML). These generate timed *text* subtitles, not
  line-21 captions.
- `pkg/scte35/scte35.go` is SCTE-35 splice signalling (ad markers), unrelated to captions.
- The only `cc_data`-adjacent bytes are opaque SCTE-35 test vectors under
  `cmd/cmaf-ingest-receiver/app/testdata/awsMediaLiveScte35/`.

Conclusion: livesim2 offers **no reusable CEA-608 code**. It is, however, the natural downstream *consumer*
of go-608 (it already muxes fMP4/CMAF and could inject CEA-608 SEI), and its `timesubs*.go` +
`DataSorter`-style patterns are a reasonable style reference for a Go implementation.

---

## 5. Cross-cutting reference tables and rules

### 5.1 Character tables (three sets)

Both implementations model characters as "mostly ASCII with a small exception table," not a full 256-entry
LUT. Source of truth used here: media-tools `byte_to_utf8` (`cea608.py:76-175`) and SVTA
`specialCea608CharsCodes` (`utils/specialCea608CharsCodes.ts:4-109`) — they agree entry for entry.

- **Basic (modified ASCII), codes `0x20..0x7F`:** identical to ASCII *except* these 10 slots
  (`cea608.py:78-87`): `0x2A=á, 0x5C=é, 0x5E=í, 0x5F=ó, 0x60=ú, 0x7B=ç, 0x7C=÷, 0x7D=Ñ, 0x7E=ñ,
  0x7F=█ (full block)`. Everything else in `0x20..0x7F` is plain ASCII (decoder: `getCharForByte` returns
  `specialCea608CharsCodes[byte] || byte`, `utils/getCharForByte.ts:3-4`).
- **Special characters, internal codes `0x80..0x8F`** (transmitted as `0x11` + `0x30..0x3F`, decoder adds
  `+0x50`): `® ° ½ ¿ ™ ¢ £ ♪ à` (`0x89`=transparent space, rendered as regular space) `è â ê î ô û`
  (`cea608.py:91-106`).
- **Extended Western-European set A (Spanish/French), internal codes `0x90..0xAF`** (transmitted as `0x12`
  + `0x20..0x3F`, decoder adds `+0x70`): `Á É Ó Ú Ü ü ' ¡ * ' ━ © ℠ • " "` and accented capitals/lowers
  `À Â Ç È Ê Ë ë Î Ï ï Ô Ù ù Û « »` (`cea608.py:109-140`).
- **Extended Western-European set B (Portuguese/German/Danish), internal codes `0xB0..0xCF`** (transmitted
  as `0x13` + `0x20..0x3F`, decoder adds `+0x90`): `Ã ã Í Ì ì Ò ò Õ õ { } \ ^ _ | ∼ Ä ä Ö ö ß ¥ ¤ ┃
  Å å Ø ø ┏ ┓ ┗ ┛` (`cea608.py:143-174`).

The "internal code" is a flat 0..0xCF index the models use so that one `byte_to_utf8` table serves all
sets. **Port this flat table verbatim** (it is the single most error-prone thing to retype).

### 5.2 The extended-char "backspace-and-replace" mechanism

Extended chars (sets A/B, internal `>= 0x90`) are defined by the standard to be preceded by a
"standard-char" fallback and, when the extended char arrives, the receiver **backspaces over the fallback
and writes the extended glyph in its place**. This is implemented on both sides:
- Decode: `Row.insertChar` does `if byte >= 0x90: backSpace()` *before* writing (`Row.ts:138-142`;
  media-tools `cea608.py:319-321`). Note the threshold is `0x90`, so special chars (`0x80..0x8F`, set from
  `0x11`) do **not** backspace — only sets A/B do.
- Encode: `sccgen.print_uchar` emits a filler char (`0x23`) *before* the `0x12`/`0x13` pair for internal
  codes `>= 0x90`, but **not** for the `0x11` specials (`sccgen.py:226-238`). Symmetric with the decoder.

Gotcha for go-608: keep the `0x90` boundary consistent between encode and decode, and choose a better
fallback glyph than `#` (the encoder even admits `#` is "not always optimal", `sccgen.py:232`).

### 5.3 Control / PAC / midrow encoding summary

- Global commands: `(0x14|0x1C|0x15|0x1D, 0x20..0x2F)` (see 2.5 map) and Tab Offset
  `(0x17|0x1F, 0x21..0x23)`.
- PAC: first byte selects row+channel via the 4 tables (2.6); second byte `0x40..0x7F` encodes
  color/italics/underline/indent (2.5 `interpretPAC`). Rows are 1-based in the spec, 0-based in the buffers
  (`Cta608Parser.ts:379` note; `CaptionScreen.setPAC` does `row-1`, `CaptionScreen.ts:139`).
- Midrow: `(0x11|0x19, 0x20..0x2F)` color+underline+italics (2.5 `ccMIDROW`).
- Background/foreground-black: `(0x10|0x18, 0x20..0x2F)` bg color; `(0x17|0x1F, 0x2D..0x2F)`
  transparent/black (2.5).
- Colors (PAC/midrow foreground): `[white, green, blue, cyan, red, yellow, magenta]` then white/italics
  (`Cta608Parser.ts:361-370`). Background colors:
  `[white, green, blue, cyan, red, yellow, magenta, black, transparent]` (`utils/backgroundColors.ts`,
  `cea608.py:45`).

### 5.4 Parity and control-code doubling (consolidated)

- Odd parity on every transmitted byte (bit 7 = parity). Encode: set it (`sccgen.fix_parity`,
  `sccgen.py:257-260`). Decode: media-tools validates+drops (`cea608.py:706-708`), SVTA/dash.js ignores and
  masks (`Cta608Parser.ts:90-91`), mp4ff preserves the bits for the caller to decide (`sei4.go:133-135`).
  Recommendation: go-608 should (a) always set odd parity on encode, and (b) offer a decode option to
  validate vs strip.
- Control-code doubling: control codes are transmitted twice for analog robustness; the receiver must
  execute the command **once** and ignore the immediately-repeated identical pair. Canonical
  implementation = SVTA's uniform `hasCmdRepeated` gate over all `0x10..0x1F` pairs with history reset so a
  genuine third repeat is honored (`Cta608Parser.ts:115-130`). Encoders may omit doubling for digital
  carriage (sccgen disables it, `sccgen.py:207`).

### 5.5 SCC format (Scenarist Closed Caption)

- Header: `Scenarist_SCC V1.0` (`scc.py:54`, `SccParser.ts:52`).
- Body: blank line, then `timecode<TAB/space>hhhh hhhh ...` pairs, repeating (`scc.py:73-81`,
  `SccParser.ts:57-89`).
- Timecode: `HH:MM:SS:FF` (non-drop) or `HH:MM:SS;FF` (drop-frame; `;` before frames). Nominal 30 (29.97)
  fps: frame time = `1001/30000` s.
- **Drop vs non-drop is where the two DASH-IF codebases disagree** — see 10.

---

## 6. Shortlist for go-608

### 6.1 Port to Go (near-verbatim — these are pure data / well-tested logic)

1. **The flat character table** internal-code `0x00..0xCF -> rune` (5.1), from `cea608.py:76-175` /
   `specialCea608CharsCodes.ts`. Single biggest transcription risk; the two sources agree, so use them as a
   cross-check. Build both directions (`byte->rune` for decode, `rune->byte` for encode as in `sccgen.py:61`).
2. **PAC row tables** (2.6), both decode (4 maps) and the encode `pac_row_codes` (1.4). They are inverses;
   generate one from the other in a test.
3. **PAC second-byte interpret/encode** (`interpretPAC`, `Cta608Parser.ts:342-380`; sccgen
   `sccgen.py:138-168`): color/italics/underline/indent bit math.
4. **Midrow and background/black bit math** (`ccMIDROW` + `parseBackgroundAttributes`, and sccgen `MID`/
   `BKG`/`BLK`).
5. **Odd-parity nibble table + check** (`PARITY_CHECK_TABLE`, `cea608.py:649-655`) and
   `fix_parity` (`sccgen.py:257-260`).
6. **Roll-up splice math** (`CaptionScreen.ts:205-219` / `cea608.py:457-467`) and the SVTA roll-up
   base-row carry-over in `setPAC` (`CaptionScreen.ts:140-168`).
7. **The command dispatch order and trigger-byte ranges** (2.5) — copy SVTA's ranges (they include the
   `0x15/0x1D` variants media-tools misses; see 9).

### 6.2 Design fresh for Go idioms

1. **Cell/pen representation.** Both sources store a full `PenState` object per cell. In Go prefer a small
   value struct `type Cell struct { R rune; Pen Pen }` with `Pen` a comparable value struct (color as a
   typed enum, not a string) so `==` works and there is no aliasing. Colors as `int`/enum, not `"white"`.
2. **Row buffer width.** Choose deliberately: media-tools' strict 32 vs SVTA's 100-wide tolerance. A 32-col
   visible model with explicit overflow handling is cleaner than a magic 100.
3. **Output seam.** Replace the callback `CueHandler`/`outputFilter` with a Go channel or an interface
   returning immutable screen snapshots; the diffing in `Cta608Channel.outputDataUpdate`
   (`Cta608Channel.ts:289-317`) is worth keeping as behavior but should return values, not mutate shared
   `lastOutputScreen`.
4. **The encode `cc_data` builder.** Nothing surveyed builds `cc_data`/SEI (only mp4ff *parses* it, sccgen
   builds bare SCC pairs). go-608 must design: byte-pair scheduler (2 bytes/field/frame, `0x8080` padding,
   correct `cc_count` for the frame rate), field-1/field-2 interleave, then hand to `mp4ff/sei` for SEI
   wrapping. This is net-new and the main engineering surface.
5. **XDS.** Absent everywhere. Add only if required; design as a separate field-2 state machine
   (class/type codes `0x01..0x0F`).
6. **Drop-frame timecode.** Implement a real SMPTE drop-frame <-> frame-count converter (neither source
   does true drop-frame accounting — see 10) if SCC round-tripping matters.

### 6.3 Known bugs / gotchas spotted in the sources

1. **media-tools misses `0x15`/`0x1D` control codes.** `parse_cmd` only accepts `a in (0x14,0x1C)` for
   global commands (`cea608.py:743`), whereas SVTA accepts `0x14,0x1C,0x15,0x1D`
   (`Cta608Parser.ts:189`). Streams that use the alternate control-code channel bytes (common) would be
   mis-decoded by media-tools. Use SVTA's ranges.
2. **media-tools double-drops only cmd/PAC, not midrow/background.** Doubling is checked in `parse_cmd`
   (`cea608.py:746`) and `parse_pac` (`cea608.py:821`) but *not* in `parse_midrow` or
   `parse_background_attributes`. A legitimately-doubled midrow/background pair would be applied twice
   (e.g. two spaces inserted by `set_bkg_data`). SVTA's top-level `hasCmdRepeated` avoids this
   (`Cta608Parser.ts:115-130`). Adopt the uniform gate.
3. **SCC drop/non-drop mismatch between the two DASH-IF tools.** media-tools `SccWriter` writes non-drop
   all-colon `HH:MM:SS:FF` at exactly 30 fps (`scc.py:126`), but the SVTA `SccParser.timeConverter` only
   parses drop-frame `;` timecodes and returns 0 for a non-drop line (`SccParser.ts:91-100`). So SVTA
   cannot correctly time SCC files that media-tools produced. go-608 should accept both notations and treat
   the separator as the drop/non-drop flag.
4. **Neither does true drop-frame math.** Both just compute `frames_total*1001/30000`
   (`SccParser.ts:97`) — they do not add back the frames skipped by drop-frame numbering (2 per minute
   except every 10th). For frame-accurate SCC this is wrong by up to ~3.6 s/hour.
5. **Extended-char boundary must match on both sides.** Decode backspaces at internal code `>= 0x90`
   (`Row.ts:139`); encode inserts a filler for `>= 0x90` (`sccgen.py:229`). Specials (`0x80..0x8F`) must
   *not* backspace. Off-by-one here corrupts every accented capital.
6. **PAC indent inherits color from the neighboring cell.** `setPAC` with an indent overrides
   `pacData.color` with the previous cell's foreground (`CaptionScreen.ts:172-177`;
   `cea608.py:439-443`). Surprising but intentional; replicate to match reference output.
7. **Parity policy divergence** (5.4): three different behaviors across the three code bases. Pick one and
   document it; don't silently inherit dash.js's "ignore parity."
8. **Text mode is unimplemented** in both decoders (`cea608.py:583-591`; `Cta608Channel.ts:210-220` just
   `setMode`). If go-608 claims text-mode support it is greenfield.
9. **`parseChars` accepts `a` up to `0x7F` only after control/PAC checks fail**; a byte with the high bit
   still set (unmasked) would break the `0x20..0x7F` test — always mask parity before dispatch (SVTA masks
   in `addData`, media-tools masks after the parity check). Keep masking centralized.

---

## 7. Sources (everything actually read)

Remote (GitHub, pinned commits):
- media-tools `cea608.py` — https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/cea608.py
- media-tools `sccgen.py` — https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/sccgen.py
- media-tools `scc.py` — https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/scc.py
- media-tools `cea708.py` — https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/cea708.py
- media-tools `example.ccs` — https://github.com/Dash-Industry-Forum/media-tools/blob/28561749d42252d12ee1d65afcfdc4c6fb3ece8f/python/dash_tools/example.ccs
- media-tools `cea608towebvtt.py`, `sample_tables.py`, `generate_ccs.py` (skimmed for context) — same tree prefix
- SVTA `libs/608/src/Cta608Parser.ts` — https://github.com/streaming-video-technology-alliance/common-media-library/blob/85e17dbd6eeb6815b4a02c391143ce88b8e24549/libs/608/src/Cta608Parser.ts
- SVTA `Cta608Channel.ts`, `CaptionScreen.ts`, `Row.ts`, `PenState.ts`, `PenStyles.ts`, `PACData.ts`, `StyledUnicodeChar.ts`, `CaptionModes.ts`, `Channels.ts`, `SupportedField.ts`, `CmdHistory.ts`, `CueHandler.ts`, `SccParser.ts`, `index.ts` — same tree prefix `.../libs/608/src/`
- SVTA `utils/`: `getCharForByte.ts`, `specialCea608CharsCodes.ts`, `rowsLowCh1.ts`, `rowsHighCh1.ts`, `rowsLowCh2.ts`, `rowsHighCh2.ts`, `backgroundColors.ts`, `NR_COLS.ts`, `NR_ROWS.ts`, `hasCmdRepeated.ts`, `setLastCmd.ts`, `createCmdHistory.ts`, `numArrayToHexArray.ts`, `seiHelpers.ts` — `.../libs/608/src/utils/`
- SVTA carriage: `extractCta608Data.ts`, `extractCta608DataFromSample.ts`, `findCta608Nalus.ts` — `.../libs/608/src/`
- dash.js original decoder (lineage) — https://github.com/Dash-Industry-Forum/dash.js/blob/v4.7.4/externals/cea608-parser.js
- livesim2 tree (searched, no 608) — https://github.com/Dash-Industry-Forum/livesim2/tree/1738fef49ec76472eadd80dd7b0005f0928c25d7

Local files:
- `/Users/tobbe/proj/github/ev/mp4ff/sei/sei4.go` (SEI type-4 -> CEA-608 field bytes)
- `/Users/tobbe/proj/github/ev/dash.js/src/streaming/text/TextSourceBuffer.js` (integration/scheduling)
- `/Users/tobbe/proj/github/ev/dash.js/src/streaming/MediaPlayer.js` (import of `@svta/common-media-library` `Cta608Parser`)
- `/Users/tobbe/proj/github/ev/dash.js/src/streaming/constants/Constants.js` (`ACCESSIBILITY_CEA608_SCHEME`)
- `/Users/tobbe/proj/github/ev/livesim2` (verified NOT the DASH simulator — 2023 workshop material only)

## 8. Additional reference implementations (added after #2, surfaced by T.E.)

- **SubtitleEdit** — https://github.com/SubtitleEdit/subtitleedit (C#/.NET, GPL). A mature,
  actively-maintained subtitle editor/converter with **CEA-608 decode** and **SCC (Scenarist)
  read/write** support, plus conversion to/from many formats including **WebVTT**. Relevant as a
  cross-check for:
  - the core 608 char/PAC/control tables and decode behaviour (this doc);
  - **SCC format** handling — header, drop/non-drop timecode, byte-pair lines (ticket #8);
  - **WebVTT ↔ 608** styling/positioning mapping (ticket #9).
  Note the GPL license — consult for algorithms/behaviour, do **not** copy code into the
  (MIT-licensed) go-608.
</content>
</invoke>
