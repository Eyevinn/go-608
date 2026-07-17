# go-608 ↔ mp4ff carriage seam (design note)

Working design note for wayfinder ticket #6 (Define the mp4ff carriage seam). Built incrementally
during the grilling. Depends on: `cea608-core-model.md` (#5), `../research/normative-rules-608-708-a53.md`
(#3), `../research/consumer-injection-points.md` (#4).

The chain: per-field **byte-pairs** (pure `cta608` core, #5) → **`cc_data`** → **T.35 payload** →
**SEI message** → **codec NAL** → *(consumer inserts into the access unit)*.

## Decisions

### C1 — `cc_data` assembly is a pure, timing-free function in the carriage package

- **Confirmed (T.E.):** carriage owns `cc_data` byte assembly as a **pure, timing-free** function
  (`BuildCCData(field1, field2 []byte, ccCount int) []byte`); `cc_count` is **passed in**. The
  **fps → `cc_count` policy** and the per-frame decision of *which* byte-pairs to emit live in the
  **generation layer (#7)**, not carriage.
- **Consequence:** the entire go-608 stack (`cta608` core + carriage) is **timing-agnostic**; time
  enters only at #7. Decode's inverse (`cc_data → field pairs`) reuses mp4ff's existing
  `sei.ParseCEA608`, so carriage does not reimplement it.

### C2 — Wrap mp4ff (don't extend it upstream, for now)

- **Confirmed (T.E.):** go-608's carriage package **wraps** mp4ff. Split of the encode chain:
  - **go-608 carriage:** build `cc_data()` + wrap the **T.35/GA94** header (608/ATSC semantics —
    "go-608 owns what mp4ff lacks").
  - **mp4ff (reused):** serialize the SEI message → RBSP payload **with emulation-prevention**
    (`sei.NewSEIData` + `sei.WriteSEIMessages` / `EBSPWriter`); and on decode, extract field pairs
    (`sei.ParseCEA608` / `ExtractCEA608sei` / `ITUData`).
  - **go-608 carriage:** prepend the codec **NAL header** (trivial) to produce the NAL unit.
  - **consumer:** insert the NAL into the access unit (#4).
- **Later (T.E. "may change mp4ff later"):** optionally upstream a symmetric `sei.NewCEA608sei`
  constructor + NAL-wrap helper into mp4ff. Not on the critical path; a future refactor that would
  not change go-608's public API much.

### C3 — Carriage API surface

- **Confirmed (T.E.):** `SEINALU` returns a **bare NAL unit** (NAL header + EBSP payload, **no**
  4-byte length prefix); the consumer adds the length and splices before the first VCL NALU.
- **Confirmed (T.E.):** provide a thin **`FieldPairs`** decode wrapper (over mp4ff `ParseCEA608`) for
  round-trip symmetry.
- **Confirmed (T.E.):** **codec is an explicit `Codec` enum parameter**; go-608 never sniffs it (both
  consumers know their codec — #4).

---

## Carriage package API (synthesis)

Timing-free, depends on mp4ff. (Package name finalized in #10.)

```go
type Codec int
const ( CodecAVC Codec = iota; CodecHEVC )

// ---- Encode (pure, timing-free; ccCount supplied by generation #7) ----

// BuildCCData assembles one frame's cc_data() from per-field byte pairs (parity already set by the
// cta608 serializer). A field pair is 0 or 2 bytes: empty ⇒ a cc_valid=0 cc_type=00/01 construct
// ("no 608 waveform this field this frame"); a 2-byte pair (incl. the 0x8080 608 null pair) ⇒
// cc_valid=1. 608 constructs go first, then pad to ccCount with cc_valid=0 cc_type=10/11 (0x0000)
// DTVCC padding and the 608-terminating construct — per normative-rules §3.1–3.2 / §1.
func BuildCCData(field1Pair, field2Pair []byte, ccCount int) []byte

// SEINALU wraps cc_data() as T.35/GA94 payload → SEI message → codec NAL unit, returning a BARE NAL
// unit (no 4-byte length prefix). Uses mp4ff sei.NewSEIData + sei.WriteSEIMessages (EBSP
// emulation-prevention), then prepends the codec NAL header (AVC 0x06 / HEVC prefix-SEI type 39).
func SEINALU(ccData []byte, codec Codec) []byte

// FrameSEINALU is the one-call convenience: BuildCCData + SEINALU.
func FrameSEINALU(field1Pair, field2Pair []byte, ccCount int, codec Codec) []byte

// ---- Decode (thin wrapper over mp4ff) ----

// FieldPairs finds the CEA-608 SEI in a sample's NALUs (mp4ff avc/hevc.ParseSEINalu +
// sei.ExtractCEA608sei / ITUData.IsCEA608) and returns the two field byte-pair streams (parity
// preserved) to feed the cta608 core Decoder.
func FieldPairs(sampleNALUs [][]byte, codec Codec) (field1, field2 []byte, err error)
```

### Encode flow (who does what)

1. `cta608` core (#5): `Serialize(tokens) → []byte` per-field byte-pairs. Pure, no mp4ff.
2. generation (#7): choose `ccCount` from fps (normative §3), decide which pairs go in this frame
   (drain field queues 2 B/field/frame), pick field-2 usage.
3. **carriage (#6):** `BuildCCData(f1, f2, ccCount)` → `SEINALU(ccData, codec)` → **bare NAL unit**.
4. consumer (#4): prepend 4-byte length, splice before the first VCL NALU (into a per-emission copy;
   before CENC).

### Decode flow

consumer/mp4ff hands sample NALUs → **carriage `FieldPairs`** (reuses mp4ff `ParseCEA608`) →
`cta608` core `Decoder.Feed(field1)` → `Screen`.

### mp4ff functions used (wrap, C2)

`sei.NewSEIData`, `sei.WriteSEIMessages`, `bits.EBSPWriter` (encode); `avc.ParseSEINalu` /
`hevc.ParseSEINalu`, `sei.ExtractCEA608sei`, `sei.ParseCEA608`, `ITUData.IsCEA608`,
`avc.GetNalusFromSample` (decode). go-608 adds only the `cc_data`+T.35 builder and the NAL-header
prepend.

### Deferred to other tickets

- **fps → `cc_count` policy, field-2 default, per-frame scheduling** → generation (#7).
- **Package name / where carriage sits in the tree** → #10.
- **Exact HEVC carriage conformance spec** (payload is codec-identical; SCTE 128-1 is AVC) →
  cross-check if a conformance claim is made (normative-rules §2 open item).

