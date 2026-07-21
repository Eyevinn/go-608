package webvtt

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Eyevinn/go-608/cta608"
)

// rgb is a 24-bit color used only as the common currency for quantizing between
// arbitrary CSS colors and 608's 8-color palette. It never leaves this package.
type rgb struct{ r, g, b uint8 }

// palette608 pins each CTA-608 foreground color to its canonical full-intensity
// RGB. These are the colors 608 can express, so a class/STYLE/<font> color read
// in from WebVTT is quantized to the nearest entry here (design note W5), and a
// Pen color written out uses exactly these hexes in the emitted STYLE block. The
// slice order is also the deterministic order STYLE rules are emitted in.
var palette608 = []struct {
	color cta608.Color
	rgb   rgb
}{
	{cta608.White, rgb{0xff, 0xff, 0xff}},
	{cta608.Green, rgb{0x00, 0xff, 0x00}},
	{cta608.Blue, rgb{0x00, 0x00, 0xff}},
	{cta608.Cyan, rgb{0x00, 0xff, 0xff}},
	{cta608.Red, rgb{0xff, 0x00, 0x00}},
	{cta608.Yellow, rgb{0xff, 0xff, 0x00}},
	{cta608.Magenta, rgb{0xff, 0x00, 0xff}},
	{cta608.Black, rgb{0x00, 0x00, 0x00}},
}

// name608 maps the lowercase 608 color names (as printed by cta608.Color.String)
// to their Color, so a class such as <c.red> resolves without needing a STYLE
// rule at all — the class name itself carries the color. "transparent" is only
// meaningful for a background and is handled by resolveClass's isBG branch.
var name608 = map[string]cta608.Color{
	"white":   cta608.White,
	"green":   cta608.Green,
	"blue":    cta608.Blue,
	"cyan":    cta608.Cyan,
	"red":     cta608.Red,
	"yellow":  cta608.Yellow,
	"magenta": cta608.Magenta,
	"black":   cta608.Black,
}

// cssNames is a small subset of the CSS named colors, enough to quantize common
// WebVTT/CSS authoring (the eight primaries plus their aliases and a few dark
// variants) to the 608 palette. Anything outside the table still resolves via
// #hex or rgb() parsing; unknown names simply fail to resolve and are dropped.
var cssNames = map[string]rgb{
	"white":   {0xff, 0xff, 0xff},
	"black":   {0x00, 0x00, 0x00},
	"red":     {0xff, 0x00, 0x00},
	"lime":    {0x00, 0xff, 0x00},
	"green":   {0x00, 0x80, 0x00},
	"blue":    {0x00, 0x00, 0xff},
	"cyan":    {0x00, 0xff, 0xff},
	"aqua":    {0x00, 0xff, 0xff},
	"yellow":  {0xff, 0xff, 0x00},
	"magenta": {0xff, 0x00, 0xff},
	"fuchsia": {0xff, 0x00, 0xff},
	"silver":  {0xc0, 0xc0, 0xc0},
	"gray":    {0x80, 0x80, 0x80},
	"grey":    {0x80, 0x80, 0x80},
	"maroon":  {0x80, 0x00, 0x00},
	"olive":   {0x80, 0x80, 0x00},
	"navy":    {0x00, 0x00, 0x80},
	"teal":    {0x00, 0x80, 0x80},
	"purple":  {0x80, 0x00, 0x80},
	"orange":  {0xff, 0xa5, 0x00},
}

// cssHex renders a 608 Color as a "#rrggbb" string for a STYLE rule, from the
// canonical palette. Transparent has no hex form and is written as the CSS
// keyword by the caller; a default/unknown color falls back to white.
func cssHex(c cta608.Color) string {
	for _, p := range palette608 {
		if p.color == c {
			return fmt.Sprintf("#%02x%02x%02x", p.rgb.r, p.rgb.g, p.rgb.b)
		}
	}
	return "#ffffff"
}

// nearestColor quantizes an arbitrary RGB to the closest 608 palette color by
// squared Euclidean distance — the nearest-of-8 mapping of W5. Squared distance
// is monotonic with distance, so no square root is needed to pick the minimum.
func nearestColor(v rgb) cta608.Color {
	best := cta608.White
	bestDist := int(^uint(0) >> 1) // max int
	for _, p := range palette608 {
		dr := int(v.r) - int(p.rgb.r)
		dg := int(v.g) - int(p.rgb.g)
		db := int(v.b) - int(p.rgb.b)
		if d := dr*dr + dg*dg + db*db; d < bestDist {
			bestDist, best = d, p.color
		}
	}
	return best
}

// parseCSSColor parses a CSS color value into RGB, accepting #rgb, #rrggbb,
// rgb()/rgba(), and the named colors in cssNames. It returns ok=false for values
// it cannot interpret (including "transparent", which has no RGB) so the caller
// can drop or specially handle them.
func parseCSSColor(s string) (rgb, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return rgb{}, false
	case strings.HasPrefix(s, "#"):
		return parseHexColor(s[1:])
	case strings.HasPrefix(s, "rgb"):
		return parseRGBFunc(s)
	default:
		v, ok := cssNames[s]
		return v, ok
	}
}

// parseHexColor parses the digits after a leading '#', supporting the 3-digit
// shorthand (#rgb -> #rrggbb) and the 6-digit form.
func parseHexColor(h string) (rgb, bool) {
	switch len(h) {
	case 3:
		r, ok1 := hexNibble(h[0])
		g, ok2 := hexNibble(h[1])
		b, ok3 := hexNibble(h[2])
		if ok1 && ok2 && ok3 {
			return rgb{r*16 + r, g*16 + g, b*16 + b}, true
		}
	case 6:
		n, err := strconv.ParseUint(h, 16, 32)
		if err == nil {
			return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
		}
	}
	return rgb{}, false
}

// hexNibble decodes a single hex digit.
func hexNibble(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// parseRGBFunc parses an rgb()/rgba() functional value, using only the first
// three channels (any alpha is ignored — 608 has no alpha).
func parseRGBFunc(s string) (rgb, bool) {
	open := strings.IndexByte(s, '(')
	closeParen := strings.IndexByte(s, ')')
	if open < 0 || closeParen < 0 || closeParen < open {
		return rgb{}, false
	}
	parts := strings.FieldsFunc(s[open+1:closeParen], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
	if len(parts) < 3 {
		return rgb{}, false
	}
	var ch [3]uint8
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil || n < 0 || n > 255 {
			return rgb{}, false
		}
		ch[i] = uint8(n)
	}
	return rgb{ch[0], ch[1], ch[2]}, true
}

// styleContext carries the class->color maps collected from a document's STYLE
// blocks so inline <c.name> classes can resolve to a color even when the name is
// not itself a 608 color word. fg holds foreground (color:) rules and bg holds
// background (background-color:) rules, both keyed by class name.
type styleContext struct {
	fg map[string]rgb
	bg map[string]rgb
}

// resolveClass turns a class name into a 608 color: first a literal 608 name
// (<c.red>), then a STYLE-block rule for the class, then a bare CSS color name or
// hex, quantizing whatever it finds to the palette. isBG selects the background
// map and enables the "transparent" keyword. It returns ok=false when the name
// carries no recognizable color (an unknown class), which the caller ignores.
func (sc styleContext) resolveClass(name string, isBG bool) (cta608.Color, bool) {
	name = strings.ToLower(name)
	if isBG && name == "transparent" {
		return cta608.Transparent, true
	}
	if c, ok := name608[name]; ok {
		return c, true
	}
	cssMap := sc.fg
	if isBG {
		cssMap = sc.bg
	}
	if v, ok := cssMap[name]; ok {
		return nearestColor(v), true
	}
	if v, ok := parseCSSColor(name); ok {
		return nearestColor(v), true
	}
	return 0, false
}

// tag is one open WebVTT span element on the parse stack. name is the element
// name ("c", "i", "u", "b", "v", ...) used to match a closing tag; the remaining
// fields record the Pen effect the tag contributes. Bold (<b>) pushes a tag with
// no Pen effect, which is exactly how bold is "dropped" on the way in (W5).
type tag struct {
	name      string
	color     cta608.Color
	hasColor  bool
	italic    bool
	underline bool
	bg        cta608.Color
	hasBG     bool
}

// currentPen folds the active tag stack into the effective Pen: the innermost
// color and background win, and italic/underline are set by any enclosing tag.
// The base foreground is White (608's default), so untagged text renders white.
func currentPen(stack []tag) cta608.Pen {
	pen := cta608.Pen{Color: cta608.White}
	for _, t := range stack {
		if t.hasColor {
			pen.Color = t.color
		}
		if t.hasBG {
			pen.Background = t.bg
		}
		if t.italic {
			pen.Italic = true
		}
		if t.underline {
			pen.Underline = true
		}
	}
	return pen
}

// makeTag builds a tag from an opening tag's inner text (e.g. "c.red.bg_black"
// or "v Bob"). The element name is the token up to the first '.' or space; the
// dot-separated classes after it set color/background (a class may sit on any
// element, not just <c>), and any space-separated annotation is ignored. <i>/<u>
// set italic/underline; <b> and everything else contribute no Pen effect, so
// bold, voices, langs, and unknown tags are transparently stripped (W5).
func makeTag(inner string, sc styleContext) tag {
	dotPart, _, _ := strings.Cut(inner, " ") // annotation (e.g. "v Bob") dropped
	parts := strings.Split(dotPart, ".")
	t := tag{name: parts[0]}
	switch t.name {
	case "i":
		t.italic = true
	case "u":
		t.underline = true
	}
	for _, cl := range parts[1:] {
		if cl == "" {
			continue
		}
		if strings.HasPrefix(cl, "bg_") {
			if c, ok := sc.resolveClass(cl[len("bg_"):], true); ok {
				t.bg, t.hasBG = c, true
			}
			continue
		}
		if c, ok := sc.resolveClass(cl, false); ok {
			t.color, t.hasColor = c, true
		}
	}
	return t
}

// popTag removes the innermost open tag whose name matches, so </c> closes the
// nearest <c...>. WebVTT nesting is not strictly validated here; an unmatched
// close is simply ignored, which keeps a slightly malformed payload parseable.
func popTag(stack []tag, name string) []tag {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].name == name {
			return append(stack[:i], stack[i+1:]...)
		}
	}
	return stack
}

// parseLine parses one WebVTT payload line into runs positioned at line-relative
// columns (the first cell is column 0; the caller shifts by the cue's left edge)
// and returns the line's rendered width in cells. It walks a tag stack so nested
// styling composes, coalesces maximal same-Pen spans into runs, decodes the WebVTT
// character entities, and drops timestamp tags (<00:00:00.000>) and any malformed
// "<" with no ">". Bold/voice/lang/unknown tags carry no Pen effect (W5).
func parseLine(s string, sc styleContext) ([]cta608.Run, int) {
	var (
		stack    []tag
		runs     []cta608.Run
		cur      strings.Builder
		curPen   = cta608.Pen{Color: cta608.White}
		startCol int
		col      int
	)
	flush := func() {
		if cur.Len() > 0 {
			runs = append(runs, cta608.Run{Column: startCol, Text: cur.String(), Pen: curPen})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				i = len(s) // malformed: no closing '>', stop
				break
			}
			inner := s[i+1 : i+j]
			i += j + 1
			if inner == "" || isTimestampTag(inner) {
				continue // stray "<>" or a <00:00:00.000> cue timestamp: skip
			}
			if strings.HasPrefix(inner, "/") {
				name, _, _ := strings.Cut(inner[1:], " ")
				stack = popTag(stack, name)
			} else {
				stack = append(stack, makeTag(inner, sc))
			}
			if pen := currentPen(stack); pen != curPen {
				flush()
				curPen, startCol = pen, col
			}
		case '&':
			if txt, size, ok := decodeEntity(s[i:]); ok {
				if cur.Len() == 0 {
					startCol = col
				}
				cur.WriteString(txt)
				col += utf8.RuneCountInString(txt)
				i += size
				continue
			}
			if cur.Len() == 0 {
				startCol = col
			}
			cur.WriteByte('&')
			col++
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if cur.Len() == 0 {
				startCol = col
			}
			cur.WriteRune(r)
			col++
			i += size
		}
	}
	flush()
	return runs, col
}

// isTimestampTag reports whether an inner tag text is a WebVTT cue timestamp
// (e.g. "00:00:01.000") rather than a styling element: those advance karaoke
// timing and carry no text, so the serializer drops them.
func isTimestampTag(inner string) bool {
	return len(inner) > 0 && inner[0] >= '0' && inner[0] <= '9'
}

// decodeEntity decodes a WebVTT/HTML character reference at the start of s. It
// returns the decoded text (empty for the zero-width bidi marks), the number of
// bytes consumed, and ok=false when s does not begin with a recognized entity.
func decodeEntity(s string) (string, int, bool) {
	end := strings.IndexByte(s, ';')
	if end < 0 || end > 8 {
		return "", 0, false
	}
	switch s[:end+1] {
	case "&amp;":
		return "&", end + 1, true
	case "&lt;":
		return "<", end + 1, true
	case "&gt;":
		return ">", end + 1, true
	case "&nbsp;":
		return " ", end + 1, true
	case "&lrm;", "&rlm;":
		return "", end + 1, true // zero-width bidi marks: consumed, emit nothing
	}
	// Numeric character reference: &#NN; (decimal) or &#xHH; / &#XHH; (hex).
	if strings.HasPrefix(s, "&#") {
		body, base := s[2:end], 10
		if len(body) > 0 && (body[0] == 'x' || body[0] == 'X') {
			body, base = body[1:], 16
		}
		if n, err := strconv.ParseInt(body, base, 32); err == nil && n > 0 && utf8.ValidRune(rune(n)) {
			return string(rune(n)), end + 1, true
		}
	}
	return "", 0, false
}

// escapeText escapes the three characters that are significant in WebVTT payload
// text so a run's literal content survives a round-trip.
func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// styledText wraps a run's text in the WebVTT span markup that carries its Pen:
// a non-white foreground and any background become <c.class...> classes (recorded
// in usedFg/usedBg so the writer can emit the matching STYLE rules), italic and
// underline become <i>/<u>. White/default foreground needs no class (it is the
// WebVTT default). Bold has no 608 source, so it is never emitted (W5).
func styledText(text string, pen cta608.Pen, usedFg, usedBg map[cta608.Color]bool) string {
	var classes []string
	if pen.Color != cta608.White && pen.Color != cta608.ColDefault {
		classes = append(classes, pen.Color.String())
		usedFg[pen.Color] = true
	}
	if pen.Background != cta608.ColDefault {
		classes = append(classes, "bg_"+pen.Background.String())
		usedBg[pen.Background] = true
	}
	out := escapeText(text)
	if pen.Underline {
		out = "<u>" + out + "</u>"
	}
	if pen.Italic {
		out = "<i>" + out + "</i>"
	}
	if len(classes) > 0 {
		out = "<c." + strings.Join(classes, ".") + ">" + out + "</c>"
	}
	return out
}

// writeStyleBlock writes the STYLE header block mapping each used color class to
// a CSS color, in the deterministic palette order. It is emitted once, before the
// cues, only when at least one non-default color was used; players that ignore
// STYLE still get the class names, and this serializer resolves either way (W5).
func writeStyleBlock(sb *strings.Builder, usedFg, usedBg map[cta608.Color]bool) {
	if len(usedFg) == 0 && len(usedBg) == 0 {
		return
	}
	sb.WriteString("STYLE\n")
	for _, p := range palette608 {
		if usedFg[p.color] {
			fmt.Fprintf(sb, "::cue(.%s) { color: %s; }\n", p.color.String(), cssHex(p.color))
		}
	}
	for _, p := range palette608 {
		if usedBg[p.color] {
			fmt.Fprintf(sb, "::cue(.bg_%s) { background-color: %s; }\n", p.color.String(), cssHex(p.color))
		}
	}
	if usedBg[cta608.Transparent] {
		sb.WriteString("::cue(.bg_transparent) { background-color: transparent; }\n")
	}
	sb.WriteString("\n")
}

// parseStyleBlock extracts "::cue(.class) { color: ...; background-color: ...; }"
// rules from a STYLE block's body into the styleContext maps. It scans rather than
// fully parsing CSS: the go-608 writer emits exactly this shape, and it is the
// common convention for 608-derived WebVTT, so a lightweight scan is sufficient
// and robust to whitespace. Global ::cue rules (no class) are ignored — a color
// cannot be attached to a specific run without a class.
func parseStyleBlock(body string, sc styleContext) {
	for {
		open := strings.Index(body, "::cue(.")
		if open < 0 {
			return
		}
		rest := body[open+len("::cue(."):]
		closeParen := strings.IndexByte(rest, ')')
		braceOpen := strings.IndexByte(rest, '{')
		braceClose := strings.IndexByte(rest, '}')
		if closeParen < 0 || braceOpen < 0 || braceClose < 0 || braceClose < braceOpen {
			return
		}
		class := strings.ToLower(strings.TrimSpace(rest[:closeParen]))
		decls := rest[braceOpen+1 : braceClose]
		applyStyleDecls(class, decls, sc)
		body = rest[braceClose+1:]
	}
}

// applyStyleDecls parses the "prop: value;" declarations of one ::cue rule,
// recording a color: as a foreground rule for the class and a background-color:
// as a background rule. background-color must be checked before color so it is
// not swallowed by the color: prefix test.
func applyStyleDecls(class, decls string, sc styleContext) {
	for _, decl := range strings.Split(decls, ";") {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		val = strings.TrimSpace(val)
		switch prop {
		case "background-color", "background":
			if v, ok := parseCSSColor(val); ok {
				sc.bg[class] = v
			}
		case "color":
			if v, ok := parseCSSColor(val); ok {
				sc.fg[class] = v
			}
		}
	}
}
