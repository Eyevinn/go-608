// Package mp4io holds the shared fragmented-mp4 muxing glue used by the go-608
// commands: go608-clock and go608-inject splice per-sample 608 caption data into a
// file with SpliceFragmented, and go608-info / go608-extract reuse the read side
// (locating the video track, flattening fragments into samples, and pulling the
// field pairs back out). It is deliberately internal — it wraps mp4ff muxing details
// that are not part of go-608's public API (SPEC §9, package-layout note P5).
//
// # Codec dispatch
//
// mp4io is the one place that knows all three codecs. Its VideoCodec is three-valued
// and internal; the public carriage.Codec stays two-valued because it names NAL
// framing, which AV1 does not have. So the caller supplies codec-free cc_data() and
// mp4io picks the envelope and the splice: an SEI NAL unit before the first VCL NAL
// for AVC/HEVC, a metadata OBU before the first frame OBU for AV1. FieldPairs is the
// same dispatch on the way back in.
//
// # Ordering
//
// Caption payloads are assigned in *presentation* order — the k-th payload belongs to
// the k-th displayed frame — and media time is measured from the track origin, the
// smallest presentation time in the file. Both sides obey this: SpliceFragmented
// still writes samples in decode order, but indexes them by presentation rank, and
// Samples returns its slice already in presentation order together with the origin.
// They have to move together, or a round-trip through both agrees with itself while
// disagreeing with every other decoder.
//
// The unit level belongs to carriage, not here: carriage.SampleNALUs, PrefixNALUs,
// SpliceSEIBeforeVCL, IsVCL, MetadataOBU, SpliceOBUBeforeFrame and OBUFieldPairs are
// public, because every consumer that injects captions into its own mp4 writer needs
// them and mp4io is unreachable from outside go-608.
package mp4io

import (
	"fmt"
	"io"
	"sort"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/mp4"
)

// VideoCodec names the codec of the video track being processed. Unlike the public
// carriage.Codec — which selects a NAL-unit header and so covers only AVC and HEVC —
// it has a value for AV1, whose captions ride in an OBU and not a NAL unit at all.
//
// Keeping the two types apart is deliberate (#51): a consumer that switches on
// carriage.Codec cannot silently acquire a wrong AV1 branch, because carriage.Codec
// has no AV1 value to fall through to.
type VideoCodec int

const (
	CodecAVC  VideoCodec = iota // H.264 / AVC: SEI NAL unit
	CodecHEVC                   // H.265 / HEVC: prefix-SEI NAL unit
	CodecAV1                    // AV1: metadata_itu_t_t35 OBU
)

func (c VideoCodec) String() string {
	switch c {
	case CodecAVC:
		return "AVC"
	case CodecHEVC:
		return "HEVC"
	case CodecAV1:
		return "AV1"
	default:
		return fmt.Sprintf("VideoCodec(%d)", int(c))
	}
}

// NALCodec returns the carriage.Codec for a NAL-framed track. ok is false for AV1,
// which has no NAL units — a caller reaching for the SEI path has to handle that
// rather than receive a plausible default.
func (c VideoCodec) NALCodec() (codec carriage.Codec, ok bool) {
	switch c {
	case CodecAVC:
		return carriage.CodecAVC, true
	case CodecHEVC:
		return carriage.CodecHEVC, true
	default:
		return 0, false
	}
}

// Track describes the elementary video track the tools operate on: its track ID,
// media timescale, and codec.
type Track struct {
	ID        uint32
	Timescale uint32
	Codec     VideoCodec
}

// VideoTrack locates the video track of a decoded fragmented mp4 and returns its
// ID, media timescale, and codec, plus the trex needed to expand each fragment's
// samples (default sample flags/duration live in the trex, not the fragment). It
// returns the first "vide"-handler track whose sample entry is AVC, HEVC or AV1.
//
// It errors when the file has no moov (an uninitialized segment), is not
// fragmented (no mvex/trex), or has no AVC/HEVC/AV1 video track.
func VideoTrack(f *mp4.File) (Track, *mp4.TrexBox, error) {
	moov := f.Moov
	if moov == nil && f.Init != nil {
		moov = f.Init.Moov
	}
	if moov == nil {
		return Track{}, nil, fmt.Errorf("mp4io: no moov box (not an initialized mp4)")
	}
	if moov.Mvex == nil {
		return Track{}, nil, fmt.Errorf("mp4io: no mvex box (not a fragmented mp4)")
	}
	for _, trak := range moov.Traks {
		if trak.Tkhd == nil || trak.Mdia == nil || trak.Mdia.Hdlr == nil || trak.Mdia.Hdlr.HandlerType != "vide" {
			continue
		}
		if trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stsd == nil {
			continue
		}
		stsd := trak.Mdia.Minf.Stbl.Stsd
		var codec VideoCodec
		switch {
		case stsd.AvcX != nil:
			codec = CodecAVC
		case stsd.HvcX != nil:
			codec = CodecHEVC
		case stsd.Av01 != nil:
			if err := checkAV1NonScalable(stsd.Av01); err != nil {
				return Track{}, nil, err
			}
			codec = CodecAV1
		default:
			return Track{}, nil, fmt.Errorf("mp4io: video track %d is not AVC, HEVC or AV1", trak.Tkhd.TrackID)
		}
		id := trak.Tkhd.TrackID
		trex, ok := moov.Mvex.GetTrex(id)
		if !ok {
			return Track{}, nil, fmt.Errorf("mp4io: no trex for video track %d", id)
		}
		var timescale uint32
		if trak.Mdia.Mdhd != nil {
			timescale = trak.Mdia.Mdhd.Timescale
		}
		return Track{ID: id, Timescale: timescale, Codec: codec}, trex, nil
	}
	return Track{}, nil, fmt.Errorf("mp4io: no AVC/HEVC/AV1 video track found")
}

// checkAV1NonScalable rejects a scalable av01 track, the precondition of the whole
// AV1 caption path: one caption OBU per sample is well defined only because a
// temporal unit shows exactly one frame, and that holds only for OperatingPointIdc
// == 0. With scalability the AV1 spec allows one shown frame per layer per temporal
// unit, and "the caption for this sample" stops naming a single picture.
//
// A track whose av1C carries no parseable sequence header is accepted: the header
// may appear only inline in the first sample, and refusing to caption a file we
// merely failed to inspect would be worse than the risk it covers.
func checkAV1NonScalable(av01 *mp4.VisualSampleEntryBox) error {
	if av01.Av1C == nil {
		return nil
	}
	sh, err := av01.Av1C.SequenceHeader()
	if err != nil || sh == nil {
		return nil
	}
	for _, idc := range sh.OperatingPointIdc {
		if idc != 0 {
			return fmt.Errorf("mp4io: scalable av01 track (operating_point_idc %#x); "+
				"CTA-608 carriage supports non-scalable AV1 only", idc)
		}
	}
	return nil
}

// Samples flattens every fragment's samples across all media segments into one
// slice in **presentation order** — the read unit the extract/info tools iterate —
// and returns the track origin alongside it: the smallest presentation time in the
// file, which media time is measured from.
//
// Presentation order, not decode order, is what a caption consumer wants: the k-th
// element is the k-th displayed frame, so it carries the k-th caption payload.
// (Whether that differs from decode order is codec-dependent — AVC and HEVC reorder
// in the container via composition offsets, AV1 reorders inside the bitstream and so
// always has pts == dts. The rule is the same either way.)
//
// Each FullSample carries its Data (length-prefixed NAL units for AVC/HEVC, a bare
// OBU sequence for AV1) and DecodeTime; s.PresentationTime() - origin is the sample's
// media time in the track's timescale.
func Samples(f *mp4.File, trex *mp4.TrexBox) (samples []mp4.FullSample, origin int64, err error) {
	for _, seg := range f.Segments {
		for _, frag := range seg.Fragments {
			s, err := frag.GetFullSamples(trex)
			if err != nil {
				return nil, 0, fmt.Errorf("mp4io: expanding fragment samples: %w", err)
			}
			samples = append(samples, s...)
		}
	}
	order, origin := presentationOrder(samples)
	sorted := make([]mp4.FullSample, len(samples))
	for k, d := range order {
		sorted[k] = samples[d]
	}
	return sorted, origin, nil
}

// presentationOrder returns order, where order[k] is the decode-order index of the
// k-th sample in presentation order, and the track origin (the smallest presentation
// time, 0 for an empty track). The sort is stable, so samples sharing a presentation
// time keep their decode order.
func presentationOrder(samples []mp4.FullSample) (order []int, origin int64) {
	order = make([]int, len(samples))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return samples[order[a]].PresentationTime() < samples[order[b]].PresentationTime()
	})
	if len(order) > 0 {
		origin = samples[order[0]].PresentationTime()
	}
	return order, origin
}

// FieldPairs returns the CTA-608 field-1 and field-2 byte-pair streams carried by one
// sample, dispatching on the track codec: the SEI path for AVC/HEVC, the metadata-OBU
// path for AV1. It is the read-side counterpart of the envelope switch in
// SpliceFragmented, and the single call the extract/info tools make per sample.
func FieldPairs(sample []byte, codec VideoCodec) (field1, field2 []byte, err error) {
	if codec == CodecAV1 {
		return carriage.OBUFieldPairs(sample)
	}
	nal, ok := codec.NALCodec()
	if !ok {
		return nil, nil, fmt.Errorf("mp4io: unknown codec %s", codec)
	}
	nalus, err := carriage.SampleNALUs(sample)
	if err != nil {
		return nil, nil, fmt.Errorf("mp4io: splitting sample NAL units: %w", err)
	}
	return carriage.FieldPairs(nalus, nal)
}

// SampleInfo identifies one sample being rewritten by SpliceFragmented. The CCDataFunc
// uses it to pick the sample's 608 payload, by frame index or by media time.
type SampleInfo struct {
	// Index is the 0-based index of this sample in presentation order across the
	// whole file — the k-th displayed frame — not its decode-order position.
	Index int
	// DecodeTime is the sample's absolute decode time in the track's media
	// timescale, as it appears in the container.
	DecodeTime uint64
	// MediaTime is the sample's presentation time relative to the track origin, in
	// the track's media timescale. It is 0 for the first displayed frame, so a
	// subtitle file's t=0 lines up with it whatever absolute timestamps the
	// container happens to start at. Edit lists are not consulted.
	MediaTime int64
}

// CCDataFunc returns the cc_data() payload to carry in the given sample — what
// carriage.BuildCCData produces — or nil to leave that sample unchanged. It is
// codec-free: SpliceFragmented wraps the payload in the envelope the track's codec
// needs and splices it into the right place.
//
// SpliceFragmented calls it once per sample in presentation order, so Index and
// MediaTime both advance monotonically. A stateful caption source — a
// schedule.Scheduler draining its queue, or a clock generator stepping frame by
// frame — can therefore be driven straight from it.
type CCDataFunc func(SampleInfo) ([]byte, error)

// SpliceFragmented rewrites a single-video-track fragmented mp4, carrying the
// cc_data() that fn returns in every sample, and encodes the result (the original
// init segment followed by the rebuilt media segments) to out. It is the shared write
// engine behind go608-clock and go608-inject: per-fragment sequence numbers, styp, and
// per-sample timing/flags/CTO are preserved, only the sample data grows by the
// spliced caption unit. It errors unless f is an initialized fragmented mp4 with
// exactly one track.
//
// fn is called in presentation order, so a stateful caption source sees a monotonic
// sequence; the samples are then written back in decode order, which the container
// requires.
func SpliceFragmented(f *mp4.File, track Track, trex *mp4.TrexBox, out io.Writer, fn CCDataFunc) error {
	if f.Init == nil || f.Init.Moov == nil {
		return fmt.Errorf("mp4io: no moov in init segment (need an initialized fragmented mp4)")
	}
	if n := len(f.Init.Moov.Traks); n != 1 {
		return fmt.Errorf("mp4io: %d tracks; only single-video-track fragmented mp4 is supported", n)
	}

	// Pass 1: expand every fragment, keeping the segment/fragment structure, so the
	// presentation ranks can be computed across the whole file before anything is
	// written. A fragment's samples cannot be ranked on their own: reordering spans
	// fragment boundaries.
	type fragment struct {
		seq     uint32
		samples []mp4.FullSample
	}
	segments := make([][]fragment, 0, len(f.Segments))
	var all []mp4.FullSample
	for _, seg := range f.Segments {
		frags := make([]fragment, 0, len(seg.Fragments))
		for _, frag := range seg.Fragments {
			samples, err := frag.GetFullSamples(trex)
			if err != nil {
				return fmt.Errorf("mp4io: expanding fragment samples: %w", err)
			}
			seq := uint32(1)
			if frag.Moof != nil && frag.Moof.Mfhd != nil {
				seq = frag.Moof.Mfhd.SequenceNumber
			}
			frags = append(frags, fragment{seq: seq, samples: samples})
			all = append(all, samples...)
		}
		segments = append(segments, frags)
	}

	// Pass 2: ask fn for each sample's payload, walking presentation order so a
	// stateful caption source sees a monotonic sequence. The payloads are kept
	// against the decode-order position they will be written at.
	order, origin := presentationOrder(all)
	ccData := make([][]byte, len(all))
	for k, d := range order {
		cc, err := fn(SampleInfo{
			Index:      k,
			DecodeTime: all[d].DecodeTime,
			MediaTime:  all[d].PresentationTime() - origin,
		})
		if err != nil {
			return err
		}
		ccData[d] = cc
	}

	// Pass 3: rebuild, in decode order.
	decodeIdx := 0
	newSegments := make([]*mp4.MediaSegment, 0, len(f.Segments))
	for si, frags := range segments {
		newSeg := mp4.NewMediaSegmentWithoutStyp()
		if styp := f.Segments[si].Styp; styp != nil {
			newSeg = mp4.NewMediaSegmentWithStyp(styp)
		}
		for _, frag := range frags {
			newFrag, err := mp4.CreateFragment(frag.seq, track.ID)
			if err != nil {
				return fmt.Errorf("mp4io: creating fragment: %w", err)
			}
			for _, s := range frag.samples {
				if cc := ccData[decodeIdx]; len(cc) > 0 {
					data, err := spliceCCData(s.Data, cc, track.Codec)
					if err != nil {
						return fmt.Errorf("mp4io: carrying captions in decode-order sample %d: %w", decodeIdx, err)
					}
					s.Data = data
					s.Size = uint32(len(data))
				}
				newFrag.AddFullSample(s)
				decodeIdx++
			}
			newSeg.AddFragment(newFrag)
		}
		newSegments = append(newSegments, newSeg)
	}

	if err := f.Init.Encode(out); err != nil {
		return fmt.Errorf("mp4io: encoding init segment: %w", err)
	}
	for _, seg := range newSegments {
		if err := seg.Encode(out); err != nil {
			return fmt.Errorf("mp4io: encoding media segment: %w", err)
		}
	}
	return nil
}

// spliceCCData wraps a cc_data() payload in the envelope the codec needs and splices
// it into the sample: an SEI NAL unit before the first VCL NAL for AVC/HEVC, a
// metadata OBU before the first frame OBU for AV1. This is the only place in go-608
// that switches on all three codecs.
func spliceCCData(sample, ccData []byte, codec VideoCodec) ([]byte, error) {
	switch codec {
	case CodecAVC, CodecHEVC:
		nal, _ := codec.NALCodec()
		nalu := carriage.NALU(nal, carriage.SEIMessage(ccData))
		return carriage.SpliceSEIBeforeVCL(sample, nalu, nal)
	case CodecAV1:
		return carriage.SpliceOBUBeforeFrame(sample, carriage.MetadataOBU(ccData))
	default:
		return nil, fmt.Errorf("unknown codec %s", codec)
	}
}
