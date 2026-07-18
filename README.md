# go-608

[![Go](https://github.com/Eyevinn/go-608/workflows/Go/badge.svg)](https://github.com/Eyevinn/go-608/actions/workflows/go.yml)
[![golangci-lint](https://github.com/Eyevinn/go-608/workflows/golangci-lint/badge.svg)](https://github.com/Eyevinn/go-608/actions/workflows/golangci-lint.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Eyevinn/go-608.svg)](https://pkg.go.dev/github.com/Eyevinn/go-608)
[![license](https://img.shields.io/github/license/Eyevinn/go-608.svg)](https://github.com/Eyevinn/go-608/blob/main/LICENSE)

A pure-Go library for **CTA-608 / CEA-608** captions: encode + decode,
`cc_data` + SEI carriage per ATSC A/53 (AVC & HEVC), wall-clock caption
generation, and timed-text (SCC / WebVTT / SRT) I/O.

> **Status: implementation in progress.** This scaffolding is the first slice
> ([#12](https://github.com/Eyevinn/go-608/issues/12)); the package APIs below
> are the agreed design and land ticket by ticket. The full design is in
> [`SPEC.md`](SPEC.md), with per-decision rationale in [`docs/design/`](docs/design/).

## Why "608" and "cta608"

The modern standard is **CTA-608** (the org renamed from CEA to CTA), so the
core package is `cta608`. The legacy "CEA-608" spelling persists in prior art
and in the `mp4ff` dependency's API (`ParseCEA608`, `IsCEA608`); that spelling
is confined to the `carriage` package that wraps mp4ff.

## Packages

go-608 is a set of cooperating packages. The module has **no importable root
package** (the last path element `go-608` is not a valid Go identifier); import
the packages directly.

| Package    | Responsibility                                                        |
|------------|----------------------------------------------------------------------|
| `cta608`   | Pure core: Token stream, `Screen`, `Serialize`/`Parse`, `Decoder`/`Encoder`, `CaptionBlock`. A dependency-free leaf. |
| `carriage` | `cc_data` / T.35 / SEI / NAL carriage for AVC & HEVC. The only mp4ff importer. |
| `schedule` | Timed tokens → per-frame `{Field1, Field2, CCCount}`. The shared timing layer. |
| `generate` | Wall-clock caption `Generator` (the first milestone).                |
| `scc`      | Scenarist SCC read/write — byte-exact, true SMPTE drop-frame.        |
| `cue`      | Shared `TimedCue` intermediate + the 608↔cue mapping + a plugin seam. |
| `webvtt`   | WebVTT serializer over the `cue` model.                              |
| `srt`      | SRT serializer over the `cue` model.                                 |

### Dependency layering

```
cta608 (pure, leaf) ─┬─ scc
                     ├─ cue ─┬─ webvtt
                     │       └─ srt
                     ├─ carriage (+ mp4ff)   ← the only mp4ff importer
                     └─ schedule ── generate
cmd/ (go608-extract|inject|clock|info) wire the above; internal/ holds version + cmd glue.
```

## The `cta608` wire boundary

The core spine is a **wire-faithful token stream**. `Serialize` turns tokens
into odd-parity `cc_data` byte pairs and `Parse` turns them back, so a `[]Token`
round-trips bytes exactly.

The token sum type (closed under the `Token` interface):

| Token            | Meaning                                                       |
|------------------|---------------------------------------------------------------|
| `Chars`          | a run of characters (standard, special, and extended glyphs)  |
| `PAC`            | Preamble Address Code: row + base pen, or row + indent        |
| `MidRow`         | mid-row style change (color/underline/italics)                |
| `TabOffset`      | shift the cursor 1–3 columns                                  |
| `BackgroundAttr` | background color / transparent bg / black-foreground          |
| `SetMode`        | pop-on (RCL), roll-up (RU2/3/4), or paint-on (RDC)            |
| `Command`        | misc control: EOC, EDM, ENM, CR, BS, DER, TR, RTD, FON, …     |

`Serialize`/`Parse` own **all** byte concerns so the token model stays logical:
odd parity, control-code **doubling** (on for field 1, off for field 2 by
default, overridable), two-characters-per-pair packing, null-pair **frame
alignment** of two-byte control codes, and extended-character
**backspace-and-replace**. `ParseOptions` chooses validate-vs-strip parity;
`SerializeOptions` selects the field, channel, and doubling policy.

```go
tokens := []cta608.Token{
    cta608.SetMode{Mode: cta608.PopOn},
    cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
    cta608.Chars{Text: "HELLO"},
    cta608.Command{Op: cta608.EOC},
}
data := cta608.Serialize(tokens, cta608.SerializeOptions{}) // field 1, doubling on
back, _ := cta608.Parse(data, cta608.ParseOptions{})        // back == tokens
```

`DemuxField`/`MuxField` split and join the two in-field data channels by the
control byte's high nibble. `Screen`/`Row`/`Run`/`Pen` are the sparse, derived
display value types (`Pen` is a comparable value); the stateful `Decoder`,
`Encoder`, and `CaptionBlock` that interpret and diff a `Screen` land in later
tickets. A runnable round-trip lives in [`examples/`](examples/).

## Carriage (`cc_data` / SEI)

The `carriage` package is the only mp4ff importer and the seam between 608 byte
pairs and the elementary stream. It is pure and timing-free — the caller supplies
`ccCount` (from `schedule`); carriage never imports the `cta608` core.

**Encode** — build one frame's SEI NAL unit from the per-field byte pairs:

```go
// field1/field2 are each 0 or 2 bytes; ccCount comes from the frame rate (SPEC §5.3).
nalu := carriage.FrameSEINALU(field1, field2, ccCount, carriage.CodecAVC)
// nalu is a BARE NAL unit — the consumer prepends the 4-byte length and splices
// it before the first VCL NALU (into a per-emission copy, before CENC).
```

`FrameSEINALU` is `BuildCCData` (assemble the `cc_data()` per CTA-708-E §4.3: 608
constructs first, then DTVCC padding to `ccCount`) → `SEIMessage` (wrap as a
`user_data_registered_itu_t_t35` / GA94 SEI message) → `NALU` (serialize via mp4ff
and prepend the codec NAL header — AVC `0x06` or HEVC prefix-SEI 39). The three
"nothing here" encodings — an empty field pair, DTVCC padding, and the `0x80 0x80`
608 null pair — are kept distinct.

`SEIMessage` returns an mp4ff `sei.SEIMessage` and takes **no codec** — the payload
is codec-identical for AVC and HEVC. Use `NALU` to place the 608 message in one NAL
unit **together with other SEI messages** (e.g. `pic_timing`); the codec is only
needed there, for the NAL header:

```go
msg := carriage.SEIMessage(carriage.BuildCCData(field1, field2, ccCount))
nalu := carriage.NALU(carriage.CodecAVC, msg, otherSEIMessage) // one NAL, N messages
```

**Decode** — recover the field byte-pair streams from a sample's NAL units:

```go
field1, field2, err := carriage.FieldPairs(sampleNALUs, carriage.CodecAVC)
```

`FieldPairs` wraps mp4ff's `sei.ParseCEA608`; the recovered pairs feed the `cta608`
core `Decoder`. See `examples/` for a runnable round-trip and `testdata/` for a
fragmented-mp4 fixture.

## Scheduling (timed tokens → frames)

The `schedule` package is the shared timing layer between the logical token
stream and the per-frame carriage payload. It is format-agnostic and
carriage-free — it imports only `cta608` — so both the wall-clock `generate`r and
the subtitle-compile path drive the same scheduler.

A `Scheduler` holds a FIFO byte-pair queue per NTSC field. `Push` serializes a
wall-time-tagged batch of token transitions with `cta608.Serialize` and enqueues
the resulting 2-byte pairs; `Frame(frameWallMS)` drains **at most one pair per
field per frame** and reports the frame's `cc_count`, returning the primitive
`{Field1, Field2, CCCount}` triple `carriage` consumes.

```go
s := schedule.NewScheduler(30) // 30 fps → cc_count 20
s.Push(schedule.TimedTokens{TimeMS: 0, Tokens: tokens})

f := s.Frame(frameWallMS)      // ≤1 pair/field, padded to CCCount
nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
```

- **`cc_count` per frame rate** (CTA-708-E §4.3.6, `round(600/fps)`): 23.976/24→25,
  25→24, 29.97/30→20, 50→12, 59.94/60→10. `CCCountFull` (the default) emits that
  full count and lets `carriage` pad the surplus with DTVCC padding;
  `CCCountMinimal` emits just the two 608 field constructs.
- **608 rate cap:** at ≤30 fps a frame carries one field-1 **and** one field-2
  pair; above 30 fps only **one** 608 pair per frame (field 1 first).
- **Frame alignment:** `Serialize` emits whole 2-byte pairs and `Frame` drains
  whole pairs, so a two-byte control code never straddles a frame. An idle field
  yields a 0-byte pair (distinct from the `0x80 0x80` 608 null pair and from
  DTVCC padding).

> `Push` takes `schedule.TimedTokens` (a wall-clock `TimeMS` plus a `Field`
> selector), not `cue.TimedTokens` — depending on `cue` would break the layering
> rule (`schedule` imports only `cta608`). See the package godoc.

A runnable `schedule` → `carriage` → decode round-trip lives in [`examples/`](examples/).

## Command-line tools

| Tool            | Purpose                                                        |
|-----------------|---------------------------------------------------------------|
| `go608-clock`   | Generate a wall-clock caption and splice it into an mp4.       |
| `go608-info`    | Dump `cc_data` / tokens / rendered `Screen` from a file or bytes. |
| `go608-extract` | mp4 with 608 → WebVTT / SRT / SCC (format-only conversion is a mode). |
| `go608-inject`  | WebVTT / SRT / SCC → mp4 with 608 SEI (format-only conversion is a mode). |

Each tool supports `--version` (stamped from git via `-ldflags` at build time).

## Building

Requires **Go 1.25+**.

```sh
make all      # check (vet + lint) + build + test
make build    # go build ./... and the four cmd binaries into ./out
make test     # go test ./...
make coverage # coverage profile + function summary
```

The build stamps version and commit date into `internal` via `-ldflags`;
`make lint` runs `golangci-lint` when it is installed (CI always enforces it).

## Development

- Documentation is versioned: the detailed reference lives in this README and in
  the package documentation ([pkg.go.dev](https://pkg.go.dev/github.com/Eyevinn/go-608));
  [`CHANGELOG.md`](CHANGELOG.md) stays terse.
- Optional pre-commit hooks: `pip install pre-commit && pre-commit install`
  (see [`.pre-commit-config.yaml`](.pre-commit-config.yaml)).
- CI runs four workflows on every PR: **Go** (build + test on Linux/macOS/Windows),
  **Coverage**, **golangci-lint**, and **govulncheck**.

## License

[MIT](LICENSE) © 2026 Eyevinn Technology AB.

## ChangeLog and Versions

See [CHANGELOG.md](CHANGELOG.md).

## Support

Join our [community on Slack](http://slack.streamingtech.se) where you can post any questions regarding any of our open source projects. Eyevinn's consulting business can also offer you:

* Further development of this component
* Customization and integration of this component into your platform
* Support and maintenance agreement

Contact [sales@eyevinn.se](mailto:sales@eyevinn.se) if you are interested.

## About Eyevinn Technology

[Eyevinn Technology](https://www.eyevinntechnology.se) is an independent consultant firm specialized in video and streaming. Independent in a way that we are not commercially tied to any platform or technology vendor. As our way to innovate and push the industry forward we develop proof-of-concepts and tools. The things we learn and the code we write we share with the industry in [blogs](https://dev.to/video) and by open sourcing the code we have written.

Want to know more about Eyevinn and how it is to work here. Contact us at work@eyevinn.se!
