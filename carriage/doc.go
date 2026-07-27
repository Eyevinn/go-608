// Package carriage carries CTA-608 caption data as cc_data() inside AVC/HEVC
// SEI, wrapping github.com/Eyevinn/mp4ff for the SEI/NAL layer.
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
// convenience for a single 608 frame (BuildCCData + SEIMessage + NALU). The
// consumer adds the length prefix and splices the NAL before the first VCL NALU.
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
// The codec is an explicit Codec argument on NALU and FieldPairs (it selects the
// NAL-unit header); carriage never sniffs it. Message building is codec-free.
//
// See SPEC.md section 4.2 / 5.1-5.2 and docs/design/cea608-carriage-seam.md.
package carriage
