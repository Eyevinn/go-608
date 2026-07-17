# Package decomposition & public API (design note)

Design note for wayfinder ticket #10 — the capstone. Settles the final package tree, names,
dependency edges, and public/internal split for `github.com/Eyevinn/go-608`, once the domain model
(#5), carriage seam (#6), wall-clock generation (#7), SCC I/O (#8), and WebVTT/SRT I/O (#9) are all
decided. This is the **last design decision**; the deliverable spec is assembled from all the notes
after this. References the repo-conventions Notes on the map (follow
[hi264](https://github.com/Eyevinn/hi264); library layout mirrors
[mp4ff](https://github.com/Eyevinn/mp4ff)).

## Decisions

### P1 — Top-level packages; module `github.com/Eyevinn/go-608`; no importable root package

- **Confirmed (T.E.):** library packages live at the **repo root** (mp4ff style —
  `github.com/Eyevinn/go-608/cta608`, `/scc`, `/carriage`, …), **not** nested under `pkg/` (the
  hi264 style). go-608 is a media-format library of cooperating packages, which is mp4ff-shaped; the
  "library layout mirrors mp4ff/mp4" half of the Notes wins for the library tree. `cmd/`,
  `internal/`, `examples/`, `testdata/` remain top-level per hi264.
- **`go-608` is a valid module/import path** (hyphens are legal in path *elements*). The Go rule that
  bites is on **package identifiers**: `package go-608` (hyphen) and `package 608` (leading digit)
  are both illegal. Since the last path element isn't a valid identifier, go-608 has **no importable
  root package**. Optional: a doc-only `doc.go` at root declaring `package go608` purely for the
  godoc landing page (mirrors mp4ff's `doc.go`/`package mp4ff`); tools tolerate the name≠last-element
  mismatch (cf. `gopkg.in/yaml.v2` → `package yaml`).

### P2 — Core package is `cta608`

- **Confirmed (T.E.):** the pure core package is **`cta608`** (not `cea608`). Matches the current
  normative standard **ANSI/CTA-608-E** and dash.js's modern `Cta608Parser`; principled for a
  greenfield library. Cost accepted: **mp4ff keeps its legacy `CEA608` spelling** (`sei.ParseCEA608`,
  `ITUData.IsCEA608`) at the carriage seam — a documented, harmless vocabulary mismatch confined to
  the `carriage` package. The design-doc + glossary `cea608` placeholders were swept to `cta608`;
  `.md` filenames and the "CEA-608" *standard-name* prose are left as-is.

### P3 — Package set & dependency DAG

```
github.com/Eyevinn/go-608/
  cta608/     pure core: Token/Screen/CaptionBlock/Pen/Run/Row, Serialize/Parse,
              Decoder/Encoder, thin field mux/demux, unexported char/PAC/parity/roll-up
              tables.                                          deps: — (leaf, no heavy deps)
  scc/        SCC (Scenarist) read/write — byte-pair container (#8)   deps: cta608
  cue/        shared TimedCue{Start,End,Content} + Reader/Writer seam +
              the 608↔cue mapping (Segment / Compile) written once (#9) deps: cta608
  webvtt/     WebVTT serializer — implements cue.Reader/Writer (#9)   deps: cue, cta608
  srt/        SRT serializer     — implements cue.Reader/Writer (#9)   deps: cue, cta608
  carriage/   cc_data / T.35 / SEI / NAL; Codec enum — wraps mp4ff (#6) deps: cta608, mp4ff
  schedule/   timed tokens → per-frame {field1,field2,ccCount}; cc_count/fps
              policy + 1-pair/frame cadence + null-pair alignment (#3/#7/#9) deps: cta608
  generate/   wall-clock Generator: NextFrame(frameWallMS) (#7)       deps: cta608, schedule
  cmd/        go608-extract, go608-inject, go608-clock, go608-info
  internal/   version stamping + shared cmd/mp4 glue
  examples/   testdata/
```

- **`cta608` is a pure leaf** — no heavy deps; the char/PAC/parity/roll-up tables are **unexported
  inside `cta608`**, not a separate package.
- **mp4ff is isolated to `carriage`** — no other package imports it, and it is not pulled in
  transitively (see P3.1). Keeps the whole core + timing + I/O stack testable without mp4ff.
- **The 608↔cue mapping lives in `cue`** (written once, #9); `webvtt`/`srt` only serialize cues
  ↔ files. `cue` is the published extension seam — TTML and third-party formats implement
  `cue.Reader`/`cue.Writer` with zero change to `cue`/`cta608`.

#### P3.1 — `schedule`/`generate` are siblings of `carriage`, not dependents (keeps mp4ff out)

- **Confirmed (T.E.):** `schedule` decides *cc_count* (fps policy, #3) and *which byte-pairs go in
  each frame* (drain field queues, 1 pair/frame cadence, null-pair alignment), emitting a per-frame
  **`{field1, field2, ccCount}`** triple. It does **not** build `cc_data` bytes. `carriage` consumes
  that triple (`BuildCCData(f1, f2, ccCount)` → `SEINALU`). So `carriage` ⟂ `schedule`/`generate`
  — **siblings**, both depending only on `cta608`, joined by the caller passing the primitive triple.
  This matches the #6 encode flow and means the timing layer never imports mp4ff.
- **`schedule` vs `generate` split (T.E.):** `schedule` is the **generic** timed-tokens→frames
  scheduler, reused by both the wall-clock `generate` (#7, generated clock content) **and** the
  subtitle-compile path (#9 `cue.Compile` output). `generate` builds clock content and drives it
  through `schedule`. Both are **format-agnostic** — neither imports `cue`/`webvtt`/`srt`; the
  "subtitle file → injected frames" wiring lives in a `cmd` tool / `examples`.

### P4 — `cmd/`: four small binaries

- **Confirmed (T.E.):** several small focused binaries (Eyevinn house style — hi264 and mp4ff both):
  - **`go608-extract`** — fragmented mp4 with embedded 608 → WebVTT / SRT / SCC (and a token/screen
    dump). Pipeline: `carriage.FieldPairs` → `cta608.Decoder` → `cue.Segment` → `webvtt`/`srt`/`scc`.
  - **`go608-inject`** — WebVTT / SRT / SCC → mp4 with 608 SEI. Pipeline:
    `webvtt`/`srt`/`scc` → `cue`/`cta608` → `schedule` → `carriage` → splice into the mp4.
  - **`go608-clock`** — the wall-clock caption demo (#7 first milestone): `generate` → `schedule` →
    `carriage` → mp4.
  - **`go608-info`** — debug dumper: cc_data / tokens / rendered `Screen` from a file or raw bytes.
- Format-only conversion (SCC↔WebVTT↔SRT, no mp4) is a **mode of `extract`/`inject`**, not a fifth
  tool. Shared mp4 read/write + arg-parsing glue goes in `internal/`.

### P5 — Public vs internal

- **Public:** all eight library packages (`cta608`, `scc`, `cue`, `webvtt`, `srt`, `carriage`,
  `schedule`, `generate`).
- **`internal/`:** version stamping (`internal/version` — `commitVersion`/`commitDate` injected via
  LDFLAGS, the hi264 pattern) and shared `cmd` helpers (mp4 I/O, flag handling) that must not be part
  of the public API.
- Core tables are **unexported within `cta608`** — implementation detail, not `internal/`.

### P6 — Inherited repo conventions (from hi264, unchanged except Go version)

- Makefile (`all: check build test`), **golangci-lint**, three GitHub workflows (go / coverage /
  golangci-lint), pre-commit via `venv`, **MIT** license, module `github.com/Eyevinn/go-608`.
- **Go 1.25** (T.E. — overrides hi264's 1.24).
- `examples/` (runnable snippets per package) and `testdata/` (shared sample SCC / WebVTT / SRT files,
  a fragmented mp4 carrying 608, and raw `cc_data` vectors for the core round-trip tests).

## Public API surface (consolidated, per package)

| package | key exported surface (detail in the cited note) |
|---|---|
| `cta608` | `Token`/`Chars`/`PAC`/`MidRow`/`SetMode`/`Command`…, `Screen`/`Row`/`Run`/`Pen`/`Color`, `CaptionBlock`, `Serialize`/`Parse`, `Decoder`/`Encoder`, `DemuxField`/`MuxField` (#5) |
| `scc` | `SCCFile`/`Entry`, `Read`/`Write`, `FrameToTimecode`/`TimecodeToFrame`, `TimedPairs`, `GroupPairs` (#8) |
| `cue` | `TimedCue`, `Reader`/`Writer`, `Segment`, `Compile` (#9) |
| `webvtt`,`srt` | `Read`/`Write` implementing `cue.Reader`/`cue.Writer` (#9) |
| `carriage` | `Codec`, `BuildCCData`, `SEINALU`, `FrameSEINALU`, `FieldPairs` (#6) |
| `schedule` | scheduler over timed tokens → per-frame `{field1,field2,ccCount}` (new; #3/#7/#9) |
| `generate` | `Generator`, `NewGenerator`, `NextFrame`, `Config`/`LineSpec` (#7) |

## Layering (bottom → top)

```
cta608  (pure, leaf) ─┬─ scc
                      ├─ cue ─┬─ webvtt
                      │       └─ srt
                      ├─ carriage (+ mp4ff)        ← the ONLY mp4ff importer
                      └─ schedule ── generate
cmd/ (go608-extract|inject|clock|info) wire the above; internal/ holds version + cmd glue.
```

## Resolves / closes out

- The map's **"generic timed-tokens → per-frame scheduler"** fog item is resolved here: it is the
  `schedule` package (P3.1).
- With #10 closed, **all design decisions have landed** → the **"Spec-document assembly"** fog
  graduates into the final ticket: consolidate `docs/design/*.md` + `docs/research/*.md` + these
  conventions into the single deliverable spec for the `/implement` hand-off.
- **TTML ↔ 608** remains future fog behind the `cue` seam (P3).
