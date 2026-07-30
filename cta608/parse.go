package cta608

import "fmt"

// ParseOptions configures decoding. The zero value strips parity (masks the
// high bit and trusts the low seven).
type ParseOptions struct {
	// ValidateParity makes Parse return an error when any byte does not have
	// odd parity, instead of silently stripping the parity bit.
	ValidateParity bool
}

// ParseError reports a byte that failed odd-parity validation.
type ParseError struct {
	Offset int  // byte index in the input
	Byte   byte // the offending byte, as received
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("cta608: parity error at byte %d: %#02x", e.Offset, e.Byte)
}

// Parse decodes cc_data byte pairs back into a token stream. It is the inverse
// of Serialize: it strips (or validates) parity, collapses doubled control
// codes to a single logical token, packs character bytes back into Chars runs,
// and folds each extended character's fallback + two-byte code back into one
// rune (backspace-and-replace). It expects a single channel's byte stream; use
// DemuxField for a field carrying both channels.
//
// Collapsing doubled control codes is standard 608 decoder behavior (act on the
// first of two identical successive control pairs, ignore the second) and it
// applies whether or not the sender doubled. A genuine third identical copy is
// honored. One consequence, per CTA-608-E §B.14: two logically-adjacent
// identical control commands (for example two carriage returns) are
// indistinguishable on the wire from one doubled command and collapse to a
// single token; to deliver N identical adjacent commands a sender must transmit
// 2N-1 copies. Character-carrying pairs (special and extended glyphs) are never
// treated as doubled control codes, so repeated glyphs like "♪♪" survive.
func Parse(data []byte, opts ParseOptions) ([]Token, error) {
	var p parser
	return p.parse(data, opts)
}

// parse decodes one chunk of byte pairs, carrying p's cross-chunk state over from
// the previous call. Parse starts from a zero parser, so a whole-buffer parse is
// unaffected; Decoder keeps one parser for its whole lifetime, because it is fed
// incrementally — one pair per video frame — and 608 constructs routinely straddle
// that boundary (a doubled control code, an extended character and its fallback).
func (p *parser) parse(data []byte, opts ParseOptions) ([]Token, error) {
	p.tokens = nil
	for i := 0; i < len(data); i += 2 {
		r0 := data[i]
		var r1 byte
		if i+1 < len(data) {
			r1 = data[i+1]
		}
		if opts.ValidateParity {
			if !hasOddParity(r0) {
				return nil, &ParseError{Offset: i, Byte: r0}
			}
			if i+1 < len(data) && !hasOddParity(r1) {
				return nil, &ParseError{Offset: i + 1, Byte: r1}
			}
		}
		if err := p.pair(r0&0x7f, r1&0x7f); err != nil {
			return nil, err
		}
	}
	p.flushRun()
	return p.tokens, nil
}

type parser struct {
	tokens []Token
	run    []rune // current Chars accumulation

	// doubled-control-code collapse state
	prevC0, prevC1 byte
	prevCollapse   bool
}

func (p *parser) flushRun() {
	if len(p.run) > 0 {
		p.tokens = append(p.tokens, Chars{Text: string(p.run)})
		p.run = p.run[:0]
	}
}

// pair processes one masked byte pair (c0, c1).
func (p *parser) pair(c0, c1 byte) error {
	switch {
	case c0 == 0 && c1 == 0:
		// Null / padding pair — a 608 no-op. Breaks doubling adjacency.
		p.prevCollapse = false
	case c0 >= 0x10 && c0 <= 0x1f:
		p.control(c0, c1)
	case c0 >= 0x20 && c0 <= 0x7f:
		p.appendChar(c0)
		if c1 >= 0x20 && c1 <= 0x7f {
			p.appendChar(c1)
		}
		p.prevCollapse = false
	default:
		// c0 in 0x01..0x0f (field-2 / XDS, out of scope) or a stray low byte.
		p.prevCollapse = false
	}
	return nil
}

// control handles a control-range first byte (0x10..0x1F, either channel).
func (p *parser) control(c0, c1 byte) {
	c0n := c0
	if c0 >= 0x18 {
		c0n = c0 - 8
	}

	// Character-carrying codes are not control commands: route to the run and
	// never collapse them as doubled.
	if c0n == 0x11 && c1 >= 0x30 && c1 <= 0x3f {
		p.appendInternal(c1 + 0x50) // special char
		p.prevCollapse = false
		return
	}
	if (c0n == 0x12 || c0n == 0x13) && c1 >= 0x20 && c1 <= 0x3f {
		// Extended char: backspace over the fallback the sender transmitted ahead of
		// the glyph, then append the glyph.
		//
		// The fallback is usually still pending in the current run, and dropping it
		// there is enough. When it is not — because the two pairs arrived in separate
		// parse calls, which in the timed path is always, one pair per frame — it has
		// already been emitted and displayed, so the receiver has to be told to
		// backspace over it. That is exactly CTA-608-E's backspace-and-replace on the
		// wire, so emitting the BS is the faithful reading rather than a workaround.
		if len(p.run) > 0 {
			p.run = p.run[:len(p.run)-1]
		} else {
			p.flushRun()
			p.tokens = append(p.tokens, Command{Op: BS})
		}
		if c0n == 0x12 {
			p.appendInternal(c1 + 0x70) // extended set A
		} else {
			p.appendInternal(c1 + 0x90) // extended set B
		}
		p.prevCollapse = false
		return
	}

	// A real control command: collapse an immediately-repeated identical pair
	// (control-code doubling), honoring a genuine third copy.
	if p.prevCollapse && p.prevC0 == c0 && p.prevC1 == c1 {
		p.prevCollapse = false
		return
	}
	tok, ok := decodeControl(c0n, c1)
	if !ok {
		p.prevCollapse = false
		return
	}
	p.flushRun()
	p.tokens = append(p.tokens, tok)
	p.prevC0, p.prevC1 = c0, c1
	p.prevCollapse = true
}

func (p *parser) appendChar(b byte) {
	p.run = append(p.run, internalToRune[b])
}

func (p *parser) appendInternal(ic byte) {
	if r, ok := internalToRune[ic]; ok {
		p.run = append(p.run, r)
	}
}

// decodeControl decodes a channel-normalized control pair into its token.
func decodeControl(c0n, c1 byte) (Token, bool) {
	switch c0n {
	case 0x10:
		switch {
		case c1 >= 0x20 && c1 <= 0x2f:
			return decodeBackground(c0n, c1)
		case c1 >= 0x40 && c1 <= 0x5f:
			return decodePAC(c0n, c1)
		}
	case 0x11, 0x12, 0x13, 0x16:
		if c1 >= 0x40 && c1 <= 0x7f {
			return decodePAC(c0n, c1)
		}
		if c0n == 0x11 && c1 >= 0x20 && c1 <= 0x2f {
			return MidRow{Pen: decodeMidRowPen(c1)}, true
		}
	case 0x14, 0x15:
		// 0x14/0x15 are dual-purpose: misc/mode commands (c1 in 0x20..0x2F) and
		// PAC rows 14/15 (0x14) or 5/6 (0x15) (c1 in 0x40..0x7F).
		switch {
		case c1 >= 0x20 && c1 <= 0x2f:
			return decodeCmd(c1)
		case c1 >= 0x40 && c1 <= 0x7f:
			return decodePAC(c0n, c1)
		}
	case 0x17:
		switch {
		case c1 >= 0x21 && c1 <= 0x23:
			return TabOffset{Columns: int(c1 - 0x20)}, true
		case c1 >= 0x2d && c1 <= 0x2f:
			return decodeBackground(c0n, c1)
		case c1 >= 0x40 && c1 <= 0x7f:
			return decodePAC(c0n, c1)
		}
	}
	return nil, false
}

// decodePAC decodes a PAC pair (Table 53).
func decodePAC(c0n, c1 byte) (Token, bool) {
	upper := c1 >= 0x60
	row, ok := pacRowDecode[pacRowCode{byte1: c0n, upper: upper}]
	if !ok {
		return nil, false
	}
	idx := c1 - 0x40
	if upper {
		idx = c1 - 0x60
	}
	underline := idx&1 == 1
	switch {
	case idx <= 0x0d: // color
		return PAC{Row: row, Indent: NoIndent, Pen: Pen{Color: wireColors[idx>>1], Underline: underline}}, true
	case idx <= 0x0f: // white italics
		return PAC{Row: row, Indent: NoIndent, Pen: Pen{Color: White, Italic: true, Underline: underline}}, true
	default: // indent; the standard forces white
		indent := int((idx-0x10)>>1) * 4
		return PAC{Row: row, Indent: indent, Pen: Pen{Color: White, Underline: underline}}, true
	}
}

// decodeMidRowPen decodes a mid-row second byte (Table 51).
func decodeMidRowPen(c1 byte) Pen {
	underline := c1&1 == 1
	if c1 >= 0x2e { // white italics
		return Pen{Color: White, Italic: true, Underline: underline}
	}
	return Pen{Color: wireColors[(c1-0x20)>>1], Underline: underline}
}

// decodeBackground decodes a background/foreground-attribute pair.
func decodeBackground(c0n, c1 byte) (Token, bool) {
	if c0n == 0x10 && c1 >= 0x20 && c1 <= 0x2f {
		// background color (semi-transparent bit c1&1 is not modeled by Pen)
		return BackgroundAttr{Pen: Pen{Background: bgColorFromWireIndex[(c1-0x20)>>1]}}, true
	}
	switch c1 {
	case 0x2d:
		return BackgroundAttr{Pen: Pen{Background: Transparent}}, true
	case 0x2e:
		return BackgroundAttr{Pen: Pen{Color: Black}}, true
	case 0x2f:
		return BackgroundAttr{Pen: Pen{Color: Black, Underline: true}}, true
	}
	return nil, false
}

// decodeCmd decodes a misc/mode control second byte on the 0x14/0x15 channel.
func decodeCmd(c1 byte) (Token, bool) {
	switch c1 {
	case 0x20:
		return SetMode{Mode: PopOn}, true
	case 0x25:
		return SetMode{Mode: RollUp, RollUpRows: 2}, true
	case 0x26:
		return SetMode{Mode: RollUp, RollUpRows: 3}, true
	case 0x27:
		return SetMode{Mode: RollUp, RollUpRows: 4}, true
	case 0x29:
		return SetMode{Mode: PaintOn}, true
	case 0x21:
		return Command{Op: BS}, true
	case 0x22:
		return Command{Op: AOF}, true
	case 0x23:
		return Command{Op: AON}, true
	case 0x24:
		return Command{Op: DER}, true
	case 0x28:
		return Command{Op: FON}, true
	case 0x2a:
		return Command{Op: TR}, true
	case 0x2b:
		return Command{Op: RTD}, true
	case 0x2c:
		return Command{Op: EDM}, true
	case 0x2d:
		return Command{Op: CR}, true
	case 0x2e:
		return Command{Op: ENM}, true
	case 0x2f:
		return Command{Op: EOC}, true
	}
	return nil, false
}
