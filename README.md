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
