package carriage

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/sei"
)

// Codec selects the video codec whose NAL-unit header wraps the SEI message.
// It is always supplied explicitly; carriage never sniffs the codec.
type Codec int

const (
	CodecAVC  Codec = iota // H.264 / AVC: 1-byte NAL header, SEI type 6.
	CodecHEVC              // H.265 / HEVC: 2-byte NAL header, prefix-SEI type 39.
)

func (c Codec) String() string {
	switch c {
	case CodecAVC:
		return "AVC"
	case CodecHEVC:
		return "HEVC"
	default:
		return fmt.Sprintf("Codec(%d)", int(c))
	}
}

// cc_data() construct byte: five marker bits (11111), then cc_valid (1 bit) and
// cc_type (2 bits). See CTA-708-E §4.3.
const ccConstructMarker = 0xf8 // 11111 000

// cc_type values.
const (
	ccTypeField1 = 0x0 // 608 field 1
	ccTypeField2 = 0x1 // 608 field 2
	ccTypeDTVCC  = 0x2 // 708/DTVCC continuation — used here only for padding
)

const ccValidBit = 0x4

// BuildCCData assembles one frame's cc_data() from per-field byte pairs, per the
// CTA-708-E §4.3 syntax. Parity is assumed already set by the cta608 serializer;
// BuildCCData is pure and timing-free — the caller (the schedule package) supplies
// ccCount.
//
// A field pair is either 0 bytes or 2 bytes, and the two "nothing here" encodings
// are kept distinct (SPEC §5.3):
//   - a 0-byte pair becomes a cc_valid=0, cc_type=00/01 construct ("no 608 waveform
//     this field this frame");
//   - a 2-byte pair — including the 608 null pair 0x80 0x80 — becomes a cc_valid=1
//     construct.
//
// Both 608 constructs (field 1 then field 2) are emitted first; the remaining
// constructs up to ccCount are DTVCC padding (cc_valid=0, cc_type=10, 0x0000). The
// first padding construct also marks the end of the 608 data (§5.1).
//
// BuildCCData panics on a contract violation: a field pair that is neither 0 nor 2
// bytes, or a ccCount that cannot hold the two 608 constructs (<2) or overflow the
// 5-bit cc_count field (>31).
func BuildCCData(field1Pair, field2Pair []byte, ccCount int) []byte {
	checkPair("field1Pair", field1Pair)
	checkPair("field2Pair", field2Pair)
	if ccCount < 2 {
		panic(fmt.Sprintf("carriage: ccCount %d too small for two 608 constructs", ccCount))
	}
	if ccCount > 31 {
		panic(fmt.Sprintf("carriage: ccCount %d overflows the 5-bit cc_count field", ccCount))
	}

	out := make([]byte, 0, 2+ccCount*3+1)
	// process_em_data_flag=1, process_cc_data_flag=1, additional_data_flag=0, cc_count.
	out = append(out, 0xc0|byte(ccCount))
	out = append(out, 0xff) // em_data
	out = appendField(out, field1Pair, ccTypeField1)
	out = appendField(out, field2Pair, ccTypeField2)
	for i := 2; i < ccCount; i++ {
		out = append(out, ccConstructMarker|ccTypeDTVCC, 0x00, 0x00)
	}
	out = append(out, 0xff) // trailing marker_bits
	return out
}

func appendField(out, pair []byte, ccType byte) []byte {
	if len(pair) == 2 {
		return append(out, ccConstructMarker|ccValidBit|ccType, pair[0], pair[1])
	}
	// Empty field: cc_valid=0 with the field's own cc_type — distinct from padding.
	return append(out, ccConstructMarker|ccType, 0x00, 0x00)
}

func checkPair(name string, pair []byte) {
	if len(pair) != 0 && len(pair) != 2 {
		panic(fmt.Sprintf("carriage: %s must be 0 or 2 bytes, got %d", name, len(pair)))
	}
}

// SEIMessage wraps a cc_data() payload as a user_data_registered_itu_t_t35 SEI
// message (T.35/GA94 header + cc_data), returning it as an mp4ff sei.SEIMessage.
//
// The T.35/GA94 header — country_code 0xB5, provider_code 0x0031 (ATSC),
// user_identifier "GA94", user_data_type_code 0x03 (SPEC §5.2) — is mp4ff's
// sei.CTA608ITUData, the same header its AV1 metadata-OBU path uses: the payload is
// identical for AVC/HEVC and AV1, and only the envelope differs.
//
// The message is codec-identical for AVC and HEVC (SPEC §5.2), so SEIMessage takes
// no codec. Combine it with other sei.SEIMessage values in a single SEI NAL unit
// via NALU (or serialize it yourself with sei.WriteSEIMessages); use FrameSEINALU
// for the common one-message-per-frame case.
func SEIMessage(ccData []byte) sei.SEIMessage {
	return sei.CreateCTA608SEIMessage(ccData)
}

// NALU serializes one or more SEI messages into a bare SEI NAL unit for the codec:
// the SEI payload (with emulation-prevention) prefixed by the codec NAL-unit header
// (AVC 0x06 / HEVC prefix-SEI 39). It delegates to mp4ff's avc.CreateSEINalu /
// hevc.CreateSEINalu, which own that header. It returns a bare NAL unit — no 4-byte
// length prefix; the consumer adds the length and splices the NAL before the first
// VCL NALU.
//
// Passing several messages places them in one SEI NAL unit — e.g. a 608 message
// alongside a pic_timing or other user_data SEI.
func NALU(codec Codec, msgs ...sei.SEIMessage) []byte {
	var nalu []byte
	var err error
	switch codec {
	case CodecAVC:
		nalu, err = avc.CreateSEINalu(msgs)
	case CodecHEVC:
		nalu, err = hevc.CreateSEINalu(msgs)
	default:
		panic(fmt.Sprintf("carriage: unknown codec %d", int(codec)))
	}
	if err != nil {
		// Unreachable: CreateSEINalu only fails when sei.WriteSEIMessages fails, and
		// that writes into a bytes.Buffer it allocates itself.
		panic(fmt.Sprintf("carriage: building %s SEI NAL unit: %v", codec, err))
	}
	return nalu
}

// FrameSEINALU is the one-call convenience for a single 608 frame: it builds the
// cc_data(), wraps it as an SEI message, and returns the bare NAL unit. Equivalent
// to NALU(codec, SEIMessage(BuildCCData(field1Pair, field2Pair, ccCount))).
func FrameSEINALU(field1Pair, field2Pair []byte, ccCount int, codec Codec) []byte {
	return NALU(codec, SEIMessage(BuildCCData(field1Pair, field2Pair, ccCount)))
}

// FieldPairs is the decode wrapper over mp4ff's sei.ParseCTA608. It scans a sample's
// NAL units for the CTA-608 SEI message and returns the concatenated field-1 and
// field-2 byte-pair streams (parity preserved) to feed the cta608 core Decoder.
// Non-SEI NAL units are ignored.
func FieldPairs(sampleNALUs [][]byte, codec Codec) (field1, field2 []byte, err error) {
	for _, nalu := range sampleNALUs {
		if len(nalu) == 0 {
			continue
		}
		msgs, ok, perr := parseSEI(nalu, codec)
		if perr != nil {
			return nil, nil, perr
		}
		if !ok {
			continue
		}
		for _, m := range msgs {
			cta, ok := m.(*sei.CTA608sei)
			if !ok {
				continue
			}
			field1 = append(field1, cta.Field1...)
			field2 = append(field2, cta.Field2...)
		}
	}
	return field1, field2, nil
}

// parseSEI parses one NAL unit if it is an SEI NAL for the given codec, returning
// only its user_data_registered_itu_t_t35 (type 4) messages — the ones that can
// carry CTA-608. ok is false for a non-SEI or header-only NAL (which the caller
// skips).
//
// It decodes the SEI framing directly (sei.ExtractSEIData) rather than via
// avc/hevc.ParseSEINalu so that unrelated SEI message types in the same sample
// (e.g. pic_timing, which mp4ff cannot fully decode without the SPS) are ignored
// instead of aborting 608 extraction. A missing rbsp_trailing_bits byte is
// tolerated — mp4ff still returns the messages.
func parseSEI(nalu []byte, codec Codec) (msgs []sei.SEIMessage, ok bool, err error) {
	var hdrLen int
	switch codec {
	case CodecAVC:
		if avc.GetNaluType(nalu[0]) != avc.NALU_SEI {
			return nil, false, nil
		}
		hdrLen = 1
	case CodecHEVC:
		switch hevc.GetNaluType(nalu[0]) {
		case hevc.NALU_SEI_PREFIX, hevc.NALU_SEI_SUFFIX:
		default:
			return nil, false, nil
		}
		hdrLen = 2
	default:
		return nil, false, fmt.Errorf("carriage: unknown codec %d", int(codec))
	}
	if len(nalu) <= hdrLen {
		return nil, false, nil // header only / truncated — nothing to parse
	}

	seiDatas, err := sei.ExtractSEIData(bytes.NewReader(nalu[hdrLen:]))
	if err != nil && !errors.Is(err, sei.ErrRbspTrailingBitsMissing) {
		return nil, false, fmt.Errorf("carriage: extracting SEI data: %w", err)
	}
	for i := range seiDatas {
		if seiDatas[i].Type() != sei.SEIUserDataRegisteredITUtT35Type {
			continue
		}
		// A CTA-608 message needs the 8-byte T.35 header plus at least one cc_data
		// byte, so a shorter type-4 payload can't be CTA-608. mp4ff no longer panics
		// on one (it returns an error as of v0.55.0), but skipping is still what we
		// want: a truncated or foreign type-4 SEI should not abort 608 extraction for
		// the whole sample, which is what surfacing the error would do.
		if len(seiDatas[i].Payload()) <= sei.ITUDataSize {
			continue
		}
		m, derr := sei.DecodeUserDataRegisteredSEI(&seiDatas[i])
		if derr != nil {
			return nil, false, fmt.Errorf("carriage: decoding T.35 SEI: %w", derr)
		}
		msgs = append(msgs, m)
	}
	return msgs, true, nil
}
