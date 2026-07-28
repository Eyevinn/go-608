package carriage

import (
	"fmt"

	"github.com/Eyevinn/mp4ff/av1"
)

// This file is the AV1 half of the carriage seam. It runs parallel to the SEI path
// rather than extending it: AV1 has no NAL units and no Codec to choose between, so
// none of these functions take a Codec, and Codec itself stays two-valued (it names
// NAL framing, which AV1 does not have). A consumer that handles all three codecs
// therefore owns its own three-value discriminator — which is the point: adding AV1
// as a third Codec would have left every existing switch compiling while quietly
// captioning nothing.
//
// The payload is identical to the SEI path — the same cc_data() from BuildCCData,
// under the same T.35/GA94 header — so only the envelope and the splice differ. See
// docs/research/av1-metadata-obu-608-layout.md.

// MetadataOBU wraps a cc_data() payload as a complete metadata_itu_t_t35 OBU: the
// OBU header, obu_size, metadata_type = 4 (ITU-T T.35), the T.35/GA94 header, the
// cc_data() bytes, and the trailing_bits byte 0x80.
//
// It is the AV1 counterpart of SEIMessage, and returns finished wire bytes rather
// than a message value because an OBU is self-framing — there is no NAL unit to
// combine several messages into. Unlike an SEI NAL unit an OBU carries no
// emulation-prevention, so ccData is written verbatim.
//
// Splice the result into a sample with SpliceOBUBeforeFrame; use FrameMetadataOBU
// for the common one-frame case.
func MetadataOBU(ccData []byte) []byte {
	return av1.CreateCTA608MetadataOBU(ccData)
}

// FrameMetadataOBU is the one-call convenience for a single 608 frame, mirroring
// FrameSEINALU: it builds the cc_data() and wraps it as a metadata OBU. Equivalent
// to MetadataOBU(BuildCCData(field1Pair, field2Pair, ccCount)).
func FrameMetadataOBU(field1Pair, field2Pair []byte, ccCount int) []byte {
	return MetadataOBU(BuildCCData(field1Pair, field2Pair, ccCount))
}

// SpliceOBUBeforeFrame inserts a metadata OBU into an av01 sample immediately before
// its first OBU_FRAME or OBU_FRAME_HEADER, returning the rewritten sample. This is
// the AV1 encode-side splice, the counterpart of SpliceSEIBeforeVCL.
//
// obu is complete wire bytes — what MetadataOBU returns. An mp4 sample holds one
// temporal unit as a bare OBU sequence (no length prefixes, and the mp4 muxer strips
// temporal delimiters), so the splice anchors on the frame OBU rather than on a
// position from the start of the sample: that way the rule picks the same place in an
// mp4 sample and in an IVF temporal unit, which keeps its delimiters. Any sequence
// header, temporal delimiter or earlier metadata OBU therefore stays ahead of the
// caption OBU, which is where AV1's "Ordering of OBUs" puts metadata — after the
// sequence header, before the frame payload whose scope it opens.
//
// Unlike SpliceSEIBeforeVCL there is no no-anchor fallback: every temporal unit must
// output exactly one shown frame, so an OBU_FRAME or OBU_FRAME_HEADER is always
// present, and a sample without one is malformed rather than merely frameless. It is
// reported as an error.
//
// Assignment is one OBU per sample, in sample order — one sample is one temporal
// unit, and a temporal unit shows exactly one frame. Several frame OBUs in one sample
// (hidden reference frames) are not an ambiguity; only the first is the anchor. This
// holds for non-scalable streams, i.e. OperatingPointIdc == 0; with scalability the
// spec allows one shown frame per layer and one-payload-per-sample stops being well
// defined.
//
// The unmodified OBUs are re-serialized with an obu_size field. That is a no-op for
// every OBU that already had one, which in mp4 is normally all of them; a final OBU
// relying on the "extends to end of data" form gains an explicit size, which is
// equally valid and unambiguous.
//
// The typical caller loops over a fragment's samples and grows the sample size
// alongside the data:
//
//	data, err := carriage.SpliceOBUBeforeFrame(s.Data, obu)
//	if err != nil {
//		return err
//	}
//	s.Data, s.Size = data, uint32(len(data))
func SpliceOBUBeforeFrame(sample, obu []byte) ([]byte, error) {
	obus, err := av1.SplitOBUs(sample)
	if err != nil {
		return nil, fmt.Errorf("carriage: splitting av01 sample into OBUs: %w", err)
	}
	insertAt := -1
	for i, o := range obus {
		if o.Header.Type == av1.OBUFrame || o.Header.Type == av1.OBUFrameHeader {
			insertAt = i
			break
		}
	}
	if insertAt < 0 {
		return nil, fmt.Errorf("carriage: av01 sample has no OBU_FRAME or OBU_FRAME_HEADER to splice before")
	}

	out := make([]byte, 0, len(sample)+len(obu))
	for _, o := range obus[:insertAt] {
		out = append(out, o.Encode()...)
	}
	out = append(out, obu...)
	for _, o := range obus[insertAt:] {
		out = append(out, o.Encode()...)
	}
	return out, nil
}

// OBUFieldPairs is the AV1 decode wrapper, the counterpart of FieldPairs: it scans an
// av01 sample's OBUs for CTA-608 metadata OBUs and returns the concatenated field-1
// and field-2 byte-pair streams (parity preserved) to feed the cta608 core Decoder.
// Non-metadata and non-608 OBUs are ignored; both fields are nil when the sample
// carries no captions.
//
// It takes the raw sample rather than a pre-split OBU slice — the asymmetry with
// FieldPairs, which takes NAL units because go608-extract already has them split. An
// AV1 caller has nothing else to do with the OBU list, and taking the sample keeps
// mp4ff's av1 types out of go-608's signature.
func OBUFieldPairs(sample []byte) (field1, field2 []byte, err error) {
	obus, err := av1.SplitOBUs(sample)
	if err != nil {
		return nil, nil, fmt.Errorf("carriage: splitting av01 sample into OBUs: %w", err)
	}
	field1, field2, err = av1.ExtractCTA608(obus)
	if err != nil {
		return nil, nil, fmt.Errorf("carriage: extracting CTA-608 from av01 sample: %w", err)
	}
	return field1, field2, nil
}
