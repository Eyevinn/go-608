package examples_test

import (
	"fmt"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/schedule"
)

// Example_scheduleToCarriage schedules a short pop-on caption onto video frames,
// wraps each frame with carriage, and decodes it straight back — the shared
// encode path both the wall-clock generator and the subtitle-compile path use.
// schedule serializes the tokens and drains at most one byte pair per field per
// frame (padding to cc_count); carriage builds the per-frame SEI NAL unit. In
// production the caller splices the NAL into the elementary stream rather than
// decoding it in place.
func Example_scheduleToCarriage() {
	// A one-line pop-on caption as a token stream.
	tokens := []cta608.Token{
		cta608.SetMode{Mode: cta608.PopOn},
		cta608.PAC{Row: 15, Indent: cta608.NoIndent, Pen: cta608.Pen{Color: cta608.White}},
		cta608.Chars{Text: "HI"},
		cta608.Command{Op: cta608.EOC},
	}

	// Schedule at 30 fps (cc_count 20), starting at wall-clock time 0.
	s := schedule.NewScheduler(30)
	s.Push(schedule.TimedTokens{TimeMS: 0, Tokens: tokens})

	// Pull ten frames (33 ms apart); wrap each with carriage and recover the
	// field-1 byte pairs, concatenating them across frames.
	var field1 []byte
	framesWithData := 0
	for frame := 0; frame < 10; frame++ {
		f := s.Frame(int64(frame) * 33)
		nalu := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, carriage.CodecAVC)
		got1, _, err := carriage.FieldPairs([][]byte{nalu}, carriage.CodecAVC)
		if err != nil {
			panic(err)
		}
		if len(got1) > 0 {
			framesWithData++
		}
		field1 = append(field1, got1...)
	}

	// Parse the recovered pairs back into the token stream (Parse collapses the
	// field-1 control-code doubling). Full Screen reconstruction awaits the
	// cta608 Decoder.
	back, err := cta608.Parse(field1, cta608.ParseOptions{ValidateParity: true})
	if err != nil {
		panic(err)
	}
	fmt.Printf("recovered %d field-1 pairs over %d frames\n", len(field1)/2, framesWithData)
	for _, tok := range back {
		fmt.Println(tok)
	}

	// Output:
	// recovered 7 field-1 pairs over 7 frames
	// SetMode(pop-on)
	// PAC(row=15 white)
	// Chars("HI")
	// Command(EOC)
}
