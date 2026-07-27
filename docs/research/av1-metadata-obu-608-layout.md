# AV1 `metadata_itu_t_t35` OBU byte layout for CTA-608

Research note for wayfinder ticket #47 (map #46, *AV1 CTA-608 caption carriage*). Documents the exact
byte layout of a CTA-608-carrying AV1 metadata OBU, the specs that govern it, and the
`trailing_bits` gotcha — for byte-exact interop. Fed #50 (wrap vs extend), #51 (API shape),
#52 (placement).

## Outcome (2026-07-27)

**The byte layout below is unchanged and confirmed, but go-608 no longer builds these bytes.** #50
decided to *extend mp4ff* rather than wrap it, and that work shipped in
**mp4ff v0.55.0**: `av1.CreateCTA608MetadataOBU(ccData)` emits the complete OBU, and
`av1.ExtractCTA608(obus)` reads the field pairs back. go-608 keeps only
`carriage.BuildCCData` — the `cc_data()` structure — and nothing below it.

What that changes for a reader of this note:

- The **24-byte worked example** below is now asserted byte-for-byte by an mp4ff unit test, and a
  second test asserts the AV1 OBU payload and the AVC/HEVC SEI payload are identical for the same
  `cc_data()`. The "only the envelope differs" verdict is enforced upstream, not just documented here.
- The **T.35/GA94 header** is `sei.CTA608ITUData()` in mp4ff. It was `carriage.t35Header` when this
  note was written; that identifier was removed when `carriage` moved onto the mp4ff creators, so
  references below have been updated.
- The **`trailing_bits` rule** is implemented upstream, including a case this note did not reach: a
  payload byte *shared* with `trailing_bits` (which `metadata_timecode` can have) must not gain a
  second `trailing_one_bit` on re-encode. mp4ff tracks that with
  `MetadataOBU.PayloadHasTrailingBits`. For CTA-608 it never arises — the payload is byte-aligned.
- **CEA-608 was renamed to CTA-608** across mp4ff's `sei` API in v0.55.0 (`ParseCEA608` →
  `ParseCTA608`, `IsCEA608` → `IsCTA608`), with no aliases. Symbol names below use the current ones.
- The **deferred reference validation** (#49) is resolved — see *Deferred / open* at the end.
- **Placement within the temporal unit is still #52**, but narrower than this note assumed; the
  fixtures settled which frame a caption attaches to.

## Verdict

**The caption payload is byte-identical to the SEI path; only the envelope differs.** An AV1
CTA-608 OBU is `OBU_METADATA` → `metadata_type = ITUT_T35` → the *same* 8-byte T.35/GA94 header and
the *same* A/53 `cc_data()` that `carriage.SEIMessage`/`BuildCCData` already produce — wrapped in an
OBU header (+ `obu_size`) instead of a SEI message + NAL, with **no emulation prevention** and **one
trailing `0x80` byte** (the metadata-OBU `trailing_bits`). Everything after `metadata_type` is what
go-608 already builds.

## Nesting (outer → inner)

```
obu_header()            1 byte   : forbidden=0, type=OBU_METADATA(5), ext=0, has_size_field=1, reserved=0  → 0x2A
obu_size                leb128   : size of everything below (metadata_type + itu_t_t35 body + trailing byte)
─ metadata_obu() ─
  metadata_type         leb128   : METADATA_TYPE_ITUT_T35 = 4  → 0x04
  ─ metadata_itu_t_t35() ─
    itu_t_t35_country_code            f(8)    : 0xB5 (USA)            ┐
    itu_t_t35_terminal_provider_code  16 bits : 0x0031 (ATSC)        │  == sei.CTA608ITUData()
    itu_t_t35_terminal_provider_oriented_code 32 bits : 'GA94'       │  {b5 00 31 47 41 39 34 03}
    user_data_type_code               f(8)    : 0x03 (cc_data)       ┘
    user_data / cc_data()                     : A/53 Part 4 §6.2.2 == carriage.BuildCCData output
─ end metadata_obu() ─
  trailing_bits         : trailing_one_bit(1) + zero-pad to byte  → 0x80 (payload is byte-aligned)
```

`obu_header`, `obu_size`, `metadata_type` (leb128), `metadata_itu_t_t35`, and `trailing_bits` are all
from the **AV1 Bitstream & Decoding Process Specification** (open_bitstream_unit §5.2, metadata OBU
§5.8). `trailing_bits(obu_size*8 − payloadBits)` is invoked for every OBU type **except**
`OBU_TILE_GROUP`, `OBU_TILE_LIST`, `OBU_FRAME` — so a metadata OBU **always** carries it.

### Constants

| Symbol | Value |
|---|---|
| `OBU_METADATA` | 5 |
| `METADATA_TYPE_ITUT_T35` | 4 |
| `itu_t_t35_country_code` (US) | `0xB5` |
| `terminal_provider_code` (ATSC) | `0x0031` |
| user_identifier | `'GA94'` = `0x47413934` |
| `user_data_type_code` (cc_data) | `0x03` |

## Diff versus the SEI path

Identical: the 8-byte T.35/GA94 header (`sei.CTA608ITUData()`) and the `cc_data()` body
(`carriage.BuildCCData`). This is confirmed by ffmpeg, which parses A/53 CC from an AV1 T.35 OBU with
the **same** `ff_itut_t35_parse_buffer` path it uses for AVC/HEVC SEI (country `0xB5`, provider
`0x0031`, `GA94`, type `0x03`).

Different (envelope only):

| | SEI (AVC/HEVC) | OBU (AV1) |
|---|---|---|
| Container unit | SEI message in a SEI NAL | Metadata OBU |
| Type signalling | `payload_type=4` + `payload_size` | `obu_type=5` + `metadata_type=4` (leb128) |
| Size | SEI `payload_size` (ff-coded) | `obu_size` (leb128) |
| Escaping | **emulation prevention** (EBSP) | **none** |
| Terminator | `rbsp_trailing_bits` on the NAL | `trailing_bits` on the OBU → `0x80` |

## The `trailing_bits` gotcha (the interop crux)

`itu_t_t35_payload_bytes` has **no explicit length** — it "runs to the end of the OBU." But the OBU
also ends with `trailing_bits` (`0x80` plus possibly more), so a decoder that naively reads to
`obu_size` swallows the terminator. The AV1 spec resolves this with a conformance rule
(metadata-OBU semantics §6.7.1):

> *"The last byte of the valid content of the payload data for metadata OBU types is considered to be
> the last byte that is not equal to zero. … when any payload data is present for this OBU type, at
> least one byte of the payload data (including the trailing bit) shall not be equal to 0. This rule
> is to prevent the dropping of valid bytes by systems that interpret trailing zero bytes as a
> padding continuation of the trailing bits in an OBU."*

So a conformant reader finds the payload end by **scanning back past trailing zero bytes and the
`0x80`**. ffmpeg's CBS does exactly this (`cbs_av1_get_payload_bytes_left`): *"the payload runs up to
the start of the trailing bits, but there might be arbitrarily many trailing zeroes so we need to
read through twice."*

**For CTA-608 this is a non-issue in practice:** the A/53 `cc_data()` is self-delimiting — `cc_count`
(5 bits) says how many 3-byte constructs follow, and `BuildCCData` ends the body with a `0xFF`
`marker_bits`. So the last non-zero payload byte is always the `0xFF` marker, the conformance rule is
satisfied automatically, and a `cc_count`-bounded parser (the reused `sei.ParseCTA608`) ignores
anything after it — including the `0x80`.

## Worked example (verifiable)

608 field-1 control pair `94 2C`, empty field-2, `ccCount = 3` →
`BuildCCData` = `c3 ff  fc 94 2c  f9 00 00  fa 00 00  ff` (12 B).

Full OBU (24 bytes):

```
2A                          obu_header: type=5, has_size_field=1
16                          obu_size = 22
04                          metadata_type = 4 (ITU-T T.35)
b5 00 31 47 41 39 34 03     T.35/GA94 header (== sei.CTA608ITUData())
c3                          A/53: flags(110) + cc_count=3
ff                          em_data
fc 94 2c                    cc construct: valid,type0(field1) + 94 2c
f9 00 00                    cc construct: type1(field2), cc_valid=0
fa 00 00                    cc construct: DTVCC padding, cc_valid=0
ff                          marker_bits  ← last non-zero payload byte
80                          trailing_bits (trailing_one_bit + pad)
```

`obu_size` (22) counts everything from `metadata_type` through the `0x80`.

**These exact 24 bytes are what `av1.CreateCTA608MetadataOBU(ccData)` emits** (mp4ff v0.55.0), and an
mp4ff test asserts them against this worked example. When this note was written the plan was for
go-608 to assemble the 22-byte payload itself and hand it to `av1.OBU.Encode`; #50 moved that
assembly upstream instead, so a caller now supplies only the 12-byte `cc_data()`.

## Recommendations for byte-exact interop

All three are implemented in mp4ff v0.55.0; they are kept here as the rationale for why the
implementation looks the way it does.

- **Encode:** append the `0x80` `trailing_bits` byte and include it in `obu_size` — this is
  spec-required (metadata OBUs always get `trailing_bits`) and matches current libaom/ffmpeg output.
  (Historical note: some old libaom builds omitted it, which is exactly what the conformance rule
  above was added to guard against — do not replicate that.) mp4ff appends it unless
  `MetadataOBU.PayloadHasTrailingBits` says the payload already ends with them, and tolerates an OBU
  that omits them altogether by re-encoding it as it came in.
- **Decode:** bound `cc_data()` by `cc_count`, never by `obu_size`; ignore any trailing bytes
  (`0x80` and/or zeros). The reused `sei.ParseCTA608` already does this, so it is tolerant of
  encoders that vary or omit the trailing byte.
- **No EBSP:** do not run emulation-prevention on the OBU payload (unlike the SEI path) — write raw
  bytes and read raw bytes.

## MP4 / av01 specifics (byte-layout-relevant only)

Per the **AV1 Codec ISO Media File Format Binding**, OBUs stored in `av01` samples must have
`obu_has_size_field = 1` (each OBU self-sized) — which is exactly the form `av1.OBU.Encode` emits and
`av1.SplitOBUs` expects. **Where** the metadata OBU sits within the sample/temporal unit (relative to
the temporal-delimiter and frame OBUs) is a placement decision, deferred to #52; this note fixes only
the OBU's own bytes.

One placement constraint has since been measured and is worth recording here because it is a
*byte-layout* consequence: the mp4 muxer strips temporal-delimiter OBUs while IVF from the same
encoder keeps them, so a placement rule must anchor on the first frame / frame-header OBU rather than
on position-from-start to mean the same bytes in both containers. See `testdata/README.md`.

## Deferred / open — resolved

- **Byte-exact validation against a real-world reference: not obtainable locally (#49, closed).**
  Confirmed as this note anticipated — the only local AV1 encoder is `libsvtav1`, which does not emit
  A/53 CC metadata OBUs; `-a53cc` is an x264/x265 option it silently ignores; and transcoding a real
  AVC+608 mp4 to av01 drops the captions entirely. The correctness bar is therefore met differently:
  validate against **ffmpeg as an independent decoder**
  (`ffmpeg -f lavfi -i "movie=FILE.mp4[out0+subcc]" -map 0:1 out.srt`, verified end to end on the AVC
  side), plus the byte-exact checks mp4ff's own tests now carry — the reference OBU above and a real
  CTA-608 SEI vector. Two clean av01 fixtures landed with #49; recipes and per-sample OBU layouts are
  in `testdata/README.md`.

## Sources

- [AV1 Bitstream & Decoding Process Specification](https://aomediacodec.github.io/av1-spec/) — §5.2 open_bitstream_unit, §5.8 metadata OBU, §6.7 metadata semantics, trailing_bits.
- [av1-spec/06.bitstream.syntax.md](https://github.com/AOMediaCodec/av1-spec/blob/master/06.bitstream.syntax.md), [07.bitstream.semantics.md](https://github.com/AOMediaCodec/av1-spec/blob/master/07.bitstream.semantics.md)
- [AV1 Codec ISO Media File Format Binding](https://aomediacodec.github.io/av1-isobmff/v1.3.0.html)
- ffmpeg `libavcodec/av1dec.c` (`export_itut_t35` → `ff_itut_t35_parse_buffer`) and `libavcodec/cbs_av1_syntax_template.c` (`metadata_itu_t_t35`, `cbs_av1_get_payload_bytes_left`).
- ATSC A/53 Part 4 §6.2.2 (Closed Captioning / `cc_data()`), mirrored by CTA-708-E §4.3 — the body `carriage.BuildCCData` implements.
