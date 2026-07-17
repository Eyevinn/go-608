// Package webvtt is a thin WebVTT serializer over the cue model: it
// implements cue.Reader and cue.Writer and owns only WebVTT syntax and its
// styling/positioning quantization.
//
// Styling is carried across the lossy boundary quantized to 608's palette
// (color via <c.name> spans plus a STYLE block, italic/underline via <i>/<u>;
// bold and font/size/region are dropped). Positioning maps line:/position:/
// align: to and from the grid Row/Column, approximately; position-less cues
// anchor bottom-center. All 608<->cue logic lives in the cue package.
//
// See SPEC.md section 4.6 / 8.2 and docs/design/cea608-webvtt-srt-io.md.
package webvtt
