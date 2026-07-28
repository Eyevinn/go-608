# testdata

Shared test fixtures for go-608. Populated by the package tickets as they land:

- raw `cc_data` / byte-pair vectors for the `cta608` core round-trip tests (#13);
- `carriage-608-avc.mp4` — a fragmented mp4 (AVC track, CTA-608 SEI) for `carriage` /
  `go608-info` / `go608-extract` (#16, #24, #25). Golden file; regenerate with
  `go test ./carriage/ -run TestCarriageMP4FixtureRoundTrip -update`;
- `av01-clean.mp4` and `av01-clean-hierarchical.mp4` — real av01 fragmented mp4s without
  captions, the injection targets for AV1 CTA-608 carriage (#49, see below);
- `bframes-avc.mp4` and `bframes-hevc.mp4` — real reordered AVC/HEVC, the fixtures that
  keep the presentation-order caption assignment honest (#54, see below);
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

Since verified on the av01 side too: injecting `srt/basic.srt` into a 10 s libsvtav1 clip
built the same way comes back out of ffmpeg **byte-identical to the AVC output** of the same
injection. That is the interop bar #49 defined, met.

Caveat found while establishing this, now fixed: the extraction originally only matched when
the input had **no B-frames** (`-bf 0`), because captions were injected in decode order (the
composition-order issue, #54). Captions are now assigned in presentation order, so B-frame
input round-trips correctly too — see the B-frame fixtures below.

## B-frame fixtures (#54)

`bframes-avc.mp4` and `bframes-hevc.mp4` exist because the composition-order bug was
invisible against every fixture that came before them: `carriage-608-avc.mp4` has no
composition offsets and starts at `pts=0`, and the av01 fixtures reorder inside the
bitstream rather than in the container. Both properties these fixtures add are
load-bearing — **non-zero composition offsets** exercise the ordering rule, and
**`start_pts=1024`** exercises the timing origin.

Both are 128x72, 30 fps, 30 frames, one fragment, `-bf 3`. Regenerate with the full
ffmpeg (8.1.2) at `/opt/homebrew/bin/ffmpeg`, which has libx264/libx265 (the PATH ffmpeg
does not); `+bitexact` makes them byte-reproducible.

```sh
ffmpeg -y -fflags +bitexact -f lavfi -i "testsrc2=size=128x72:rate=30" -frames:v 30 \
  -c:v libx264 -preset veryfast -crf 45 -pix_fmt yuv420p -g 30 -bf 3 \
  -flags:v +bitexact -map_metadata -1 \
  -movflags +frag_keyframe+empty_moov+default_base_moof+skip_trailer \
  testdata/bframes-avc.mp4

# bframes-hevc.mp4 — the same, with:
#   -c:v libx265 -x265-params "log-level=none:bframes=3"
```

**Gotcha:** `-preset ultrafast` disables B-frames in x264 but *not* in x265, so it is an
easy way to reproduce nothing. Use `veryfast` plus an explicit `-bf 3`. Verify the
reordering survived with:

```sh
ffprobe -v error -select_streams v:0 -show_entries packet=pts,dts -of csv=p=0 FILE
```

Both files come out with the same pattern — `1024,0` / `3072,512` / `2048,1024` / … — so
presentation order genuinely differs from decode order.

### What they demonstrate

Injecting `srt/basic.srt` into a 10 s clip built the same way and reading it back with
ffmpeg as an independent decoder shows the bug and the fix directly:

```sh
go608-inject -i bf.mp4 -sub testdata/srt/basic.srt -o bf-608.mp4 -fps 30
ffmpeg -f lavfi -i "movie=bf-608.mp4[out0+subcc]" -map 0:1 out.srt
```

| | field 1 as ffmpeg reads it |
|---|---|
| decode-order assignment (before) | `llHeo, ldor! w` |
| presentation-order assignment (after) | `Hello, world!` |

Measured identically on AVC and HEVC. The internal round-trip was green throughout,
because the read side made the same mistake as the write side — which is why the
regression test asserts against the *presentation* index rather than against a
round-trip.
