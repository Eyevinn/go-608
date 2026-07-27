# testdata

Shared test fixtures for go-608. Populated by the package tickets as they land:

- raw `cc_data` / byte-pair vectors for the `cta608` core round-trip tests (#13);
- `carriage-608-avc.mp4` — a fragmented mp4 (AVC track, CTA-608 SEI) for `carriage` /
  `go608-info` / `go608-extract` (#16, #24, #25). Golden file; regenerate with
  `go test ./carriage/ -run TestCarriageMP4FixtureRoundTrip -update`;
- `av01-clean.mp4` and `av01-clean-hierarchical.mp4` — real av01 fragmented mp4s without
  captions, the injection targets for AV1 CTA-608 carriage (#49, see below);
- sample `.scc` files, both `;` (drop-frame) and `:` variants (#19);
- sample `.vtt` and `.srt` files (#21, #22).

Keep fixtures small and, where possible, human-readable.

## av01 fixtures (#49)

Unlike `carriage-608-avc.mp4`, which go-608 synthesizes itself (real SPS/PPS, dummy VCL
payloads), these are **real, decodable** AV1 bitstreams from an encoder — so injected
captions can be validated with an independent decoder. Both are 256x144, 30 fps, 5 frames,
Main profile (`av01.0.00M.08`), one fragment, `ftyp + moov + moof + mdat` with no `mfra`
and no encoder-tag `meta` box. `-fflags/-flags:v +bitexact` makes them byte-reproducible:
re-running the command below yields an identical file.

Regenerate with the full ffmpeg (8.1.2, libsvtav1 4.2.0) at `/opt/homebrew/bin/ffmpeg`:

```sh
# av01-clean.mp4 — one frame OBU per temporal unit (low delay)
ffmpeg -y -fflags +bitexact -f lavfi -i "testsrc2=size=256x144:rate=30" -frames:v 5 \
  -c:v libsvtav1 -preset 8 -crf 63 -pix_fmt yuv420p \
  -svtav1-params "pred-struct=1:lookahead=0" \
  -flags:v +bitexact -map_metadata -1 \
  -movflags +frag_keyframe+empty_moov+default_base_moof+skip_trailer \
  testdata/av01-clean.mp4

# av01-clean-hierarchical.mp4 — same, minus -svtav1-params (default hierarchical GOP)
```

`av01-clean.mp4` is the simple case, one frame per sample:

```
sample 1  sync   SequenceHeader(11B) Frame(979B)
sample 2         Frame(27B)
sample 3         Frame(30B)
sample 4         Frame(26B)
sample 5         Frame(121B)
```

`av01-clean-hierarchical.mp4` is the awkward real-world case a default encoder produces,
and exists to keep the OBU-placement decision (#52) honest:

```
sample 1  sync   SequenceHeader(11B) Frame(848B)
sample 2         Frame(68B) Frame(23B) Frame(21B)   <- three frame OBUs in one temporal unit
sample 3         FrameHeader(1B)                    <- 3-byte sample, show_existing_frame
sample 4         Frame(21B)
sample 5         FrameHeader(1B)
```

Notes that fall out of these fixtures:

- **No temporal-delimiter OBUs.** The mp4 muxer drops them, as the AV1 ISOBMFF binding
  allows, so a sample starts directly with a sequence-header or frame OBU. IVF from the same
  encoder *keeps* them, so a placement rule for the caption OBU has to anchor on the first
  frame / frame-header OBU rather than on "first in the sample" to mean the same thing in
  both containers — #52.
- A temporal unit can hold **several frame OBUs**, and a sample can hold **no frame data at
  all** (a bare `show_existing_frame` frame header). Neither is an *assignment* ambiguity: one
  sample carries one temporal unit, which outputs exactly one frame, so it is one caption OBU
  per sample. In sample 2 only one of the three frame OBUs is output and the others are hidden
  references; the 3-byte samples are still an `OBU_FRAME_HEADER` and so still an anchor. What
  is left for #52 is *where in the OBU sequence* the caption OBU goes.
- **Every composition-time offset is 0** (`pts == dts` on all five samples, hierarchical GOP
  included). AV1 reorders inside the bitstream — hidden frames plus `show_existing_frame` —
  so it never reaches the container, and AV1 does **not** have the decode-order caption
  assignment problem that AVC/HEVC do. Verify with
  `ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts -of csv=p=0 FILE`.
- The sequence header appears both in the `av1C` `configOBUs` and inline in sample 1.

### No captioned av01 reference is obtainable locally (#49 verdict)

A real-world av01-with-608 reference could **not** be produced with the available tooling.
What was tried:

| Attempt | Result |
|---|---|
| `/opt/homebrew/bin/ffmpeg` AV1 encoders | `libsvtav1` only; no `libaom-av1` |
| `/usr/local/bin/ffmpeg` (git build) AV1 encoders | none at all |
| `-a53cc 1` with `-c:v libsvtav1` | option accepted but *"has not been used for any stream"* — it is an x264/x265 private option |
| transcode a real AVC+608 mp4 to av01 | output contains **zero** metadata OBUs; the captions are dropped |
| `aomenc --help` | no caption/A-53/SEI/metadata option |

So: no local encoder inserts `metadata_itu_t_t35` OBUs. Candidate external sources for a
ground-truth sample are AOM test vectors, a cloud encoder, or a captured stream.

**Fallback validation plan.** ffmpeg can be used as the *independent decoder* even without
a reference file, because its AV1 decoder exports A/53 captions through the same
`ff_itut_t35_parse_buffer` path as AVC/HEVC. Extract 608 from any mp4 with:

```sh
ffmpeg -f lavfi -i "movie=FILE.mp4[out0+subcc]" -map 0:1 out.srt
```

This was verified end to end on the AVC side: a real libx264 clip injected with
`go608-inject -sub testdata/srt/basic.srt` comes back out of ffmpeg as
`Hello, world!` / `Second caption / on two lines` with the original timings. Pointing the
same command at our own av01 output therefore validates the OBU against a third-party
implementation, which is weaker than a reference file for byte-exactness but strong for
interop.

Caveat found while establishing this: the extraction only matches when the input has **no
B-frames** (`-bf 0`). With B-frames it comes back permuted, because captions are currently
injected in decode order — see the composition-order issue (#54). That affects **AVC and
HEVC** identically; the av01 fixtures above are unaffected, since their composition offsets
are all 0. Note that `-preset ultrafast` disables B-frames in x264 but not in x265, so it is
an easy way to reproduce nothing when trying to trigger this.
