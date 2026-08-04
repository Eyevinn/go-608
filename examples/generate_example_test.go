package examples_test

import (
	"fmt"
	"math"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/generate"
)

// Example_generate drives the wall-clock Generator one call per frame for three
// seconds at 30 fps, wraps each frame's triple as a carriage SEI NAL, decodes it
// back, and prints the clock caption each time it flips — the full
// generate → schedule → carriage → cta608.Decoder loop the consumers use.
func Example_generate() {
	const fps = 30.0
	g := generate.NewGenerator(fps, generate.DefaultConfig())
	var dec cta608.Decoder

	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC).UnixMilli()
	for frame := 0; frame < 3*30; frame++ {
		wall := start + int64(math.Round(float64(frame)*1000.0/fps))
		f := g.NextFrame(wall)
		if len(f.Field1) == 0 {
			continue // idle frame (cc_count padded by carriage)
		}
		nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
		fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
		if err != nil {
			panic(err)
		}
		if err := dec.Feed(fld1); err != nil {
			panic(err)
		}
		if dec.Changed() {
			fmt.Printf("flip @frame %d: %s | %s\n", frame, rowText(dec.Screen(), 14), rowText(dec.Screen(), 15))
		}
	}
	// Output:
	// flip @frame 29: 15:04:06Z | MEDIA 00:00:01
	// flip @frame 59: 15:04:07Z | MEDIA 00:00:02
	// flip @frame 89: 15:04:08Z | MEDIA 00:00:03
}

// Example_generatePaintOn drives the same generator in paint-on mode
// (generate.WithPaintOn) for one second at 30 fps and prints the screen every time
// it changes. Instead of one flip at the second boundary, the second opens with a
// cleared screen and the caption writes itself onto the display two characters per
// frame — the 608 wire rate made visible — and then stands until the next second
// clears it.
func Example_generatePaintOn() {
	const fps = 30.0
	g := generate.NewGenerator(fps, generate.Config{Lines: []generate.LineSpec{
		{Row: 15, Color: "yellow", Kind: "media"},
	}}, generate.WithPaintOn())
	var dec cta608.Decoder

	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC).UnixMilli()
	for frame := 0; frame < 30; frame++ {
		wall := start + int64(math.Round(float64(frame)*1000.0/fps))
		f := g.NextFrame(wall)
		if len(f.Field1) == 0 {
			continue // idle frame: the caption is complete and stays on screen
		}
		nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
		fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
		if err != nil {
			panic(err)
		}
		if err := dec.Feed(fld1); err != nil {
			panic(err)
		}
		if dec.Changed() {
			fmt.Printf("frame %2d: %q\n", frame, rowText(dec.Screen(), 15))
		}
	}
	// Output:
	// frame  4: "ME"
	// frame  5: "MEDI"
	// frame  6: "MEDIA "
	// frame  7: "MEDIA 00"
	// frame  8: "MEDIA 00:0"
	// frame  9: "MEDIA 00:00:"
	// frame 10: "MEDIA 00:00:00"
}

// Example_generateRollUp drives the generator in roll-up (generate.WithRollUp) for
// two seconds at 30 fps and prints the whole window whenever the bottom row settles.
// Roll-up types its line out like paint-on but never clears: each second scrolls the
// window up, so the previous second stays visible above the new one.
func Example_generateRollUp() {
	const fps = 30.0
	g := generate.NewGenerator(fps, generate.Config{Lines: []generate.LineSpec{
		{Row: 15, Color: "white", Kind: "media"},
	}}, generate.WithRollUp(2))
	var dec cta608.Decoder

	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC).UnixMilli()
	for frame := 0; frame < 60; frame++ {
		wall := start + int64(math.Round(float64(frame)*1000.0/fps))
		f := g.NextFrame(wall)
		if len(f.Field1) > 0 {
			nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
			fld1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
			if err != nil {
				panic(err)
			}
			if err := dec.Feed(fld1); err != nil {
				panic(err)
			}
		}
		// Look at the settled window on the last frame of each second.
		if frame == 29 || frame == 59 {
			fmt.Printf("frame %2d: row14=%q row15=%q\n", frame,
				rowText(dec.Screen(), 14), rowText(dec.Screen(), 15))
		}
	}
	// Output:
	// frame 29: row14="" row15="MEDIA 00:00:00"
	// frame 59: row14="MEDIA 00:00:00" row15="MEDIA 00:00:01"
}

func rowText(s cta608.Screen, idx int) string {
	for _, r := range s.Rows {
		if r.Index != idx {
			continue
		}
		txt := ""
		for _, run := range r.Runs {
			txt += run.Text
		}
		return txt
	}
	return ""
}
