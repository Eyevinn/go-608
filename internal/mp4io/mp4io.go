// Package mp4io holds the shared fragmented-mp4 read/write and NAL-splice glue
// used by the go-608 commands: go608-clock spliced-in the wall-clock 608 SEI with
// it today, and go608-info / go608-extract reuse the read side (locating the video
// track and splitting a sample into NAL units). It is deliberately internal — it
// wraps mp4ff muxing details that are not part of go-608's public API (SPEC §9,
// package-layout note P5).
//
// The NAL helpers are codec-aware only where the codec matters: splitting and
// length-prefixing a sample are identical for AVC and HEVC (a 4-byte big-endian
// length before each NAL unit), while VCL detection and the splice point depend on
// the codec, so those dispatch on the carriage.Codec the caller already tracks.
package mp4io

import (
	"encoding/binary"
	"fmt"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Track describes the elementary video track the tools operate on: its track ID,
// media timescale, and codec.
type Track struct {
	ID        uint32
	Timescale uint32
	Codec     carriage.Codec
}

// VideoTrack locates the video track of a decoded fragmented mp4 and returns its
// ID, media timescale, and codec, plus the trex needed to expand each fragment's
// samples (default sample flags/duration live in the trex, not the fragment). It
// returns the first "vide"-handler track whose sample entry is AVC or HEVC.
//
// It errors when the file has no moov (an uninitialized segment), is not
// fragmented (no mvex/trex), or has no AVC/HEVC video track.
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
		var codec carriage.Codec
		switch {
		case stsd.AvcX != nil:
			codec = carriage.CodecAVC
		case stsd.HvcX != nil:
			codec = carriage.CodecHEVC
		default:
			return Track{}, nil, fmt.Errorf("mp4io: video track %d is neither AVC nor HEVC", trak.Tkhd.TrackID)
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
	return Track{}, nil, fmt.Errorf("mp4io: no AVC/HEVC video track found")
}

// SampleNALUs splits a length-prefixed sample — a 4-byte big-endian length before
// each NAL unit, the AVC/HEVC in-mp4 format — into its NAL units. It is the exact
// inverse of PrefixNALUs and is codec-independent (the length framing is the same
// for AVC and HEVC).
func SampleNALUs(sample []byte) ([][]byte, error) {
	var nalus [][]byte
	for pos := 0; pos < len(sample); {
		if pos+4 > len(sample) {
			return nil, fmt.Errorf("mp4io: truncated 4-byte NAL length prefix at byte %d of %d", pos, len(sample))
		}
		n := int64(binary.BigEndian.Uint32(sample[pos:]))
		pos += 4
		if n == 0 {
			return nil, fmt.Errorf("mp4io: zero-length NAL unit at byte %d", pos)
		}
		if int64(pos)+n > int64(len(sample)) {
			return nil, fmt.Errorf("mp4io: NAL length %d at byte %d overruns %d-byte sample", n, pos, len(sample))
		}
		nalus = append(nalus, sample[pos:pos+int(n)])
		pos += int(n)
	}
	return nalus, nil
}

// PrefixNALUs concatenates NAL units into a sample, writing a 4-byte big-endian
// length before each — the length-prefixed sample format mp4ff reads and writes.
func PrefixNALUs(nalus ...[]byte) []byte {
	size := 0
	for _, n := range nalus {
		size += 4 + len(n)
	}
	out := make([]byte, 0, size)
	var l [4]byte
	for _, n := range nalus {
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// SpliceSEIBeforeVCL inserts a bare SEI NAL unit (already carrying its codec NAL
// header — e.g. from carriage.FrameSEINALU) immediately before the first VCL NAL
// unit of a length-prefixed sample, returning the rewritten sample. This is the
// encode-side splice of SPEC §6: the caption SEI rides ahead of the picture data
// in the same access unit.
//
// If the sample carries no VCL NAL unit, the SEI is placed first so no data is
// lost. Any SEI already present in the sample is preserved.
func SpliceSEIBeforeVCL(sample, seiNALU []byte, codec carriage.Codec) ([]byte, error) {
	nalus, err := SampleNALUs(sample)
	if err != nil {
		return nil, err
	}
	insertAt := 0
	for i, n := range nalus {
		if isVCL(n, codec) {
			insertAt = i
			break
		}
	}
	out := make([][]byte, 0, len(nalus)+1)
	out = append(out, nalus[:insertAt]...)
	out = append(out, seiNALU)
	out = append(out, nalus[insertAt:]...)
	return PrefixNALUs(out...), nil
}

// isVCL reports whether a NAL unit is a video coding layer (picture) NAL for the
// codec — the anchor the SEI splices before.
func isVCL(nalu []byte, codec carriage.Codec) bool {
	if len(nalu) == 0 {
		return false
	}
	switch codec {
	case carriage.CodecAVC:
		return avc.IsVideoNaluType(avc.GetNaluType(nalu[0]))
	case carriage.CodecHEVC:
		return hevc.IsVideoNaluType(hevc.GetNaluType(nalu[0]))
	default:
		return false
	}
}
