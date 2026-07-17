// PROTOTYPE — throwaway. go-608 wall-clock caption generator behavior (wayfinder #7).
//
// QUESTION: how should the wall-clock 608 caption generator BEHAVE?
//   content : two centered lines — row14 = UTC RFC3339 (white), row15 = media time (YELLOW)
//   mode    : pop-on, refreshed once/sec, built in non-displayed mem DURING the second and flipped
//             with a single EOC on the LAST frame of the second (frame-accurate, zero lag)
//   cadence : <=1 field-1 byte-pair/frame (2B/field/frame); cc_count pads the rest per fps
//   fps     : 25/30/29.97/50/60 (cc_count = round(600/fps))
//   API     : NewGenerator(fps,cfg) + NextFrame(frameWallMS) -> cc_data  (caller passes wall time)
//
// Surfaces two real 608 subtleties on request:
//   - CENTERING: PAC indent (mult of 4) + Tab Offset (0..3) to hit the exact start column.
//   - COLOR+CENTER: an indented PAC is always white, so a centered colored line needs
//     PAC(indent,white) + TabOffset + MidRow(color) + chars — the mid-row cell shifts text by 1.
//   - BUDGET: two full lines don't always fit a 1-sec refresh at 1 pair/frame (watch 25 fps).
//
// Run:  cd proto-wallclock && go run .
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// ------------------------- PORTABLE LOGIC (would lift into real code) -------------------------

type Pair struct {
	B0, B1 byte
	Kind   string // RCL PAC TO MIDROW CHARS EOC NULL
	Row    int    // caption row this pair contributes to (0 for RCL/EOC/NULL)
	Note   string
}

type LineSpec struct {
	Row   int
	Color string // "" = white
	Kind  string // "utc" | "media"
}

type Config struct{ Lines []LineSpec }

func DefaultConfig() Config {
	return Config{Lines: []LineSpec{
		{Row: 14, Color: "white", Kind: "utc"},
		{Row: 15, Color: "yellow", Kind: "media"},
	}}
}

type FrameOut struct {
	Frame   int
	WallMS  int64
	CcCount int
	Field1  Pair
	Flip    bool
	Overrun bool // build did not finish before the boundary — line refresh didn't fit this fps
}

type Generator struct {
	fps        float64
	ccCount    int
	frameDurMS float64
	cfg        Config

	frame       int
	builtForSec int64
	queue       []Pair
	eocArmed    bool

	lines     []lineMeta      // per-line meta for the second being built
	build     map[int]string  // row -> chars accumulated (non-displayed)
	displayed map[int]lineView // row -> what's on screen
	overran   bool
}

type lineMeta struct {
	row, startCol int
	color         string
	full          string
}
type lineView struct {
	startCol int
	color    string
	text     string
}

func NewGenerator(fps float64, cfg Config) *Generator {
	if len(cfg.Lines) == 0 {
		cfg = DefaultConfig()
	}
	return &Generator{
		fps: fps, ccCount: ccCountForFPS(fps), frameDurMS: 1000.0 / fps, cfg: cfg,
		builtForSec: -1, build: map[int]string{}, displayed: map[int]lineView{},
	}
}

// CEA-708 §4.3.6: cc_count = round(600/fps). 30->20 25->24 60->10 50->12 24->25.
func ccCountForFPS(fps float64) int { return int(math.Round(600.0 / fps)) }

func centerCol(width int) int {
	c := (32 - width) / 2
	if c < 0 {
		c = 0
	}
	return c
}

func content(kind string, wallSec int64, mediaMS int64) string {
	switch kind {
	case "utc":
		return time.Unix(wallSec, 0).UTC().Format("2006-01-02T15:04:05Z")
	case "media":
		s := mediaMS / 1000
		return fmt.Sprintf("MEDIA %02d:%02d:%02d", s/3600, (s/60)%60, s%60)
	}
	return ""
}

var colorIdx = map[string]int{"white": 0, "green": 1, "blue": 2, "cyan": 3, "red": 4, "yellow": 5, "magenta": 6}

func parity(b byte) byte {
	b &= 0x7f
	ones := 0
	for i := 0; i < 7; i++ {
		if b&(1<<i) != 0 {
			ones++
		}
	}
	if ones%2 == 0 {
		b |= 0x80
	}
	return b
}

func pacIndent(row, indent int) Pair { // row 15 high range 0x70; row 14 low range 0x50
	b1 := byte(0x70) + byte((indent/4)*2)
	if row == 14 {
		b1 = byte(0x50) + byte((indent/4)*2)
	}
	return Pair{parity(0x14), parity(b1), "PAC", row, fmt.Sprintf("PAC row %d indent %d (white)", row, indent)}
}
func tabOffset(n int) Pair {
	return Pair{parity(0x17), parity(byte(0x20 + n)), "TO", 0, fmt.Sprintf("Tab Offset %d", n)}
}
func midRow(color string) Pair { // 0x11, 0x20 + 2*colorIndex
	return Pair{parity(0x11), parity(byte(0x20 + 2*colorIdx[color])), "MIDROW", 0, "Mid-Row " + color + " (+space)"}
}

var rclPair = Pair{parity(0x14), parity(0x20), "RCL", 0, "Resume Caption Loading"}
var eocPair = Pair{parity(0x14), parity(0x2F), "EOC", 0, "End Of Caption (flip)"}
var nullPair = Pair{0x80, 0x80, "NULL", 0, "608 null pad"}

// encodeScreen lowers the whole 2-line screen to pairs (pop-on build, WITHOUT the trailing EOC).
func (g *Generator) encodeScreen(wallSec, mediaMS int64) ([]Pair, []lineMeta) {
	pairs := []Pair{rclPair}
	var metas []lineMeta
	for _, ls := range g.cfg.Lines {
		text := content(ls.Kind, wallSec, mediaMS)
		colored := ls.Color != "" && ls.Color != "white"
		textStart := centerCol(len(text))
		cursor := textStart
		if colored {
			cursor-- // mid-row cell sits one col before the text
			if cursor < 0 {
				cursor = 0
				textStart = 1
			}
		}
		indent := (cursor / 4) * 4
		pairs = append(pairs, pacIndent(ls.Row, indent))
		if tab := cursor % 4; tab > 0 {
			pairs = append(pairs, tabOffset(tab))
		}
		if colored {
			pairs = append(pairs, midRow(ls.Color))
		}
		rs := []rune(text)
		for i := 0; i < len(rs); i += 2 {
			c0, n := parity(byte(rs[i])), string(rs[i])
			var c1 byte = 0x80
			if i+1 < len(rs) {
				c1, n = parity(byte(rs[i+1])), n+string(rs[i+1])
			}
			pairs = append(pairs, Pair{c0, c1, "CHARS", ls.Row, fmt.Sprintf("%q", n)})
		}
		metas = append(metas, lineMeta{row: ls.Row, startCol: textStart, color: ls.Color, full: text})
	}
	return pairs, metas
}

// NextFrame advances one video frame at the given wall-clock ms and returns the cc_data emitted.
func (g *Generator) NextFrame(frameWallMS int64) FrameOut {
	sec := frameWallMS / 1000
	nextWall := frameWallMS + int64(math.Round(g.frameDurMS))
	lastFrameOfSecond := nextWall/1000 > sec

	if sec != g.builtForSec {
		g.builtForSec = sec
		mediaMS := int64(math.Round(float64(g.frame) * g.frameDurMS))
		g.queue, g.lines = g.encodeScreen(sec+1, mediaMS+1000) // build the NEXT second's screen
		g.build = map[int]string{}
		g.eocArmed = false
	}

	var emitted Pair
	flip, overrun := false, false
	switch {
	case len(g.queue) > 0:
		emitted = g.queue[0]
		g.queue = g.queue[1:]
		if emitted.Kind == "CHARS" {
			g.build[emitted.Row] += strings.Trim(emitted.Note, "\"·")
		}
		if len(g.queue) == 0 {
			g.eocArmed = true
		}
		if lastFrameOfSecond && len(g.queue) > 0 { // ran out of frames before finishing the build
			overrun, g.overran = true, true
		}
	case g.eocArmed && lastFrameOfSecond:
		emitted = eocPair
		g.displayed = map[int]lineView{}
		for _, m := range g.lines {
			g.displayed[m.row] = lineView{startCol: m.startCol, color: m.color, text: g.build[m.row]}
		}
		g.eocArmed, flip = false, true
	default:
		emitted = nullPair
	}

	out := FrameOut{Frame: g.frame, WallMS: frameWallMS, CcCount: g.ccCount, Field1: emitted, Flip: flip, Overrun: overrun}
	g.frame++
	return out
}

// ------------------------- THROWAWAY TUI SHELL -------------------------

const (
	bold, dim, reset = "\x1b[1m", "\x1b[2m", "\x1b[0m"
)

var ansiColor = map[string]string{"yellow": "\x1b[33m", "green": "\x1b[32m", "cyan": "\x1b[36m", "red": "\x1b[31m", "blue": "\x1b[34m", "magenta": "\x1b[35m", "white": ""}

func main() {
	fpsChoices := []float64{25, 30, 29.97, 50, 60}
	fpsIdx := 1
	startWall := time.Date(2026, 7, 17, 14, 23, 44, 0, time.UTC).UnixMilli()
	g := NewGenerator(fpsChoices[fpsIdx], DefaultConfig())
	var recent []FrameOut

	wallAt := func(frame int) int64 { return startWall + int64(math.Round(float64(frame)*g.frameDurMS)) }
	step := func() { recent = append(recent, g.NextFrame(wallAt(g.frame))) }
	stepSecond := func() {
		t := wallAt(g.frame)/1000 + 1
		for wallAt(g.frame)/1000 < t {
			step()
		}
	}
	setFPS := func(i int) { fpsIdx = i; g = NewGenerator(fpsChoices[fpsIdx], DefaultConfig()); recent = nil }

	renderRow := func(v lineView) string {
		line := strings.Repeat(" ", v.startCol) + v.text
		if len(line) > 32 {
			line = line[:32]
		}
		body := fmt.Sprintf("%-32s", line)
		if c := ansiColor[v.color]; c != "" {
			return c + body + reset
		}
		return body
	}
	render := func() {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Printf("%s=== go-608 wall-clock generator PROTOTYPE (throwaway) ===%s\n", bold, reset)
		fmt.Printf("%stwo centered lines: row14 UTC (white), row15 media (yellow); pop-on, EOC-on-boundary%s\n\n", dim, reset)
		fmt.Printf("%sfps%s %.2f   %scc_count%s %d   %sframe#%s %d   %swall%s %s   %squeue%s %d\n",
			bold, reset, g.fps, bold, reset, g.ccCount, bold, reset, g.frame, bold, reset,
			time.UnixMilli(wallAt(g.frame)).UTC().Format("15:04:05.000"), bold, reset, len(g.queue))
		if g.overran {
			fmt.Printf("%s\x1b[31m!! OVERRUN: two full lines did not finish building within 1s at this fps !!%s\n", bold, reset)
		}
		fmt.Printf("\n%sscreen (displayed):%s\n", bold, reset)
		fmt.Printf("  14 |%s|\n", renderRow(g.displayed[14]))
		fmt.Printf("  15 |%s|\n", renderRow(g.displayed[15]))
		fmt.Printf("\n%srecent frames%s\n", bold, reset)
		start := 0
		if len(recent) > 14 {
			start = len(recent) - 14
		}
		for _, f := range recent[start:] {
			mark := " "
			if f.Flip {
				mark = "*"
			}
			fmt.Printf("  %sf%-4d%s %s cc=%2d  %02x %02x  %-6s %s%s%s\n",
				dim, f.Frame, reset, mark, f.CcCount, f.Field1.B0, f.Field1.B1, f.Field1.Kind, dim, f.Field1.Note, reset)
		}
		fmt.Printf("\n%s[f]%s frame  %s[s]%s second  %s[1..5]%s fps 25/30/29.97/50/60  %s[a]%s auto 2s  %s[q]%s quit\n",
			bold, reset, bold, reset, bold, reset, bold, reset, bold, reset)
	}

	render()
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		switch strings.TrimSpace(sc.Text()) {
		case "f", "":
			step()
		case "s":
			stepSecond()
		case "a":
			stepSecond()
			stepSecond()
		case "1":
			setFPS(0)
		case "2":
			setFPS(1)
		case "3":
			setFPS(2)
		case "4":
			setFPS(3)
		case "5":
			setFPS(4)
		case "q":
			return
		}
		render()
	}
}
