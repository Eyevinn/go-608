// Package carriage carries CTA-608 caption data as cc_data() inside AVC/HEVC
// SEI, wrapping github.com/Eyevinn/mp4ff for the SEI/NAL layer.
//
// It is the only package in go-608 that imports mp4ff, and it is pure and
// timing-free: the per-frame cc_count is supplied by the caller (from the
// schedule package). BuildCCData assembles one frame's cc_data() from per-
// field byte pairs; SEINALU wraps it as a T.35/GA94 payload inside an SEI
// message and prepends the codec NAL header, returning a bare NAL unit (no
// 4-byte length prefix). FieldPairs is the decode wrapper that recovers the
// field byte-pair streams from a sample's NAL units.
//
// See SPEC.md section 4.2 / 5.1-5.2 and docs/design/cea608-carriage-seam.md.
package carriage
