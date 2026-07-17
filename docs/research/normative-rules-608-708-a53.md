# Normative rules for CEA-608 encoding & carriage

Design-research input for `github.com/Eyevinn/go-608`. This document extracts the rules that
constrain a CEA-608 **encoder** and the **carriage** of `cc_data` in AVC/HEVC SEI, straight from
the governing standards, so the domain-model (#5) and carriage-seam (#6) tickets rest on
requirements rather than guesswork. Its companion is
[`prior-art-608.md`](./prior-art-608.md), which extracted the same tables from *implementation
code*; here we give the **normative backing** and add the carriage/timing rules the code survey
did not cover (the per-frame-rate `cc_count` budget and padding).

## Source access

Read in full from the user's **licensed** copies (iCloud `…/Standards/`), converted to text with
`pdftotext -layout`. These are copyrighted standards; this file cites section/table numbers and
extracts only the design-relevant values rather than bulk-copying tables.

| Spec | File | Used for |
|---|---|---|
| **ANSI/CTA-608-E (2019)** | `CTA/ANSI-CTA-608-E-S-2019-Final.pdf` | 608 char sets, PAC/mid-row/control tables, parity, doubling, XDS, field/channel |
| **ANSI/CTA-708-E R-2018** | `CTA/ANSI-CTA-708-E-R-2018-Final.pdf` | `cc_data()` syntax (§4.3), **`cc_count` per frame rate (§4.3.6)**, padding (§4.3.5) |
| **ANSI/SCTE 128-1 2020** | `SCTE/ANSI_SCTE 128-1 2020.pdf` | AVC SEI carriage: `user_data_registered_itu_t_t35` → `ATSC1_data()` → `cc_data()` |

**Not available locally:** ATSC **A/53 Part 4** (the ticket named it; only Parts 1/2/3/5/6 are in
the collection). Its role — MPEG-2 carriage and the `cc_data` pointer — is covered here by
CTA-708-E (which *defines* `cc_data()`) and SCTE 128-1 (which defines the AVC SEI wrapper). Flagged
as a cross-check the reader may still want, but not on the critical path: CTA-708-E is the
normative home of the `cc_data()` structure and the `cc_count` rule regardless of codec.

---

## 1. `cc_data()` structure — CTA-708-E §4.3

Syntax (CTA-708-E R-2018 §4.3, **Table 2**, `cta-708-e-r2018.txt:800`):

```
cc_data() {
    reserved                 1     '1'
    process_cc_data_flag     1     bslbf
    additional_data_flag     1     '1'          (a.k.a. zero_bit / reserved in some texts)
    cc_count                 5     uimsbf
    reserved                 8     '11111111'   (em_data byte)
    for (i=0; i < cc_count; i++) {
        marker_bits          5     '11111'
        cc_valid             1     bslbf
        cc_type              2     bslbf
        cc_data_1            8     bslbf
        cc_data_2            8     bslbf
    }
    marker_bits              8     '11111111'
}
```

Semantics (§4.3, `:817`–`:877`):
- **`process_cc_data_flag`** — when `0`, the whole `cc_data()` shall be discarded (`:817`).
- **`cc_count`** — number of CC constructs that follow (5 bits, so 0–31); its value is a function
  of frame rate (see §3).
- **`cc_valid`** — `1` ⇒ the two data bytes are valid and interpreted per the 608/708 rules; `0` ⇒
  the two data bytes have no meaning **but `cc_type` still does** (`:832`).
- **`cc_type`** — **Table 3** (`:850`, "Closed-Caption Type (cc_type) Coding"):

  | `cc_type` | `cc_valid` | meaning |
  |---|---|---|
  | `00` | 1 | **CEA-608 line-21 field 1** caption bytes |
  | `00` | 0 | field-1 "do not generate run-in clock/start bit" (no 608 waveform) *or* DTVCC padding (context) |
  | `01` | 1 | **CEA-608 line-21 field 2** caption bytes |
  | `01` | 0 | field-2 "no 608 waveform" *or* DTVCC padding (context) |
  | `10` | 1 | DTVCC (708) packet **continuation** data |
  | `10` | 0 | DTVCC padding |
  | `11` | 1 | DTVCC (708) packet **start** (`cc_data_1` = CCP header, `cc_data_2` = CCP data) |
  | `11` | 0 | DTVCC padding |

**Ordering rule (§4.3.1, `:878`):** all CEA-608 constructs (`cc_type` 00/01) must appear **at the
beginning** of the `cc_data()` structure, before any DTVCC (708) constructs. A byte pair with
`cc_valid=0` and `cc_type=10`/`11` marks the **end of the 608 data** within the structure (§4.3.5,
`:1146`). This is byte-for-byte what `mp4ff/sei` `ParseCEA608` reads (see prior-art doc §3.3) — go-608
does **not** need its own `cc_data` parser for the mp4ff decode path; it needs the inverse
**builder**.

---

## 2. AVC/HEVC SEI carriage — SCTE 128-1 2020

The 608/708 `cc_data()` rides inside an SEI `user_data_registered_itu_t_t35` message (SEI
`payloadType == 4`). Chain (SCTE 128-1 §8, Tables 12–15):

```
SEI payloadType 4: user_data_registered_itu_t_t35() {   // Table 12, scte-128-1-2020.txt:818
    itu_t_t35_country_code    8    = 0xB5                 // :860  (USA)
    itu_t_t35_provider_code   16   = 0x0031               // :862  (ATSC, registered)
    user_identifier           32   = 0x47413934 "GA94"    // Table 13 :870 → ATSC1_data()
    ATSC1_data() {                                         // Table 14 :879
        user_data_type_code   8    = 0x03                 // Table 15 :891 → cc_data()
        user_data_type_structure() = cc_data()            // §8.2.2 :913
        marker_bits           8    = 0xFF
    }
}
```

- **§8.2.2 (`:913`):** "The contents of `cc_data()` **shall be as defined in CTA-708**." So the
  carriage is codec-agnostic at the payload level — the same `cc_data()` bytes are wrapped
  identically for AVC.
- **Table 8 SEI Constraints (`:579`):** the `user_data_registered_itu_t_t35` SEI message is
  **required** for carriage of AFD / closed captioning / bar data (`:590`, `:605`).
- This is exactly the magic `mp4ff/sei.ITUData.IsCEA608()` checks (`0xB5 / 0x0031 / "GA94" / 0x03`).
  **Confirmed identical.** go-608 should reuse `mp4ff/sei` for the SEI wrap/unwrap and own only the
  `cc_data()`↔byte-pair layer.

**HEVC gap / flag:** SCTE 128-1 is written for **AVC** (H.264). HEVC uses the *same* T.35/GA94/
`ATSC1_data`/`cc_data` payload inside an HEVC SEI (`mp4ff` already handles both header sizes — see
prior-art doc §3.2), but the *normative home* for HEVC caption carriage is elsewhere (e.g. ATSC
A/72 for AVC-in-ATSC, and for ATSC 3.0 the video-specific carriage docs; SCTE 128-1 itself is the
cable-AVC constraint). **Open item for #6:** cite the exact HEVC carriage spec if one is required
for conformance claims; functionally the payload is unchanged.

---

## 3. `cc_count` per frame rate — CTA-708-E §4.3.6 (THE key deliverable)

`cc_count` "varies depending on the frame rate and coding mode" (§4.3.6, `cta-708-e-r2018.txt:1150`).

**Formula (`:1169`–`:1178`):** given `r` = desired average rate of `cc_data_1`+`cc_data_2` in
bits/s, and `t` = ms since the last insertion opportunity:

```
cc_count = (r × t) / 16000        (round to nearest whole number)
```

For fixed-allocated-bandwidth systems, `cc_data()` is inserted at **every** opportunity (every
picture) and `r = 9600 bps` — **divided by 1.001 for the fractional rates 23.98 / 29.97 / 59.94**
(`:758`). Because `t` (the frame period) carries the *same* 1.001 factor, the fractional and
integer members of each family yield the **same** `cc_count`:

| Frame rate | `t` (ms) | `cc_count` | Source |
|---|---|---|---|
| 23.976 / 24 | 41.71 / 41.67 | **25** | Table 4/5 (24p) — informative, `:1203` / `:1229` |
| 25 | 40.00 | **24** | computed from §4.3.6 formula (PAL; not in ATSC tables) |
| 29.97 / 30 | 33.37 / 33.33 | **20** | Table 4/5 (30i/30p) — informative, `:1205` / `:1233` |
| 50 | 20.00 | **12** | computed from §4.3.6 formula (PAL; not in ATSC tables) |
| 59.94 / 60 | 16.68 / 16.67 | **10** | Table 4/5 (60p) — informative, `:1209` / `:1248` |

Worked example: 30 fps → `9600 × 33.333 / 16000 = 20`. 60 → `9600 × 16.667 / 16000 = 10`. 24 →
`9600 × 41.667 / 16000 = 25`. 25 → `9600 × 40 / 16000 = 24`. 50 → `9600 × 20 / 16000 = 12`.

> **Caveat:** the ATSC-oriented informative Tables 4 & 5 only list 24p/30i/30p/60p, so **25 and 50
> are formula-derived**, not table-confirmed. They are the universally-used PAL values and match the
> `cc_count × frame_rate ≈ 600 constructs/s` (= 9600/16) invariant, but if PAL conformance matters,
> confirm against the DVB/PAL carriage spec.

**3:2 pull-down:** 30i-with-pulldown alternates `cc_count` between **20 and 30** frame-to-frame
(Table 4 `:1206`, Table 5 `:1238`) — i.e. `cc_count` need not be constant across a sequence.

### 3.1 How many 608 pairs per frame (field scheduling)

CTA-708-E §4.3.6 (`:1180`): *"The rate of delivery of the CEA-608 datastream shall not exceed
`(60/1.001 × 2)` bytes per second"* ≈ **119.88 B/s** — i.e. the two legacy NTSC fields together.
This caps the number of **608** pairs per frame (the rest of the `cc_count` budget is DTVCC/708 or
padding):

- **≤ 30 fps** → room for **both** field-1 (`cc_type=00`) and field-2 (`cc_type=01`) pairs each
  frame (2 pairs = 4 bytes/frame; 4 × 29.97 = 119.88). Table 5 shows field-1+field-2 present.
- **60 fps** → room for **one** 608 pair per frame (2 bytes/frame; 2 × 59.94 = 119.88). Table 5's
  60p rows carry field-1 only.
- The rule "shall be met **across splices**" (`:1181`) — a splicer may drop a 608 pair at a cut.

**Design consequence for #6/#7:** the wall-clock generator emits, per video frame, up to one
field-1 608 pair and (≤30 fps) one field-2 608 pair placed first in `cc_data()`, then pads the
remaining `cc_count − (nПairs)` constructs.

### 3.2 Padding — CTA-708-E §4.3.5 (`:1141`)

- Construct-level padding = `cc_valid=0`, `cc_type=10`/`11`, recommended `cc_data_1=cc_data_2=0x00`
  (`:1148`).
- A leading `cc_valid=0, cc_type=00/01` pair is **not** padding — it explicitly says "no 608
  waveform for this field this frame" (`:1143`).
- **Distinct from 608-level null pairs:** to keep a 608 *field* alive without new caption content,
  encoders emit a **608 null pair `0x80 0x80`** (0x00/0x00 forced to odd parity) with `cc_valid=1`,
  `cc_type=00/01`. That is a valid 608 "no-op" byte pair, versus the `cc_valid=0` construct padding
  above. go-608 must not conflate the two. (See prior-art doc §3.3 — the `0x8080` padding it
  mentions is this 608-level null pair.)

**Open design question for #6/#7:** does go-608 emit the **full** per-rate `cc_count` (with DTVCC
padding constructs) at every frame — the safe, conformant "fixed-allocation" choice many decoders
expect — or a **minimal** `cc_count` (e.g. 2: just the two 608 pairs) when no 708 is present? Both
are legal; fixed-allocation is more interoperable, minimal is smaller. Decide explicitly.

---

## 4. Character sets — CTA-608-E (normative backing for the code tables)

CTA-608-E defines the glyphs the prior-art doc extracted from code. Confirmed present and matching:

- **Standard characters** — CTA-608-E **Table 50** (`cta-608-e-2019.txt:494`, Annex F.1.1.2). The
  modified-ASCII set (0x20–0x7F) with the ~10 substituted slots the code tables show.
- **Special characters** — **Table 49** (`:493`, Annex F.1.1.1). Transmitted as `0x11`+`0x30..0x3F`
  (internal 0x80–0x8F): `® ° ½ ¿ ™ ¢ £ ♪ à … û` and the **transparent space** (`:983`).
- **Extended Western-European sets** — **Tables 5–10** (`:442`–`:447`): Spanish, Miscellaneous,
  French (transmitted `0x12`+`0x20..0x3F`), and Portuguese, German, Danish (transmitted
  `0x13`+`0x20..0x3F`). §6.4.2 "Optional Extended Characters" (`:129`).

  > **Correction to note for #5:** these extended sets **are normative in CTA-608-E** (Tables
  > 5–10). They are absent only from **FCC 47 CFR §15.119**, which encodes the older analog *decoder*
  > requirement — hence an earlier CFR-based reading wrongly concluded "no extended sets." The
  > prior-art doc's code-derived "set A / set B" (Spanish-French / Portuguese-German-Danish) **do**
  > correspond to Tables 5–10. Cross-check byte values against Tables 5–10 when finalizing the port.

The extended-char **backspace-and-replace** behavior (prior-art doc §5.2) is the normative mechanism
behind these two-byte glyphs: extended chars are preceded by a standard-char fallback that the
decoder backspaces over. Encoder emits the fallback + the 2-byte extended code (see prior-art doc
§1.4 / §5.2 for the `>= 0x90` boundary).

---

## 5. PAC, mid-row, and control codes — CTA-608-E

### 5.1 Preamble Address Codes — CTA-608-E **Table 53** (`cta-608-e-2019.txt:5631`)

First byte selects **row + data channel**; second byte (`0x40–0x7F`) selects color/underline **or**
indent/underline. Verbatim first-byte→row map (channel 1 / channel 2):

| Row | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **Ch1 byte1** | 11 | 11 | 12 | 12 | 15 | 15 | 16 | 16 | 17 | 17 | 10 | 13 | 13 | 14 | 14 |
| **Ch2 byte1** | 19 | 19 | 1A | 1A | 1D | 1D | 1E | 1E | 1F | 1F | 18 | 1B | 1B | 1C | 1C |

Second byte (rows that share a first byte use the **0x40-range for the lower row, 0x60-range for the
upper**): White `40/60`, +underline `+1`; Green `42`, Blue `44`, Cyan `46`, Red `48`, Yellow `4A`,
Magenta `4C` (each `+1` = underline); **White Italics `4E`** (`+1` = italics+underline). Indents:
`50`=indent 0, `52`=4, `54`=8, `56`=12, `58`=16, `5A`=20, `5C`=24, `5E`=28 (each `+1` = underline).

- **Table 53 note (`:5630`):** *all indent codes assign **white** as the color attribute.* (This is
  the normative source of the prior-art doc's "PAC indent inherits/forces color" gotcha.)
- **Indent range = 0…28** (steps of 4). ⚠️ The media-tools `sccgen.py` encoder capped indent at
  ≤ 20 (prior-art doc §1.4) — that is a **tool limitation, not the spec**. go-608's encoder should
  allow 0–28.
- Confirms the four decode row-tables and the `pac_row_codes` encode table in the prior-art doc
  (§2.6 / §1.4). **Port Table 53 verbatim; it is the authoritative source.**

### 5.2 Mid-row codes — CTA-608-E **Table 51** (`:495`, Annex F.1.1.3 `:5530`)

Color + underline mid-row codes (`0x11`+`0x20..0x2F` for ch1). Matches the code-derived table
(prior-art doc §2.5 `ccMIDROW`): color index `(b−0x20)/2`, underline `b&1`, italics for the top
values. Cite Table 51 for the normative values.

### 5.3 Control codes

Miscellaneous control codes (RCL, BS, AOF, AON, DER, RU2/3/4, FON, RDC, TR, RTD, EDM, CR, ENM, EOC,
Tab Offset) and their channel/field encoding are normative in CTA-608-E §7/Annex F and match the
dispatch tables in prior-art doc §2.5. **Invalid on field 1:** control codes with first byte
`0x01–0x0F` "have no valid purpose on line 21, field 1 … Encoder manufacturers **shall not** insert
these" (§D.2, `:4663`) — they are field-2 (XDS) data.

---

## 6. Parity, doubling, and framing — the encoder rules (CTA-608-E §D.2, Normative/Regulatory)

**§D.2 "Transmission of Control Code Pairs"** (`cta-608-e-2019.txt:4649`) is the authoritative
encoder contract:

1. **Odd parity — mandatory.** *"All data shall be forced to odd parity"* (`:4653`). Every byte's
   bit 7 is the odd-parity bit (`:940`, `:2076`). XDS too (`:2149`).
2. **Control-code doubling — SHOULD, and disable-able.** *"each two-byte control code whose first
   character is in the range of 0x10 to 0x1F **should be transmitted twice** in successive frames"*
   (`:4651`). It is a *should*, not a *shall*; §D.2 further recommends encoders offer a switch to
   disable doubling, and that *"unnecessary doubling … **should be disabled as a default for field
   2**"* (`:4655`, and §B.14 `:4226`).
3. **Frame alignment — SHALL.** *"Null bytes shall be inserted as necessary to provide for frame
   alignment of two-byte control codes"* (`:4652`) — a control code's two bytes must sit in one
   frame's pair, so pad with a null pair when needed.

**Decoder side of doubling** (definition of "Immediately", `:916`): the command takes effect *"upon
receipt of **the first** of the redundant pairs, assuming it passes parity."* So the decoder acts on
the first valid copy and ignores the immediately-repeated identical pair. Matches the SVTA
`hasCmdRepeated` model (prior-art doc §2.4). Note §B.14's field-2 subtlety (`:4230`): because the
decoder collapses adjacent identical control pairs, to get **two** real CRs processed you must
encode **three** CRs.

**Encoder-default recommendation for #5/#6/#7:** always force odd parity; always frame-align with
null padding; make control-code doubling an **option, defaulted ON for field 1 / OFF for field 2**
(the §D.2 posture). Note the prior-art `sccgen.py` disabled doubling entirely ("not needed for
digital carriage") — reasonable for SEI/file output, but the spec's default for field 1 is doubled.

---

## 7. Field / data-channel structure & XDS

- **Fields:** line-21 **field 1** (`cc_type=00`) and **field 2** (`cc_type=01`). Each field carries
  two data channels; the first-byte high nibble selects the channel (prior-art doc §1.1).
- **Caption/text channels:** CC1–CC4 (captions) and T1–T4 (text) map onto field1/field2 × channel1/
  channel2.
- **XDS (eXtended Data Service):** field-2-only, using the control codes `0x01–0x0F` (CTA-608-E §9,
  `:66`, `:813`; odd parity `:2149`). It carries program/rating metadata (content advisory, TSID,
  etc.), **not caption text**. Out of the caption-core scope — see the map's "XDS in/out of scope?"
  fog item. If ever supported, it is a separate field-2 state machine.

---

## 8. Ambiguities & open questions for the design tickets

1. **`cc_count` policy (→ #6/#7):** emit the full per-rate `cc_count` with DTVCC padding
   (fixed-allocation, most interoperable) vs. minimal `cc_count=2` when 608-only. §3.2.
2. **25 / 50 fps `cc_count` (→ #6):** 24 / 12 are **formula-derived** (§4.3.6), not in the ATSC
   informative tables. Confirm against a PAL/DVB carriage reference if PAL conformance is claimed.
3. **HEVC carriage spec (→ #6):** SCTE 128-1 is AVC-only; the payload is identical for HEVC but cite
   the exact HEVC normative home (A/72 / ATSC-3.0 video docs) if conformance claims are made. §2.
4. **A/53 Part 4 not consulted:** its `cc_data` role is covered by CTA-708-E + SCTE 128-1; obtain it
   if an ATSC-MPEG-2 conformance statement is needed.
5. **Doubling on encode (→ #5):** spec default is doubled on field 1; digital-carriage practice
   (sccgen) omits it. Pick go-608's default and expose the switch. §6.
6. **608 null pair `0x8080` vs construct padding `0x0000` (→ #6):** two different "nothing here"
   encodings at two layers; keep them distinct. §3.2.
7. **Indent range 0–28 (→ #5):** honor the full Table 53 range, unlike sccgen's ≤20. §5.1.
8. **608-E (2019) vs older 608-B/-C:** the extended sets and PAC/mid-row tables are stable across
   these; the 2019 edition is the reference here. A newer **CTA-708-E S-2023** exists in the
   collection — not diffed; §4.3 `cc_data`/`cc_count` rules are not expected to have changed, but
   verify if a 2023 conformance claim is needed.

---

## 9. Sources

Local licensed copies (`~/Library/Mobile Documents/com~apple~CloudDocs/Standards/`):
- **ANSI/CTA-608-E S-2019** — `CTA/ANSI-CTA-608-E-S-2019-Final.pdf` (Tables 5–10, 49, 50, 51, 53;
  §6.4.2, §7, §9; §D.2; §B.14; Annex F; definitions §3)
- **ANSI/CTA-708-E R-2018** — `CTA/ANSI-CTA-708-E-R-2018-Final.pdf` (§4.3, §4.3.1, §4.3.5, §4.3.6;
  Tables 2, 3, 4, 5)
- **ANSI/SCTE 128-1 2020** — `SCTE/ANSI_SCTE 128-1 2020.pdf` (§7.2 Table 8; §8.1–§8.2, Tables
  12–15)
- (also present, not diffed: `CTA/ANSI CTA-708-E S-2023 …pdf`, `CC/CEA-608.pdf`, `CEA/CEA-608.pdf`,
  ATSC A/53 Parts 1/2/3/5/6)

Cross-referenced: `./prior-art-608.md`; `/Users/tobbe/proj/github/ev/mp4ff/sei/sei4.go`.

Not consulted / to obtain if needed: ATSC A/53 Part 4, ATSC A/72, PAL/DVB caption-carriage spec.
