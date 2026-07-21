package srt

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Eyevinn/go-608/cta608"
)

// SRT carries styling only as light inline HTML-ish tags — <font color>, <i>,
// <u>, <b> — with no positioning and no palette. This file owns the two-way
// translation between those tags and the 608 Pen, quantized to 608's 8-color
// palette (SPEC §8.2, design note W5). It is the whole reason srt is more than a
// timestamp formatter, so it is kept apart from the block/timestamp plumbing in
// srt.go.

// paletteEntry pairs a 608 foreground Color with the representative 24-bit RGB we
// emit for it and match against on the way in. The eight entries are exactly the
// colors 608 can name (white..magenta plus black); ColDefault renders as white on
// the wire, so it is folded to White and never appears here.
type paletteEntry struct {
	color   cta608.Color
	r, g, b uint8
}

// palette is the fixed 8-color 608 foreground palette, using full-intensity
// primaries as the representative hex (the same values decoders and players use
// for these named colors). Order is white-first so a distance tie resolves to
// white — the neutral default — rather than a chromatic color.
var palette = []paletteEntry{
	{cta608.White, 0xff, 0xff, 0xff},
	{cta608.Green, 0x00, 0xff, 0x00},
	{cta608.Blue, 0x00, 0x00, 0xff},
	{cta608.Cyan, 0x00, 0xff, 0xff},
	{cta608.Red, 0xff, 0x00, 0x00},
	{cta608.Yellow, 0xff, 0xff, 0x00},
	{cta608.Magenta, 0xff, 0x00, 0xff},
	{cta608.Black, 0x00, 0x00, 0x00},
}

// colorHex returns the "#rrggbb" string emitted for a 608 foreground Color.
// ColDefault and any color without a palette entry fold to white, matching how
// the wire renders a default/absent foreground (cta608 folds ColDefault->White).
func colorHex(c cta608.Color) string {
	for _, e := range palette {
		if e.color == c {
			return "#" + hex2(e.r) + hex2(e.g) + hex2(e.b)
		}
	}
	return "#ffffff"
}

// hex2 renders a byte as exactly two lowercase hex digits.
func hex2(v uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&0x0f]})
}

// nearestColor quantizes an arbitrary 24-bit RGB to the closest of the 8 608
// colors by squared Euclidean distance in RGB space (design note W5, "nearest of
// 608's 8 colors"). Squared distance avoids a sqrt and never overflows an int for
// byte components. Ties keep the earliest palette entry (white).
func nearestColor(r, g, b uint8) cta608.Color {
	best := palette[0].color
	bestDist := 1 << 30
	for _, e := range palette {
		dr := int(r) - int(e.r)
		dg := int(g) - int(e.g)
		db := int(b) - int(e.b)
		d := dr*dr + dg*dg + db*db
		if d < bestDist {
			bestDist = d
			best = e.color
		}
	}
	return best
}

// namedCSSColors maps the CSS color keywords that name a 608 palette color to
// that color, so a <font color="red"> reads back exactly rather than through the
// hex path. Keywords outside this set are quantized via their hex where known and
// otherwise fall through to white; this is a best-effort convenience, not a full
// CSS color table (SPEC §8.2 keeps srt to a portable common denominator).
var namedCSSColors = map[string]cta608.Color{
	"white":   cta608.White,
	"green":   cta608.Green,
	"lime":    cta608.Green,
	"blue":    cta608.Blue,
	"cyan":    cta608.Cyan,
	"aqua":    cta608.Cyan,
	"red":     cta608.Red,
	"yellow":  cta608.Yellow,
	"magenta": cta608.Magenta,
	"fuchsia": cta608.Magenta,
	"black":   cta608.Black,
}

// parseColor maps a CSS/HTML color value (as written in a <font color> attribute)
// onto a 608 palette color. It accepts #rrggbb and #rgb hex and the CSS keywords
// that name a palette color; anything else falls back to white. Hex is quantized
// to the nearest of 8 (W5).
func parseColor(v string) cta608.Color {
	v = strings.TrimSpace(v)
	if v == "" {
		return cta608.White
	}
	if v[0] == '#' {
		if r, g, b, ok := parseHex(v[1:]); ok {
			return nearestColor(r, g, b)
		}
		return cta608.White
	}
	if c, ok := namedCSSColors[strings.ToLower(v)]; ok {
		return c
	}
	return cta608.White
}

// parseHex decodes a 3- or 6-digit hex color body (no leading '#') into 8-bit RGB.
// A 3-digit value expands each nibble (f -> ff), the standard CSS shorthand.
func parseHex(h string) (r, g, b uint8, ok bool) {
	switch len(h) {
	case 6:
		rv, err1 := strconv.ParseUint(h[0:2], 16, 8)
		gv, err2 := strconv.ParseUint(h[2:4], 16, 8)
		bv, err3 := strconv.ParseUint(h[4:6], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, 0, 0, false
		}
		return uint8(rv), uint8(gv), uint8(bv), true
	case 3:
		rv, err1 := strconv.ParseUint(h[0:1], 16, 8)
		gv, err2 := strconv.ParseUint(h[1:2], 16, 8)
		bv, err3 := strconv.ParseUint(h[2:3], 16, 8)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, 0, 0, false
		}
		return uint8(rv * 0x11), uint8(gv * 0x11), uint8(bv * 0x11), true
	default:
		return 0, 0, 0, false
	}
}

// fontColorRe extracts the value of a color attribute from a <font ...> tag body,
// tolerating single, double, or no quotes and arbitrary spacing around '='.
var fontColorRe = regexp.MustCompile(`(?i)color\s*=\s*["']?([^"'>\s]+)`)

// emitRun writes one run's text wrapped in the inline tags its Pen calls for
// (SPEC §8.2, W5): foreground color -> <font color="#rrggbb"> (white/default
// omitted, since absence renders white), italic -> <i>, underline -> <u>. The
// background is dropped (SRT has no background), and bold is never emitted (it has
// no 608 source). Nesting is font > i > u, closed in reverse.
func emitRun(b *strings.Builder, run cta608.Run) {
	pen := run.Pen
	font := pen.Color != cta608.White && pen.Color != cta608.ColDefault
	if font {
		b.WriteString(`<font color="`)
		b.WriteString(colorHex(pen.Color))
		b.WriteString(`">`)
	}
	if pen.Italic {
		b.WriteString("<i>")
	}
	if pen.Underline {
		b.WriteString("<u>")
	}
	b.WriteString(run.Text)
	if pen.Underline {
		b.WriteString("</u>")
	}
	if pen.Italic {
		b.WriteString("</i>")
	}
	if font {
		b.WriteString("</font>")
	}
}

// formatRow serializes a Screen row to one SRT text line. Runs are taken in
// column order and adjacent runs sharing a Pen are merged first, so the output is
// clean (<i>AB</i>, not <i>A</i><i>B</i>) and re-parses to the same run set.
// Absolute columns are dropped here: SRT has no positioning, so only the styled
// text survives (design note W6).
func formatRow(r cta608.Row) string {
	runs := sortedRuns(r.Runs)
	runs = mergeAdjacent(runs)
	var b strings.Builder
	for _, run := range runs {
		emitRun(&b, run)
	}
	return b.String()
}

// parseLine turns one SRT text line into line-relative styled runs, honoring
// <font color>/<i>/<u>, tracking (but dropping) <b>, and stripping unknown tags
// (SPEC §8.2, W5). The returned runs carry a line-relative Column (the running
// rune offset from the line start) so cta608.CaptionBlock can center them; the
// caller anchors the resulting rows to the bottom (W6). Adjacent text sharing a
// Pen is coalesced into one run.
func parseLine(line string) []cta608.Run {
	var (
		runs                    []cta608.Run
		italic, underline, bold int // depths; bold is tracked only to balance nesting
		colors                  []cta608.Color
		col                     int
	)
	curColor := func() cta608.Color {
		if len(colors) > 0 {
			return colors[len(colors)-1]
		}
		return cta608.White
	}
	flush := func(text string) {
		if text == "" {
			return
		}
		pen := cta608.Pen{Color: curColor(), Italic: italic > 0, Underline: underline > 0}
		if n := len(runs); n > 0 && runs[n-1].Pen == pen {
			runs[n-1].Text += text
			col += utf8.RuneCountInString(text)
			return
		}
		runs = append(runs, cta608.Run{Column: col, Text: text, Pen: pen})
		col += utf8.RuneCountInString(text)
	}

	for i := 0; i < len(line); {
		lt := strings.IndexByte(line[i:], '<')
		if lt < 0 {
			flush(line[i:])
			break
		}
		flush(line[i : i+lt])
		i += lt
		gt := strings.IndexByte(line[i:], '>')
		if gt < 0 { // an unterminated '<': treat the remainder as literal text
			flush(line[i:])
			break
		}
		tag := line[i+1 : i+gt]
		i += gt + 1
		italic, underline, bold, colors = applyTag(tag, italic, underline, bold, colors)
	}
	return runs
}

// applyTag folds one tag body (the text between '<' and '>') into the running
// style state and returns the updated state. Opening tags push; closing tags pop
// (never below zero / an empty stack, so malformed nesting degrades gracefully).
// Unknown tags are ignored (stripped from the output text).
func applyTag(
	tag string,
	italic, underline, bold int,
	colors []cta608.Color,
) (int, int, int, []cta608.Color) {
	closing := false
	name := strings.TrimSpace(tag)
	if strings.HasPrefix(name, "/") {
		closing = true
		name = strings.TrimSpace(name[1:])
	}
	// The tag name is the leading word; attributes (e.g. font color=...) follow.
	if sp := strings.IndexAny(name, " \t"); sp >= 0 {
		name = name[:sp]
	}
	switch strings.ToLower(name) {
	case "i":
		italic = bump(italic, closing)
	case "u":
		underline = bump(underline, closing)
	case "b":
		bold = bump(bold, closing) // tracked to balance nesting, never applied (W5)
	case "font":
		if closing {
			if len(colors) > 0 {
				colors = colors[:len(colors)-1]
			}
		} else {
			colors = append(colors, fontColor(tag))
		}
	default:
		// Unknown tag: strip it, leaving the enclosed text.
	}
	return italic, underline, bold, colors
}

// bump increments an open-tag depth or decrements it on a close, clamped at zero.
func bump(depth int, closing bool) int {
	if closing {
		if depth > 0 {
			return depth - 1
		}
		return 0
	}
	return depth + 1
}

// fontColor pulls the color out of a <font ...> tag body, quantized to the 608
// palette; a font tag with no color attribute inherits white.
func fontColor(tag string) cta608.Color {
	if m := fontColorRe.FindStringSubmatch(tag); m != nil {
		return parseColor(m[1])
	}
	return cta608.White
}
