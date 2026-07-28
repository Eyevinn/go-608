// Package carriage carries CTA-608 caption data as cc_data() inside AVC/HEVC SEI
// or an AV1 metadata OBU, wrapping github.com/Eyevinn/mp4ff for the SEI/NAL and
// OBU layers.
//
// It is the only package in go-608 that imports mp4ff, and it is pure and
// timing-free: the per-frame cc_count is supplied by the caller (from the
// schedule package), and it operates on raw byte pairs without importing the
// cta608 core.
//
// # Encode
//
// BuildCCData assembles one frame's cc_data() from the field-1 and field-2 byte
// pairs (parity already applied by the cta608 serializer), placing the two 608
// constructs first and padding to cc_count with DTVCC constructs, per CTA-708-E
// §4.3. SEIMessage wraps a cc_data() payload as a user_data_registered_itu_t_t35
// (T.35/GA94) SEI message and returns it as an mp4ff sei.SEIMessage; the message
// is codec-identical for AVC and HEVC, so SEIMessage takes no codec. NALU
// serializes one or more SEI messages into a bare SEI NAL unit for a given codec
// (no 4-byte length prefix) — pass a 608 message together with any other SEI
// messages to place them in the same NAL unit. FrameSEINALU is the one-call
// convenience for a single 608 frame (BuildCCData + SEIMessage + NALU).
//
// # Samples
//
// The NAL unit still has to get into an mp4 sample, which is the same work for
// every consumer: SpliceSEIBeforeVCL inserts a bare SEI NAL unit into a
// length-prefixed sample immediately before its first VCL NAL unit, adding the
// 4-byte length prefix. SampleNALUs and PrefixNALUs are the split/join either side
// of it — SampleNALUs also feeds FieldPairs on the way back in — and IsVCL is the
// coded-slice predicate the splice point is found with. A caller that has an
// mp4.FullSample needs only:
//
//	data, err := carriage.SpliceSEIBeforeVCL(s.Data, seiNALU, codec)
//	// ...
//	s.Data, s.Size = data, uint32(len(data))
//
// A field pair is either 0 or 2 bytes, and carriage keeps the distinct "nothing
// here" encodings distinct (SPEC §5.3): an empty pair is a cc_valid=0 608
// construct ("no waveform this field this frame"), DTVCC padding is a cc_valid=0
// cc_type=10 construct, and the 608 null pair 0x80 0x80 is a live cc_valid=1
// construct.
//
// # Decode
//
// FieldPairs recovers the field byte-pair streams from a sample's NAL units. It
// decodes the SEI framing directly (sei.ExtractSEIData) and reuses mp4ff's
// sei.ParseCTA608 for the type-4 T.35 messages, ignoring any other SEI message
// types in the sample. It is the inverse of the encode path and feeds the cta608
// core Decoder. (Because ParseCTA608 drops pairs whose seven low bits are all
// zero, the 0x80 0x80 null pair does not survive this specific round-trip; that is
// a property of mp4ff's parser, not of the builder.)
//
// The codec is an explicit Codec argument on NALU, FieldPairs, SpliceSEIBeforeVCL
// and IsVCL (it selects the NAL-unit header and the VCL types); carriage never
// sniffs it. Message building and the length framing are codec-free.
//
// # AV1
//
// AV1 carries the same cc_data() — BuildCCData is reused unchanged — under the same
// T.35/GA94 header; only the envelope and the splice differ. MetadataOBU wraps a
// cc_data() payload as a metadata_itu_t_t35 OBU (no emulation prevention, unlike an
// SEI NAL unit), FrameMetadataOBU is the one-call form mirroring FrameSEINALU,
// SpliceOBUBeforeFrame places the OBU in a sample before the first frame OBU, and
// OBUFieldPairs reads the pairs back out of a raw sample.
//
// These run parallel to the SEI functions rather than extending them, and none takes
// a Codec — Codec names NAL framing, which AV1 does not have, so it stays two-valued.
// A consumer handling all three codecs owns its own three-value discriminator; that
// is deliberate, since adding an AV1 value to Codec would have left every existing
// consumer switch compiling while quietly captioning nothing for av01.
//
// AV1 support is scoped to non-scalable streams (OperatingPointIdc == 0): one caption
// OBU per sample is well defined only because a temporal unit shows exactly one
// frame, which the spec guarantees only in that case.
//
// See SPEC.md section 4.2 / 5.1-5.2, docs/design/cea608-carriage-seam.md, and
// docs/research/av1-metadata-obu-608-layout.md.
package carriage
