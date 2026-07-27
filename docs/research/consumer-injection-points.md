# Consumer injection points for CEA-608 SEI (wayfinder #4)

Research input for `github.com/Eyevinn/go-608`. Maps how the two intended consumers —
**livesim2** (DASH/HLS/CMAF live source simulator) and **moqlivemock** (MoQ live mock
publisher/subscriber) — produce and serve video access units, and where/how they could inject
per-frame SEI NAL units carrying CEA-608 `cc_data`. Resolves ticket #4. It only **observes**;
it proposes, but does not make, code changes in those repos.

Companion docs: `../design/cea608-core-model.md` (#5 core model), `./normative-rules-608-708-a53.md`
(SEI/`cc_data` byte shape, per-frame `cc_count`), `./prior-art-608.md` (what `mp4ff/sei` parses).

Repos and modules (exact local paths):
- **livesim2** — `/Users/tobbe/proj/github/dashif/livesim2` (module `github.com/Dash-Industry-Forum/livesim2`).
- **moqlivemock** — `/Users/tobbe/proj/github/ev/moq-workspace/moqlivemock` (module `github.com/Eyevinn/moqlivemock`).
- **mp4ff** — `/Users/tobbe/proj/github/ev/mp4ff` (module `github.com/Eyevinn/mp4ff`).

Citations use `path:line` with the repo made explicit.

---

## 1. livesim2

Repo: `livesim2` = `/Users/tobbe/proj/github/dashif/livesim2` (mp4ff dependency at `v0.54.0`,
`go.mod:9`). It serves DASH/HLS/CMAF segments on the fly by looping stored VoD/mezzanine content as
live. (Note: a nested decoy tree exists at `cmd/livesim2-sgai-demo/cmd/livesim2/app/…` — ignored;
all citations are the real `cmd/livesim2/app/`.)

**Headline finding:** in the **default (whole-segment) video path, livesim2 does NOT decode or visit
individual video samples** — it copies the stored mezzanine `mdat` bytes verbatim and rewrites only
fragment-level box headers (sequence number, tfdt, data offsets). A per-sample loop over video
samples exists **only** in the low-latency chunked path and in the SGAI slate generator. So a caption
library covering the default path must **introduce** the per-sample decode/re-encode step itself.

### 1.1 Video production / serving path

- **HTTP entry -> router.** Media-segment requests reach `writeSegment`
  (`cmd/livesim2/app/handler_livesim.go:180`), which routes (`handler_livesim.go:337-379`): init
  segment, then SSR/low-delay -> `writeSubSegment` (`:368`), `AvailabilityTimeCompleteFlag`/image or
  default -> `writeLiveSegment` (`:371,:378`), explicit `cfg.ChunkDurS` -> `writeChunkedSegment`
  (`:375`).
- **Default path = byte pass-through.** `writeLiveSegment` (`livesegment.go:434-480`) calls
  `genLiveSegment` (`:441`) then `mp4.MediaSegment.EncodeSW` (`:454-460`). `genLiveSegment`
  (`livesegment.go:30-129`): `createOutSeg` reads the stored segment file from disk into `outSeg.data`
  (`:551-591`, `fs.ReadFile` at `:586`); decodes with `mp4.DecodeFileSR` -> `seg :=
  segFile.Segments[0]` (`:45-53`); a **fragment time-rebasing loop** (`:72-92`) sets
  `frag.Moof.Mfhd.SequenceNumber`, rewrites `Tfdt` base-media-decode-time, and adjusts
  `traf.Trun.DataOffset` / `frag.Mdat.StartPos` / `traf.Saio.Offset`. **This loop never looks inside
  `mdat` — the video NALU payload passes through untouched.** So the default path is codec-agnostic
  byte pass-through.
- **Per-sample loop #1 (candidate injection site): chunked low-latency.** `chunkSegment`
  (`livesegment.go:898-949`): `trex := init.Moov.Mvex.Trex` (`:899`); per fragment
  `f.GetFullSamples(trex)` -> `[]mp4.FullSample` (`:902`); then **`for i := range fs { … }`**
  (`:915-934`) sets `fs[i].DecodeTime = sampleDecodeTime`, `ch.frag.AddFullSample(fs[i])`, advances
  `sampleDecodeTime += fs[i].Dur`. Here each sample is a `FullSample` with `.Data` accessible — a
  natural place to parse NALUs and add an SEI NALU. Reached via `prepareChunks` (`:698-743`, chunking
  at `:728`), used by both `writeChunkedSegment` (`:762`) and `writeSubSegment` (`:818`).
- **Per-sample loop #2: SGAI slate generator (AVC only).** `sgai_slate.go` authors video NALUs itself
  (see §1.2) — the only place livesim2 generates video sample `.Data`.
- **mp4ff types in flow:** `mp4.MediaSegment` (`segOut.seg`, `livesegment.go:524-528`),
  `mp4.Fragment`, `mp4.Mdat`, `mp4.FullSample`/`mp4.Sample`, `mp4.InitSegment`, `mp4.TrexBox`
  (`GetFullSamples`).

### 1.2 Existing SEI handling

- **None.** Repo-wide grep for `sei`/`SEI`/`user_data`/`cc_data`/`cc_count`/`caption`/`cea608` across
  `cmd pkg internal` (excluding the demo duplicate) returns no non-test matches. No SEI or CEA-608
  handling anywhere in the serving path.
- **The only NALU-level code is the AVC slate** (`sgai_slate.go`, using `Eyevinn/hi264` +
  `mp4ff/avc`): `avc.ParseSPSNALUnit` (`:70`), `avc.ParsePPSNALUnit` (`:80`),
  `encode.GenerateIDRWithSPSPPS` (`:150`), `encode.EncodePSkipSlice` (`:155`), and
  `avc.ConvertByteStreamToNaluSample(annexB)` (`:169`) which converts Annex-B to length-prefixed AVCC
  before building the `mp4.FullSample` (`:174-181`). This is the closest existing template for "produce
  a video sample's `.Data` and mux it," but it is **AVC-only**.
- **Reusable behavior:** livesim2 already builds video `mp4.FullSample`s and `AddFullSample`s them into
  a fresh fragment/segment (`sgai_slate.go:340-355`), and converts Annex-B->AVCC (`:169`). The mp4ff
  dependency also ships the full `sei` package incl. CEA-608 (see §3), but livesim2 does not call it.

### 1.3 Timing / wall-clock sources at the injection point

- **Media-time unit in scope = `segMeta` struct** (`livesegment.go:173-184`): `rep *RepData`,
  `origTime/newTime uint64`, `origNr/newNr uint32`, `origDur/newDur uint32`, `timescale uint32`.
  `newTime` = rebased base-media-decode-time of the segment (in `timescale` units), `newNr` = output
  segment number, `timescale = rep.MediaTimescale`. Populated by `findSegMetaFromNr` (`:321-354`) /
  `findSegMetaFromTime` (`:191-228`).
- **Per-sample DTS at the chunk loop** (`livesegment.go:913-919`): `sampleDecodeTime := segMeta.newTime`
  then per sample `fs[i].DecodeTime = sampleDecodeTime; sampleDecodeTime += fs[i].Dur` — absolute
  DTS in media timescale computed right there; `.CompositionTimeOffset` gives CTS, `.Dur` the frame
  duration.
- **Wall-clock / AST mapping.** `nowMS` (URL `?nowMS=` or server clock; `handler_livesim.go:254-259`)
  and `cfg.StartTimeS` (the **availabilityStartTime**). Media-time -> wall-clock is done as
  `chunkAvailTime := newTimeInt + cfg.StartTimeS*rep.MediaTimescale` then `*1000/MediaTimescale`
  (`livesegment.go:786-792`). So **wall-clock ms of a sample = `cfg.StartTimeS*1000 +
  DecodeTime*1000/timescale`.** The slate uses exactly this per-frame:
  `segStartMS := cfg.StartTimeS*1000 + meta.newTime*1000/meta.timescale` then `frameWallMS :=
  segStartMS + dtsRel*1000/meta.timescale` (`sgai_slate.go:306,329`) — the exact formula a caption
  scheduler should reuse.
- **Frame rate is derivable, not stored as fps:** `RepData.MediaTimescale` (`asset.go:872`),
  `RepData.DefaultSampleDuration` (`:876`), `RepData.ConstantSampleDuration` (`:877`), per-sample
  `trun.Samples[i].Dur`, and `RepData.sampleDur()` (`:911-924`). **fps = MediaTimescale / sampleDur.**

### 1.4 AU-assembly ownership

- **livesim2 fully owns AU/sample assembly:** it decodes stored segments, rebases timing, optionally
  re-samples (chunking) or regenerates (slate), and re-encodes via `mp4.MediaSegment.EncodeSW`
  (`livesegment.go:454`) / `chk.frag.Encode` (`:958`). An injected library must **hand livesim2 the
  SEI NALU to insert; it must not mux.**
- **Structure to inject into:** `mp4.FullSample` (`mp4/sample.go:28`) — `Data []byte` is the sample
  payload = **length-prefixed AVCC** (`[4-byte BE length][NALU]…`), confirmed by `avc.GetNalusFromSample`
  and by the slate's `avc.ConvertByteStreamToNaluSample`. Not Annex-B at sample level. To inject:
  `avc.GetNalusFromSample(sample.Data)` -> append the CEA-608 SEI NALU (type 6 AVC / prefix-SEI 39
  HEVC) -> re-serialize with 4-byte lengths -> update `Sample.Size` -> `frag.AddFullSample`. The chunk
  path already rebuilds via `AddFullSample`; the default path currently doesn't decode samples at all,
  so it would need this decode/re-encode added.
- **De/serialization today:** livesim2 only *serializes* NALUs (slate `ConvertByteStreamToNaluSample`,
  `sgai_slate.go:169`); it never calls `GetNalusFromSample` in the video path.
- **AVC vs HEVC:** default and chunked paths are **codec-agnostic** (operate on
  fragment/`FullSample` bytes, never interpret NALU semantics — AVC and HEVC flow identically). The
  only codec-specific video code is the AVC-only slate (`newSlateGen` errors for non-AVC,
  `sgai_slate.go:63-69`). livesim2 offers **no HEVC NALU handling to reuse** — only mp4ff does. So the
  injecting library must own AVC vs HEVC NALU framing.
- **Recommended injection point (agent's synthesis):** to cover the default path, hook inside
  `genLiveSegment` after `seg := segFile.Segments[0]` and the rebasing loop, before `outSeg.seg = seg`
  (~`livesegment.go:93-122`): `GetFullSamples` for the video track, inject the SEI per sample using
  DTS (`newTime` + accumulated `Dur`) and the wall-clock formula above, then rebuild the fragment. The
  chunk loop (`:915-934`) is the natural second hook and already has `FullSample`s in hand.

---

## 2. moqlivemock

Repo: `moqlivemock` = `/Users/tobbe/proj/github/ev/moq-workspace/moqlivemock`. A MoQ live
publisher/subscriber that loops pre-encoded fragmented-mp4 and serves it over MoQ in one of four
packagings (CMAF, LOCMAF, LOC, moq-mi). It already depends on mp4ff.

### 2.1 Video production / serving path

- **Pre-encoded source, decoded to per-frame `FullSample`.** Video is offline-encoded AVC/HEVC
  (`assets/test10s/video_400kbps_avc.mp4` / `..._hevc.mp4`, 25 fps 1280x720, produced by
  `utils/contentgen/videogen.go` via ffmpeg — **no captions/SEI added there**). Load path:
  `LoadAssetWithProtection` -> `parseTracks` -> `InitContentTrack` (`internal/asset.go:313,348,142`).
  `InitContentTrack` calls `mp4.DecodeFile` and flattens every fragment into
  `ct.Samples` via `frag.GetFullSamples(trex)` (`internal/asset.go:143-187`). The canonical per-frame
  store is **`ContentTrack.Samples []mp4.FullSample`** (`internal/asset.go:58`). So injecting SEI means
  **rewriting existing decoded samples**, not hooking a live encoder.
- **Per-frame NALU re-assembly at load.** Codec dispatch at `internal/asset.go:249-282`
  (`avc1/avc3`->`initAVCData`, `hvc1/hev1`->`initHEVCData`). AVC: `initAVCData`
  (`internal/media.go:52-106`) calls `avc.GetNalusFromSample` (`:66`), truncates `samples[i].Data[:0]`
  (`:70`), keeps IDR/non-IDR/**SEI** NALUs, re-emitting each as `[4-byte BE length][NALU]` (`:77-80`);
  SPS/PPS are hoisted to the init segment. HEVC: `initHEVCData` (`internal/media.go:198-287`) manually
  parses length prefixes (`:230-239`), hoists VPS/SPS/PPS, re-emits the rest (SEI included) as AVCC
  (`:250-253`). After load, **`Samples[i].Data` is length-prefixed AVCC** for both codecs and any
  pre-existing SEI is preserved.
- **Four per-object emission loops (candidate injection sites), all reading the same `Samples[].Data`:**
  1. **CMAF** (`GenMoQGroup`, `internal/moqgroup.go:31-69`) strides `sampleBatch` frames per object via
     `track.GenCMAFChunk` -> `createFragment` (`internal/asset.go:950,1012`). Payload = a full CMAF
     chunk (moof+mdat), optionally CENC-encrypted (`:962-972`).
  2. **LOCMAF** (`track.GenLocmafChunk`, `internal/moqgroup.go:60` -> `createFragment` then
     `locmaf.EncodeCanonical`, `internal/asset.go:1004`). Payload = LOCMAF header + mdat.
  3. **LOC** (raw, 1 frame/object): `PublishLOCTrack` loop `internal/pub/pub.go:416-459`; payload =
     `sample.Data` (AVCC), SPS/PPS(/VPS) prepended on sync frames (`:437-443`), written `:452`.
  4. **moq-mi** (1 frame/object): `publishMoqMIVideo` loop `internal/pub/moqmi.go:91-127`; payload =
     `sample.Data`, written `:121`.
  The common **surgical per-frame** point for CMAF/LOCMAF is `createFragment`
  (`internal/asset.go:1012-1045`) which builds a fresh `mp4.FullSample` per output sample, copying
  `orig.Data` (`:1026-1034`) before `f.AddFullSample(fs)` (`:1035`). Default `-videobatch 1`
  (`cmd/mlmpub/main.go:81-82`) => **1 video frame per object** in every packaging.
- **mp4ff types in flow:** `mp4.FullSample` (`Data` = AVCC) is the per-frame record; `mp4.Fragment`
  (`CreateFragment`/`AddFullSample`/`EncodeSW`) for CMAF/LOCMAF; NALU split/type via mp4ff `avc`/`hevc`.
  Only LOC and moq-mi object payloads are "a raw AVCC frame"; CMAF/LOCMAF wrap it in a fragment.

### 2.2 Existing SEI handling

- **No SEI is created, parsed, or interpreted.** The only reference is `avc.NALU_SEI` in the
  keep-list at `internal/media.go:77` — moqlivemock **passively preserves** pre-existing SEI (HEVC via
  its `default` branch, `internal/media.go:250-253`). No `AddNalu`, no SEI writer, no `user_data`, no
  CEA-608/708 code (repo-wide grep negative).
- **Subtitle code is a separate timed-text track, not video SEI.** `internal/subtitle.go` generates
  **WVTT** and **STPP/TTML** subtitle *tracks* as their own CMAF MoQ tracks (`PublishSubtitleTrack`
  `internal/pub/pub.go:470`; `GenSubtitleGroup` `internal/subtitle.go:231`), 1 s groups, cue text with
  UTC + group number. It never touches video samples. `internal/catalog.go:153` only lists `"caption"`
  as an allowed role string in a comment — not implemented.
- **Reusable primitives:** `avc.GetNalusFromSample` (`internal/media.go:66`); the AVCC re-serialize
  idiom `binary.BigEndian.PutUint32(work,len); append` (`internal/media.go:78-80`, `:251-253`); the
  wall-clock-aligned group scheduling in `subtitle.go`/`moqgroup.go`. mp4ff's `avc`/`hevc` NALU tooling
  is already available.

### 2.3 Timing / wall-clock sources at the injection point

Frame is anchored to the Unix epoch by output sample number, so every emission site has both media
time and wall-clock in scope:
- **`CalcSample(nr)`** (`internal/asset.go:1048-1061`) -> `startTime = nr*SampleDur` (epoch-anchored
  media units) and `origNr` (index into the looped 10 s clip; wrap via `t.LoopDur`).
- **Group number = UTC second:** `CurrMoQGroupNr = nowMS/MoqGroupDurMS`, `MoqGroupDurMS=1000`
  (`internal/moqgroup.go:93`, `internal/const.go:4`).
- **CMAF/LOCMAF** (`createFragment`, `internal/asset.go:1012-1045`): per frame in scope — `startTime`
  (written as `fs.DecodeTime`), `origNr`, `orig.Dur`, `t.SampleDur`, `t.TimeScale`, `t.GopLength`.
  In `WriteMoQGroup` (`internal/moqgroup.go:100-128`): `objTime`, `objTimeMS` (wall-clock ms) pace
  delivery. CMAF objects carry no explicit on-wire timestamp — timing is implicit via moof
  `baseMediaDecodeTime` (= `startTime`) + group number.
- **LOC** (`internal/pub/pub.go:416-459`): `sampleNr`, `sampleTime = sampleNr*sampleDur`, `objTimeMS`,
  and an explicit **LOC Timestamp extension header** (`locPropTimestamp=0x06` = µs since epoch,
  `:445-452`).
- **moq-mi** (`internal/pub/moqmi.go:91-127`): `moqmi.VideoMetadata{SeqID, PTS=sampleNr*sampleDur, DTS,
  Timebase=ct.TimeScale, Duration=ct.SampleDur, WallclockMS=time.Now().UnixMilli()}` (`:107-114`) as
  per-object extension headers; group = one GOP (`groupNr = now/gopDurMS`, `:69-74`).
- **Frame rate known everywhere:** `ct.TimeScale`, `ct.SampleDur` are `ContentTrack` fields
  (`internal/asset.go:51-54`); `frameRate = TimeScale/SampleDur` (e.g. `internal/asset.go:575`);
  `ct.GopLength` available (`:53,232-247`). So per-frame `cc_count` and per-frame PTS are derivable at
  every injection point.

### 2.4 AU-assembly ownership

- **moqlivemock fully owns AU/sample assembly:** it splits NALUs, hand-serializes AVCC, and builds
  fragments via `mp4.CreateFragment`/`AddFullSample`/`EncodeSW` (`internal/asset.go:1012-1044`), LOCMAF
  via `locmaf.EncodeCanonical` (`:1004`), and raw payloads for LOC/moq-mi. An injected library must
  **hand bytes in for insertion; it must not mux.**
- **Structure to inject into:** the frame's AVCC byte slice — canonically `mp4.FullSample.Data`, source
  of truth `ContentTrack.Samples []mp4.FullSample` (`internal/asset.go:58`). Build the SEI NALU
  (payloadType 4), prepend a 4-byte BE length, splice it **before the VCL NALU**. Cleanest common hook:
  mutate `ct.Samples[origNr].Data` (or the copy `fs.Data` in `createFragment`,
  `internal/asset.go:1026-1034`) — this feeds all four packagings.
- **Format = length-prefixed AVCC (4-byte BE), NOT Annex-B** on the producer side
  (`internal/media.go:78-80`, `:251-253`). The subscriber converts to Annex-B only for ffplay output
  (`internal/sub/loc.go`). Inject in AVCC.
- **AVC and HEVC both handled; divergence:** `initAVCData` (`internal/media.go:52-106`) vs
  `initHEVCData` (`:198-287`, adds VPS). The injector must key off the `ct.SpecData` concrete type
  (`*internal.AVCData` / `*internal.HEVCData`) as the publish paths do (`internal/pub/pub.go:393-398`,
  `internal/pub/moqmi.go:37-47`), and wrap the SEI per codec (AVC NALU type 6 vs HEVC PREFIX_SEI 39).
- **Gotchas for an injector:** (1) content is **looped** — many output frames reuse the same backing
  `Samples[origNr].Data`, and four packaging goroutines run concurrently, so mutating that slice in
  place corrupts/duplicates; inject into a **per-emission copy**. (2) For protected tracks the
  CMAF/LOCMAF chunk is CENC-encrypted after assembly (`internal/asset.go:962-972`), so SEI must go into
  the **clear** sample before encryption.

---

## 3. mp4ff SEI-creation capability

> **Outcome (2026-07-27): most of this section's gaps are closed.** It audits mp4ff as of ticket #4,
> and the answer it reaches — "mp4ff cannot create a caption SEI NAL unit" — was correct then and drove
> go-608's design. mp4ff **v0.55.0** then added the encode side
> ([#532](https://github.com/Eyevinn/mp4ff/pull/532), [#541](https://github.com/Eyevinn/mp4ff/pull/541)),
> and go-608 now delegates to it. Superseded claims are marked inline below.
>
> One part of the conclusion still holds and is worth keeping straight: **mp4ff still has no
> `cc_data()` builder.** Its creators take a `cc_data()` structure as *input*. Building those bytes is
> `carriage.BuildCCData`, and it remains the whole of go-608's contribution to the wire. What moved
> upstream is the T.35/GA94 wrapping and both envelopes.
>
> Note also that the `sei` API renamed CEA-608 to CTA-608 in v0.55.0 with no aliases
> (`ParseCEA608` → `ParseCTA608`, `CEA608sei` → `CTA608sei`, `ExtractCEA608sei` → `ExtractCTA608sei`,
> `IsCEA608` → `IsCTA608`), so symbol names and `sei4.go` line references below have been updated.

Repo: `mp4ff` = `/Users/tobbe/proj/github/ev/mp4ff`. Question: beyond parsing SEI type-4 CEA-608
(prior-art §3), can mp4ff **create/serialize** an SEI NAL unit that a consumer can drop into a
video sample? Answer **at the time of this audit**: it can serialize a generic SEI message into an
SEI-NAL RBSP payload (with emulation prevention), but it has no CEA-608 `cc_data` builder and no helper
that wraps a payload into a full NAL unit or packs NALUs back into a sample. Those last steps were the
caller's job (and were the net-new surface for go-608 #6).

### 3.1 What mp4ff CAN do (serialize SEI)

- **`sei.WriteSEIMessages(w io.Writer, msgs []SEIMessage) error`** (`mp4ff/sei/sei.go:603`): writes
  each message as `payloadType`/`payloadSize` (the 0xFF-extended run-length form via
  `WriteSEIValue`) followed by the payload bytes, then `WriteRbspTrailingBits`. The comment states:
  "The output corresponds to an SEI NAL unit payload." It writes through a `bits.EBSPWriter`, which
  **inserts start-code emulation-prevention `0x03` bytes automatically** (`mp4ff/bits/ebspwriter.go:40`,
  `WriteSEIValue` at `:86`, `WriteRbspTrailingBits` at `:99`). So emulation prevention is handled for
  free at the RBSP->EBSP boundary.
- **`SEIMessage` interface** (`mp4ff/sei/sei.go:467`): `Type() uint`, `Size() uint`, `Payload() []byte`.
  Anything implementing it can be serialized.
- **Generic wrapper: `sei.NewSEIData(msgType uint, payload []byte) *SEIData`** (`mp4ff/sei/sei.go:527`);
  `*SEIData` implements `SEIMessage` (`Type` `:532`, `Payload` `:537`, `Size` `:548`). So a consumer can
  build the full T.35 payload bytes itself and wrap them as
  `sei.NewSEIData(sei.SEIUserDataRegisteredITUtT35Type, payload)` with no new mp4ff type.

### 3.2 What mp4ff CANNOT do (the gaps go-608 #6 must fill)

- **No CTA-608 T.35 wrapper builder.** ~~`CTA608sei` is **parse-only**~~: it was created solely by
  `ExtractCTA608sei(sd *SEIData)` which calls `ParseCTA608`, the decode-side reader. Its `payload` field
  is unexported and was set only when parsing; there was **no `NewCTA608sei(field1, field2)`
  constructor**, no `cc_data()` builder, and no T.35 wrapper builder (the
  `0xB5 / 0x0031 / GA94 / 0x03` + `cc_count` + per-construct triplets + `0xFF` marker) — the only
  CTA-608 symbols in `sei/` were decode-side. **This was exactly the encode surface #6 owned:** build
  the `cc_data()` bytes (per the §1/§2/§3 normative rules), prepend the T.35 header, and hand the result
  to `WriteSEIMessages`.
  **Superseded in v0.55.0**, which split that surface in two: the T.35 wrapping is now
  `sei.CreateCTA608Payload(ccData)` and the message creator is `sei.CreateCTA608SEIMessage(ccData)`
  (`sei/sei4.go:83` and `:95`), with `sei.CTA608ITUData()` (`:47`) owning the 8 identity bytes. **The
  `cc_data()` builder itself is still not in mp4ff** and remains `carriage.BuildCCData` — the creators
  take `cc_data()` as input. So of this bullet's three claims, the first two are now false and the third
  still stands.
- **No AVC/HEVC "write SEI NALU" helper.** `avc.ParseSEINalu` (`mp4ff/avc/sei.go`) and
  `hevc.ParseSEINalu` (`mp4ff/hevc/sei.go`) were **parse-only**, so to turn the RBSP payload from
  `WriteSEIMessages` into a real NAL unit the caller had to prepend the NAL header itself:
  AVC = 1 byte `0x06` (`NALU_SEI = 6`); HEVC = 2-byte header with `NALU_SEI_PREFIX` (type 39).
  **Superseded in v0.55.0:** `avc.CreateSEINalu(msgs)` and `hevc.CreateSEINalu(msgs)`
  (`avc/sei.go:17`, `hevc/sei.go:17`) do the wrap, and `carriage.NALU` delegates to them — the
  header bytes no longer live in go-608.
- **No "pack `[][]byte` NALUs back into a length-prefixed sample" helper.** Still true of mp4ff, but
  go-608 now supplies it publicly: `carriage.PrefixNALUs`, `carriage.SampleNALUs` and
  `carriage.SpliceSEIBeforeVCL` do the split, insert and re-pack, so a consumer no longer writes the
  length prefixes by hand. The inverse parse exists —
  `avc.GetNalusFromSample(sample []byte) ([][]byte, error)` follows 4-byte length fields
  (`mp4ff/avc/nalus.go:9`) — but re-serialization is done inline by callers writing a big-endian
  uint32 length + NALU bytes. Concrete precedent in mp4ff's own tooling:
  `cmd/mp4ff-mvhevc/main.go:552-556` (`binary.BigEndian.PutUint32(lenBuf, uint32(len(nalu)))` then
  append), and `avc/annexb.go:69,82` (`ConvertByteStreamToNaluSample`). So inserting a caption SEI
  into a sample = build the SEI NAL bytes, then either (a) `GetNalusFromSample` -> insert into the
  `[][]byte` list -> re-pack with 4-byte lengths, or (b) prepend `len||naluBytes` directly to the
  sample `Data` at the right position.

### 3.3 Sample data structures a caption SEI is inserted into

- **`mp4.Sample`** (`mp4ff/mp4/sample.go:4`): trun metadata only — `Flags`, `Dur`, `Size`,
  `CompositionTimeOffset`. No bytes. **Note:** inserting a NALU grows the sample, so `Size` must be
  updated by whoever assembles the sample.
- **`mp4.FullSample`** (`mp4ff/mp4/sample.go:28`): `Sample` + `DecodeTime uint64` (absolute, mdhd
  timescale) + `Data []byte` (the length-prefixed AVCC sample). `PresentationTime() int64 =
  DecodeTime + CompositionTimeOffset` (`:39`). **`FullSample.Data` is the exact byte slice a caption
  SEI NAL is inserted into**, and `DecodeTime`/`PresentationTime()` are the media-time hooks for
  scheduling (#7).

**Bottom line at the time:** mp4ff gave go-608 the RBSP->EBSP SEI *serializer* (emulation prevention
included) and the generic `SEIData` message wrapper, but **not** the CEA-608 `cc_data` builder, **not**
the NAL-header wrap, and **not** the sample re-pack. go-608 #6 supplies the `cc_data`+T.35 payload; the
NAL-header wrap and sample re-pack are trivial and can live either in #6 (produce ready-to-insert NAL
bytes) or in the consumer (produce byte pairs / payload). This directly informs the API choice in §4.

**Bottom line now:** the split landed one notch lower than this section expected. go-608 supplies
`cc_data()` only (`carriage.BuildCCData`); mp4ff owns the T.35 wrap and both envelopes
(`sei.CreateCTA608SEIMessage` + `avc`/`hevc.CreateSEINalu` for SEI,
`av1.CreateCTA608MetadataOBU` for AV1); and the sample re-pack is go-608's, exported from `carriage`
rather than left to each consumer. The reusable unit across codecs turned out to be the **SEI message
payload** — the T.35/GA94 header + `cc_data()`, byte-identical between SEI and the AV1 metadata OBU —
not the message or the NAL, since an SEI NAL carries an emulation-prevented EBSP that AV1 has no analog
for.

---

## 4. Proposed go-608 -> consumer API shape

**Both consumers own AU assembly** (§1.4, §2.4), so go-608 must **hand them data to insert** and stay
out of muxing. The seam question is what granularity to hand over.

### Recommendation: (c) both levels, layered, with (b) — ready-to-insert SEI NAL units — as the default seam both consumers use.

Rationale:
1. **Both injection sites most naturally consume a NAL-unit `[]byte`.** moqlivemock splices NALUs into
   its AVCC `Data` / `[][]byte` list (§2.4); livesim2's hook does `GetFullSamples` -> `[][]byte` ->
   append -> re-serialize (§1.4). A bare SEI NAL unit drops straight into both.
2. **The fiddly, normative logic must not be duplicated in two consumers.** `cc_data()` construction
   (`cc_count` per frame rate — §3 of the normative doc — field-1/field-2 interleave, `0x8080` null
   pairs vs `0x0000` construct padding, `0xFF` markers), the T.35 wrapper
   (`0xB5/0x0031/GA94/0x03`), the SEI message framing, emulation-prevention, and the AVC-vs-HEVC NAL
   header all belong in **one** place: go-608 #6, which can lean on mp4ff's `WriteSEIMessages`
   (RBSP->EBSP with emulation prevention, §3.1) for the serialization step. Neither consumer has any
   of this today (§1.2, §2.2).
3. **Keep the low-level (a) public too.** moq-mi is a non-mp4 MoQ container where a consumer might
   choose to carry `cc_data` (or raw field byte pairs) in its own extension headers rather than as
   video SEI; tooling/tests want the byte pairs; and #7's frame scheduler produces byte pairs
   *before* they are wrapped. So expose the byte-pair / `cc_data` level as well — but the recommended
   consumer entry point is (b).
4. **Do NOT include the 4-byte AVCC length prefix in (b).** The consumer owns sample assembly and adds
   the length prefix as part of its normal NALU serialization (both already do:
   `internal/media.go:78-80`; `avc.ConvertByteStreamToNaluSample`). Return the bare NAL unit
   (NAL header + EBSP payload).
5. **Codec is a parameter** (AVC 1-byte header `0x06` vs HEVC 2-byte prefix-SEI type 39). Both
   consumers already know their codec (livesim2 via sample entry; moqlivemock via `ct.SpecData`
   concrete type). go-608 does not sniff it.

### Layering (mirrors the design doc's package layering)

- **pure `cea608` (#5):** `[]Token <-> []byte` byte pairs per channel/field. Timing-agnostic.
- **carriage `#6`:** byte pairs -> `cc_data()` -> T.35 payload -> SEI message -> **codec-aware SEI NAL
  unit**, via mp4ff. This is the (a)->(b) bridge.
- **generation `#7`:** owns timing, wall-clock, per-frame `cc_count` scheduling — decides *which*
  bytes go in each frame and drains the per-field queues 2 bytes/field/frame.

### Function-signature sketch

```go
// ---- Level (b): the recommended consumer seam (carriage #6 + generation #7) ----

type Codec int
const ( CodecAVC Codec = iota; CodecHEVC )

// Generator is stateful, per video elementary stream. It knows the frame rate (for the per-rate
// cc_count budget, normative §3) and codec, holds the per-field byte-pair queues produced by the
// cea608 Encoder, and drains them one video frame at a time.
type Generator struct { /* fps, codec, field-1/field-2 queues, schedule, cc_count policy */ }

func NewGenerator(fps float64, codec Codec, opts ...Option) *Generator

// Enqueue caption content to become active at a wall-clock instant (both consumers have wall-clock
// at the injection site: livesim2 cfg.StartTimeS*1000 + DTS*1000/timescale; moqlivemock epoch-anchored
// DecodeTime / WallclockMS). The Generator compiles the block via the cea608 Encoder into byte pairs.
func (g *Generator) AddCaption(activeAtWallMS int64, block cea608.CaptionBlock)

// NextFrameSEINALU returns the ready-to-insert SEI NAL unit (NAL header + EBSP payload, NO 4-byte
// length prefix) for the video frame presented at frameWallMS, advancing the field queues by this
// frame's cc_count budget. Returns nil only when configured to skip padding-only frames
// (default: emit fixed-allocation padding every frame for interoperability — normative §3.2).
func (g *Generator) NextFrameSEINALU(frameWallMS int64) []byte

// ---- Level (a): lower-level carriage seam (#6), public for non-SEI carriage & tooling ----

// BuildCCData assembles one frame's cc_data() structure from per-field byte pairs (already parity-set),
// choosing cc_count per the configured policy/frame rate and padding as needed.
func BuildCCData(field1Pair, field2Pair []byte, ccCount int) []byte

// SEINALUFromCCData wraps a cc_data() payload in the T.35/GA94 header + SEI message + codec NAL header.
func SEINALUFromCCData(ccData []byte, codec Codec) []byte
```

The consumer code at each injection site is then ~3 lines: get the frame's wall-clock time (already
in scope), call `NextFrameSEINALU`, prepend the 4-byte length, splice before the first VCL NALU.

---

## 5. Per-consumer fit notes & constraints

### livesim2
- **Frame rate:** available as `MediaTimescale / sampleDur` (`RepData.sampleDur()`, `asset.go:911-924`).
  Good for `cc_count`. Watch for variable sample durations (`ConstantSampleDuration` is nil then) and
  3:2 pull-down (`cc_count` alternates 20/30 — normative §3) if such content is ever served.
- **AVC vs HEVC:** both served, but the default/chunked paths are codec-agnostic pass-through — livesim2
  has **no HEVC NALU handling to reuse**. The SEI-insertion code must own AVC (header `0x06`) and HEVC
  (prefix-SEI 39) framing itself. Level-(b) API delivering a codec-tagged NAL unit removes this burden
  from livesim2.
- **Biggest constraint — the default path doesn't touch samples.** Injecting SEI into the common
  (whole-segment) path means **adding a decode/re-encode step** (`GetFullSamples` -> inject -> rebuild
  fragment) that does not exist today; currently the `mdat` is copied verbatim (§1.1). This is real new
  work in livesim2 and a perf consideration (per-segment sample decode/re-mux vs byte copy). The
  chunked LL path already materializes `FullSample`s, so it is the cheaper first target.
- **SEI insertion = rewriting existing samples.** Each sample grows by the SEI NALU; `Sample.Size`,
  `Trun` sizes, `Mdat` size and `DataOffset`/`Saio` offsets must be recomputed. mp4ff's
  `AddFullSample`/`EncodeSW` handle this when the fragment is rebuilt. Emulation-prevention is handled by
  mp4ff's EBSPWriter (§3.1), so raw `cc_data` bytes with `0x00 0x00` runs are safe.
- **Encrypted content:** livesim2 also does on-the-fly encryption in some paths; SEI must be inserted
  into the clear sample before any CENC step (same class of constraint as moqlivemock).

### moqlivemock
- **Frame rate:** `ct.TimeScale / ct.SampleDur` known at every emission site (`asset.go:575`); `GopLength`
  too. Clean for `cc_count` and per-frame PTS. Default 25 fps -> `cc_count = 24` (normative §3, PAL,
  formula-derived).
- **AVC vs HEVC:** both handled via distinct init paths; injector keys off `ct.SpecData` concrete type.
  Same codec-header divergence as livesim2 — solved by the codec-tagged level-(b) API.
- **SEI insertion = rewriting existing samples** (content is pre-encoded, §2.1). Samples are already
  split/re-serialized to AVCC at load (`internal/media.go`), so inserting one more NALU is cheap and
  idiomatic here — much lower friction than livesim2's default path.
- **Looping + concurrency hazard:** output frames reuse the same backing `Samples[origNr].Data`, and
  four packaging goroutines may run concurrently. **Do not mutate the shared slice in place** — inject
  into a per-emission copy (e.g. the `fs.Data` copy in `createFragment`, or a copy in the LOC/moq-mi
  loop). This argues for a level-(b) API that returns *new* NAL bytes the consumer splices into a copy,
  rather than an API that mutates a sample in place.
- **CENC:** for protected tracks the CMAF/LOCMAF chunk is encrypted after assembly
  (`internal/asset.go:962-972`); insert SEI into the clear sample first.
- **Non-SEI carriage option:** moq-mi and LOC attach rich per-object metadata (µs-epoch timestamps,
  `VideoMetadata`). A consumer *could* carry `cc_data` as its own MoQ extension rather than as video SEI
  — this is exactly why the level-(a) byte-pair/`cc_data` seam should stay public.

### Cross-cutting
- **Wall-clock is available at both injection points** and both derive it the same way (epoch/AST +
  media time / timescale), so #7 can standardize on a single "frame wall-clock ms" input.
- **Emulation prevention is not a consumer concern** if go-608 serializes via mp4ff's EBSPWriter (§3.1);
  the consumer only prepends a 4-byte length and splices.
- **One SEI NAL per access unit, before the first VCL NALU** is the target for both (fixed-allocation
  `cc_count` -> an SEI every frame, mostly padding when idle).

---

## 6. Open questions for #6 (carriage) / #7 (wall-clock)

1. **`cc_count` policy (→ #6/#7).** Fixed-allocation (full per-rate `cc_count` with DTVCC padding every
   frame — most interoperable) vs minimal `cc_count` when 608-only (normative §3.2). Both consumers
   have frame rate available, so either is implementable; recommend fixed-allocation default, exposed as
   an option.
2. **Where does the NAL-header wrap + sample re-pack live (→ #6)?** Recommendation §4 puts the codec NAL
   header inside go-608 (level b) and leaves only the 4-byte length + splice to the consumer. Confirm
   this split; the alternative (return only the SEI RBSP payload and make the consumer add the header)
   pushes codec logic back into two consumers — rejected here.
3. **Pull-by-time vs pull-by-frame-count (→ #7).** `NextFrameSEINALU(frameWallMS)` assumes the consumer
   drives one call per video frame in presentation order. livesim2 default path has no per-frame loop
   yet; confirm the Generator can also be driven purely by frame index + fps if a wall-clock per frame
   is awkward. Both consumers can supply either.
4. **Field-2 usage (→ #7).** Both consumers are single-caption (CC1). Confirm #7 emits field-1 only by
   default (field-2/`cc_type=01` reserved), consistent with the ≤30 fps "room for both fields" budget.
5. **HEVC carriage conformance (→ #6).** Payload is codec-identical, but the normative home for HEVC
   caption carriage is not SCTE 128-1 (normative §2 open item). Functional today; cite the exact spec if
   a conformance claim is made.
6. **livesim2 default-path performance (→ integration, not #6/#7).** Adding per-sample decode/re-mux to
   the byte-pass-through default path is a non-trivial change with a perf cost; may motivate a
   caption-only variant or limiting injection to the chunked path initially. Flag to livesim2 owners.
7. **In-place vs copy (→ API contract).** moqlivemock's shared/looped sample slices mean the API must
   not encourage in-place sample mutation; the level-(b) "return new NAL bytes" shape is the safe
   contract. Confirm #6/#7 never hand back an aliased slice.

---

## 7. Sources

Files actually read for this document.

**mp4ff** (`/Users/tobbe/proj/github/ev/mp4ff`):
- `sei/sei4.go` (CEA-608 parse-only; no builder)
- `sei/sei.go` (`WriteSEIMessages`, `SEIMessage` interface, `NewSEIData`/`SEIData`)
- `bits/ebspwriter.go` (`WriteSEIValue`, `WriteRbspTrailingBits`, emulation prevention)
- `avc/sei.go`, `hevc/sei.go` (`ParseSEINalu`, parse-only)
- `avc/nalus.go` (`GetNalusFromSample`), `avc/avc.go` (`NALU_SEI`), `hevc/hevc.go` (`NALU_SEI_PREFIX`)
- `mp4/sample.go` (`Sample`, `FullSample`, `PresentationTime`)
- `cmd/mp4ff-mvhevc/main.go` (inline NALU length-prefix packing precedent)

**livesim2** (`/Users/tobbe/proj/github/dashif/livesim2`) — read by exploration agent:
- `cmd/livesim2/app/handler_livesim.go`, `cmd/livesim2/app/livesegment.go`,
  `cmd/livesim2/app/sgai_slate.go`, `cmd/livesim2/app/asset.go`, `go.mod`

**moqlivemock** (`/Users/tobbe/proj/github/ev/moq-workspace/moqlivemock`) — read by exploration agent:
- `internal/asset.go`, `internal/media.go`, `internal/moqgroup.go`, `internal/const.go`,
  `internal/subtitle.go`, `internal/catalog.go`, `internal/pub/pub.go`, `internal/pub/moqmi.go`,
  `internal/sub/loc.go`, `internal/sub/mux.go`, `utils/contentgen/videogen.go`, `cmd/mlmpub/main.go`,
  `go.mod`

**Design/research context:**
- `../design/cea608-core-model.md`, `./normative-rules-608-708-a53.md`, `./prior-art-608.md`
