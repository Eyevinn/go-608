// Package webvtt is a thin WebVTT serializer over the cue model: it
// implements cue.Reader and cue.Writer and owns only WebVTT syntax and its
// styling/positioning quantization.
//
// The entry points are the package-level Read and Write (the cue.Reader /
// cue.Writer contract of SPEC section 4.6), with Reader and Writer value types
// that satisfy the published plugin seam. Read/Write is a semantic, quantized
// round-trip — not byte-exact, unlike the SCC/SEI containers.
//
// Styling is carried across the lossy boundary quantized to 608's palette
// (color via <c.name> spans plus a STYLE block, italic/underline via <i>/<u>;
// bold and font/size/region are dropped). Positioning maps line:/position:/
// align: to and from the grid Row/Column, approximately; position-less cues
// anchor bottom-center. All 608<->cue logic lives in the cue package.
//
// See SPEC.md section 4.6 / 8.2 and docs/design/cea608-webvtt-srt-io.md.
package webvtt
