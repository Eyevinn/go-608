# mp4ff v0.54.0 — AV1 capability audit for CTA-608 carriage

Research note for wayfinder ticket #48 (map #46, *AV1 CTA-608 caption carriage*). Audits exactly what
`github.com/Eyevinn/mp4ff@v0.54.0` provides for carrying CTA-608 in AV1 (av01-in-MP4), and what
go-608 must add. Fed the wrap-vs-extend (#50), API-shape (#51), and mp4io-seam (#52) decisions.

## Outcome (2026-07-27) — both gaps are closed upstream

This is a **point-in-time audit of v0.54.0**, kept as the record of why #50 chose to extend mp4ff. The
two gaps it identifies were then filled in that repo and released in **mp4ff v0.55.0**, which go-608
now depends on. Read the audit below as history; read this block for what is true today.

| v0.54.0 gap | Closed by, in v0.55.0 |
|---|---|
| metadata-OBU payload build/parse | `av1.MetadataOBU{Type, Payload, PayloadHasTrailingBits}` + `ParseMetadataOBU`, `ParseMetadataOBUFromOBU`, `Encode`, `Size`, `MetadataType` constants |
| `metadata_itu_t_t35` body | `av1.ITUTT35{CountryCode, CountryCodeExtension, Payload}` + `ParseITUTT35`, `Encode`, `Size`, `MetadataOBU` |
| CTA-608 OBU create/parse | `av1.CreateCTA608MetadataOBU(ccData)`, `av1.ExtractCTA608(obus)`, `ITUTT35.ITUData()`, `ITUTT35.CTA608CCData()` |
| *(not a gap this audit found)* encode-side SEI creator | `sei.CreateCTA608SEIMessage`, `sei.CTA608ITUData`, `sei.CreateCTA608Payload`, `sei.ITUData.Encode`, `sei.ITUDataSize`; NAL wrap via `avc.CreateSEINalu` / `hevc.CreateSEINalu` |

Three consequences that change how the sections below read:

- **The boundary moved.** go-608 keeps only `BuildCCData` — the `cc_data()` structure. mp4ff owns
  everything from there to the wire, for AVC/HEVC *and* AV1, create and parse. So section D's
  "`t35Header` … reused verbatim" is superseded: that identifier no longer exists in go-608; the header
  is `sei.CTA608ITUData()`.
- **CEA-608 → CTA-608 rename** in mp4ff's `sei` package, no aliases kept. Old → new:
  `ParseCEA608` → `ParseCTA608`, `CEA608sei` → `CTA608sei`, `ExtractCEA608sei` → `ExtractCTA608sei`,
  `ITUData.IsCEA608` → `ITUData.IsCTA608`. Symbol names below use the current ones.
- **Section E is superseded.** `SpliceSEIBeforeVCL`, `SampleNALUs`, `PrefixNALUs` and `IsVCL` are no
  longer in `internal/mp4io` — they are exported from `carriage`, because every consumer needs them and
  `internal` put them out of reach. The audit's observation that they are NAL-framing-specific still
  holds, and is now an argument in #51 about API shape rather than a to-do.

## Verdict (as of v0.54.0)

**mp4ff covers both the AV1 OBU envelope and the av01 MP4 container completely. The only real gaps
are the `metadata_itu_t_t35` payload build/parse and the OBU-level per-frame splice — and both are
*smaller* than their SEI equivalents, because AV1 OBUs carry no emulation prevention.** The single
biggest reuse win: `sei.ParseCTA608` is envelope-free, so the cc_data decoder is shared verbatim with
the SEI path. On encode, `carriage.BuildCCData` and the 8-byte T.35/GA94 header are reused verbatim;
only the OBU envelope replaces the SEI/NAL envelope.

## Provided / partial / missing

| Capability | Where | Status | Notes |
|---|---|---|---|
| OBU header parse/build | `av1.ParseOBUHeader`, `av1.OBUHeader` | ✅ provided | incl. `OBUMetadata=5`, `OBUTemporalDelimiter=2`, `OBUFrame=6` |
| OBU split (sample → OBUs) | `av1.SplitOBUs` | ✅ provided | handles sized + trailing-unsized OBUs |
| OBU serialize (sized) | `av1.OBU.Encode`, `.Size`, LEB128 helpers | ✅ provided | always sets `obu_has_size_field=1` |
| LEB128 read | `av1.ReadLEB128` | ✅ provided | write is via `OBU.Encode` (unexported `appendLEB128`) |
| No emulation prevention | `av1` (documented) | ✅ provided | raw payload in/out — no EBSP escaping |
| **metadata_type + `metadata_itu_t_t35` payload** | — | ❌ **missing** → **closed in v0.55.0** | Was: `SplitOBUs` gives raw `OBU.Payload`, so the metadata body was go-608's job. Now `av1.MetadataOBU` / `av1.ITUTT35` / `av1.CreateCTA608MetadataOBU` |
| av01 sample entry + `av1C` | `mp4.StsdBox.Av01`, `mp4.Av1CBox`, `mp4.TrakBox.SetAV1Descriptor` | ✅ provided | decode + encode + init-segment build |
| Sample expansion (frag mp4) | `mp4.Fragment.GetFullSamples`, `FullSample.Data` | ✅ provided | codec-generic; a sample's `Data` is the raw OBU temporal unit (mp4ff does **not** split it) |
| **cc_data() parse** | `sei.ParseCTA608([]byte)` | ✅ **provided, envelope-free** | takes the bytes *after* the 8-byte T.35 header; not bound to SEI framing — reuse directly for AV1 |
| T.35/GA94 identity check | `sei.ITUData` / `IsCTA608` | ✅ provided | matched go-608's then-local `t35Header` exactly; that identifier is now gone and `sei.CTA608ITUData()` is the single source |
| cc_data() build | `carriage.BuildCCData` | ✅ provided (go-608) | codec-free — reused verbatim |
| T.35/GA94 header bytes | ~~`carriage.t35Header`~~ → `sei.CTA608ITUData()` | ↪ **moved upstream** | identical for OBU and SEI, which is why #50 moved it into mp4ff |
| itu_t_t35 → OBU wrap | `carriage.SEIMessage`/`NALU` | ❌ SEI-specific → **provided in v0.55.0** | `av1.CreateCTA608MetadataOBU` for AV1; `sei.CreateCTA608SEIMessage` + `avc`/`hevc.CreateSEINalu` for SEI, which `carriage` now delegates to |
| av01 track detection | `mp4io.VideoTrack` | ❌ **AVC/HEVC only** | switches on `stsd.AvcX`/`HvcX`; needs an `stsd.Av01` branch |
| Per-frame splice | `carriage.SpliceSEIBeforeVCL`/`SampleNALUs`/`PrefixNALUs`/`IsVCL` (was `internal/mp4io`, now exported) | ❌ NAL-specific | still true: 4-byte length framing + NAL VCL detection, so AV1 needs OBU analogs (`av1.SplitOBUs` / `OBU.Encode` supply the framing half) |
| Fragment rewrite engine | `mp4io.SpliceFragmented`, `Samples`, `SEIFunc` | 🟡 structurally reusable (unchanged) | seq/styp/timing/flags/CTO preservation is codec-generic; only the splice call + `SEIFunc`'s return type (NAL→OBU) are codec-bound |

## A. AV1 OBU envelope — `github.com/Eyevinn/mp4ff/av1`

Fully sufficient for the envelope. `av1/obu.go` provides:

- `OBUType` constants including `OBUMetadata` (5), `OBUTemporalDelimiter` (2), `OBUFrameHeader` (3),
  `OBUFrame` (6).
- `OBUHeader{Type, ExtensionFlag, HasSizeField, TemporalID, SpatialID, HeaderSize}` +
  `ParseOBUHeader`.
- `OBU{Header, Payload}` with `Size()` and `Encode()`. `Encode` always emits
  `obu_has_size_field=1` (header byte(s) + `obu_size` LEB128 + payload) — the form MP4 samples use.
- `SplitOBUs([]byte) ([]OBU, error)` — inverse of `Encode`; drops the size field, returns
  `Payload` slices into the input. Works on "an av1C configOBUs field, a coded sample, or a full
  temporal unit."
- `ReadLEB128`; write via `OBU.Encode` (the `appendLEB128`/`leb128Len` helpers are unexported, so
  go-608 constructs OBUs through `OBU.Encode` rather than hand-rolling LEB128).
- Documented: **"AV1 OBUs carry no emulation-prevention bytes."** So encode is
  `payload → OBU.Encode` with no escaping, and decode is `SplitOBUs → raw payload` — simpler than the
  SEI RBSP/EBSP dance.

**Gap (v0.54.0):** no metadata-OBU *payload* handling. `SplitOBUs` yields `OBU.Payload` = everything
after the header+size, i.e. `metadata_type` (LEB128) + `metadata_itu_t_t35()` + any trailing byte, and
building or parsing that body was left to the caller (prepend `metadata_type = 4` = `0x04`, then the
T.35 payload; and honor the metadata-OBU `trailing_bits`/byte-alignment rule — exact bytes were #47's
job).

**Closed in v0.55.0.** `av1.MetadataOBU` handles `metadata_type` + payload + `trailing_bits` (including
the reverse scan for the last non-zero byte, AV1 §6.7.1), `av1.ITUTT35` handles the T.35 body and the
`0xff` country-code extension byte, and `av1.CreateCTA608MetadataOBU` / `av1.ExtractCTA608` are the
thin CTA-608 layer over them. Callers supply `cc_data()` and nothing else.

## B. av01 MP4 container — `github.com/Eyevinn/mp4ff/mp4`

Fully sufficient. `box.go`/`boxsr.go` register `"av01"` → `DecodeVisualSampleEntry` and `"av1C"` →
`DecodeAv1C`. `StsdBox` exposes `Av01 *VisualSampleEntryBox` (alongside `AvcX`/`HvcX`), and
`VisualSampleEntryBox.Av1C *Av1CBox` holds the config (`av1.CodecConfRec`). `TrakBox.SetAV1Descriptor`
builds an av01 init segment; CENC support exists (`newAV1ProtectorFactory`). Sample bytes come through
`Fragment.GetFullSamples` → `FullSample.Data` exactly as for AVC/HEVC — **mp4ff hands the raw sample
(the OBU temporal unit) without splitting it**; go-608 calls `av1.SplitOBUs` on it. Whether a sample
contains a temporal-delimiter OBU is preserved as-is (mp4ff neither adds nor strips) — the placement
rule is deferred to #52 (informed by #47).

## C. cc_data() parse reuse — `github.com/Eyevinn/mp4ff/sei` (key win)

`sei.ParseCTA608(payload []byte) (field1, field2 []byte, err error)` parses cc_data() per CTA-708-E
§4.3 from a **plain byte slice** — it is called internally as `ParseCTA608(sd.payload[8:])`, i.e. the
bytes *after* the 8-byte T.35 header, and takes no SEI type. **It is not bound to SEI framing**, so
the AV1 decode path is: `SplitOBUs` → find `OBUMetadata` whose payload after `metadata_type` starts
with the T.35/GA94 header → `sei.ParseCTA608(bytesAfterT35Header)`. `sei.ITUData`/`IsCTA608` gives the
same GA94 identity check go-608's `t35Header` encodes.

Only `DecodeUserDataRegisteredSEI`/`ExtractCTA608sei` are SEI-bound (they take `*SEIData`) — go-608
skips those and calls `ParseCTA608` directly. **Carry-over quirk:** `ParseCTA608` drops any pair whose
seven low bits are all zero (`sei.go:137`), so the `0x80 0x80` 608 null pair does not survive this
decode — identical to the SEI round-trip caveat already noted in `carriage/doc.go`; not a new problem.

## D. go-608 encode reuse — `carriage`

- `BuildCCData(field1, field2, ccCount)` — codec-free; **reused verbatim** for AV1. This is the one
  item here that survived intact, and it is now the *whole* of go-608's contribution to the wire.
- `t35Header` (`{0xb5,0x00,0x31,0x47,0x41,0x39,0x34,0x03}`) — the itu_t_t35 payload preamble is
  **byte-identical** to the SEI case. **Superseded:** because it is identical, #50 moved it upstream;
  the identifier no longer exists in go-608 and `sei.CTA608ITUData()` is the single source.
- `SEIMessage` (wrapped as `sei.NewSEIData`) and `NALU` (`sei.WriteSEIMessages` EBSP + NAL header) were
  **SEI-specific and not reusable**, and the AV1 analog was going to be
  `metadata_type(0x04) + t35Header + ccData` → `av1.OBU{…}.Encode()`. **Resolved:** #50 chose extend, so
  both envelopes are mp4ff's — `sei.CreateCTA608SEIMessage` + `avc`/`hevc.CreateSEINalu` for SEI,
  `av1.CreateCTA608MetadataOBU` for AV1 — and `carriage.SEIMessage`/`NALU` are now thin delegations to
  them, keeping their existing signatures.

## E. go-608 consumer — `internal/mp4io` (partly superseded)

The NAL-unit helpers named here **moved to `carriage` and are exported**: `SpliceSEIBeforeVCL`,
`SampleNALUs`, `PrefixNALUs`, `IsVCL`. They were unreachable from outside go-608, so all three
consumers had copied them. `SpliceFragmented`, `Samples`, `SEIFunc` and `VideoTrack` stay in `mp4io`.

- `VideoTrack` recognizes only `stsd.AvcX`/`stsd.HvcX` and errors otherwise → **needs an
  `stsd.Av01 != nil` branch**, which also forces the `Track.Codec` question (does `carriage.Codec`
  gain an AV1 value? — #51). *Still open.*
- `Samples` is codec-generic → reusable. *Unchanged.*
- `SpliceFragmented` is structurally codec-generic (per-fragment seq numbers, styp, per-sample
  timing/flags/CTO all preserved) **except** the splice call and the `SEIFunc` contract ("returns a
  bare SEI NAL unit"). For AV1 the return is an OBU and the splice is OBU-framed → generalize
  `SEIFunc`/`SpliceFragmented` or add an AV1 splice (a #52 decision). *Still open.*
- The framing helpers are 4-byte-length + NAL-type specific → AV1 needs OBU-framed analogs (split via
  `av1.SplitOBUs`, re-serialize via `OBU.Encode`, and an "insert before the frame OBU" rule instead of
  "before the first VCL NAL"). *Still open, and now measured:* the mp4 muxer strips temporal
  delimiters while IVF keeps them, so that rule must anchor on the first frame / frame-header OBU
  rather than on position-from-start. `SampleNALUs`/`PrefixNALUs` need **no** AV1 twins, since mp4ff
  already provides both halves of the OBU framing.
- **Not a concern after all:** `SampleInfo` carries decode-order position and decode time, and #54
  records that assigning payloads that way permutes captions on B-frame content. That is an **AVC and
  HEVC** bug; AV1 does not inherit it. Both av01 fixtures, hierarchical GOP included, have
  `CompositionTimeOffset = 0` on every sample, because AV1 reorders inside the bitstream (hidden frames
  + `show_existing_frame`) rather than in the container. One sample is one temporal unit is one
  displayed frame, so AV1 caption assignment is one payload per sample in sample order.

## Implications for downstream tickets

- **#50 (wrap vs extend) — decided: extend.** The reasoning below is what it was decided on. mp4ff
  already contained the reusable *primitive* (`sei.ParseCTA608`, envelope-free) and the OBU envelope,
  but no metadata-OBU payload layer; an "extend" would add an `av1`-package analog of the `sei` pattern
  operating on OBU payloads, and a "wrap" would keep that small amount of code in go-608's `carriage`.
  Extend won, and shipped in v0.55.0 as the layered facility in the table at the top — reusable for
  HDR10+ and timecode, not just captions.
- **#51 (API shape) — still open.** Encode diverges only at the envelope; `BuildCCData` is shared. The
  observation that "the AV1 surface needs an OBU return, not the `[]byte` NAL that `FrameSEINALU`
  returns" still stands, and the surface it applies to has since grown: `carriage` now exports five
  codec-keyed functions rather than two.
- **#52 (mp4io seam) — still open, narrowed.** `SpliceFragmented`'s engine is reusable; the splice
  primitive and `SEIFunc` return type are the codec-bound pieces. **Extraction is no longer part of it**
  — `av1.ExtractCTA608(obus)` does that, skipping non-caption metadata OBUs. What is left is where in
  the OBU sequence the caption OBU goes, plus the `stsd.Av01` branch and CENC.

## Sources

mp4ff **v0.54.0** — the version audited — at `$(go list -m -f '{{.Dir}}' github.com/Eyevinn/mp4ff)`:
`av1/obu.go`, `mp4/av1c.go`, `mp4/stsd.go`, `mp4/visualsampleentry.go`, `sei/sei4.go`.
go-608: `carriage/carriage.go`, `internal/mp4io/mp4io.go`.

For what the outcome block describes, see mp4ff **v0.55.0**: `av1/metadata.go`, `av1/cta608.go`,
`sei/sei4.go`, `avc/sei.go`, `hevc/sei.go`; and in go-608 `carriage/carriage.go`, `carriage/sample.go`.
The av01 fixture measurements are in `testdata/README.md`.
