package cta608

// Doubling controls control-code doubling (CTA-608-E §D.2): two-byte control
// codes may be transmitted twice in successive frames for analog robustness.
type Doubling uint8

// Doubling modes.
const (
	// DoublingDefault doubles control codes on field 1 and not on field 2,
	// the §D.2 posture.
	DoublingDefault Doubling = iota
	// DoublingOn always doubles control codes.
	DoublingOn
	// DoublingOff never doubles control codes.
	DoublingOff
)

// SerializeOptions configures the wire encoding. The zero value serializes
// channel 1 of field 1 with control-code doubling on (the standard default).
type SerializeOptions struct {
	// Field is the NTSC field (1 or 2); 0 is treated as 1. It only selects the
	// DoublingDefault behavior.
	Field int
	// Channel is the in-field data channel (1 or 2); 0 is treated as 1. Channel
	// 2 sets the high nibble of control first bytes (0x10..0x17 -> 0x18..0x1F).
	Channel int
	// Doubling selects the control-code doubling policy.
	Doubling Doubling
}

// doubleControlCodes resolves the effective doubling flag for the options.
func (o SerializeOptions) doubleControlCodes() bool {
	switch o.Doubling {
	case DoublingOn:
		return true
	case DoublingOff:
		return false
	default: // DoublingDefault: on for field 1, off for field 2
		return o.Field != 2
	}
}

// channelOffset returns the value added to a control first byte for the target
// channel (8 for channel 2, else 0).
func (o SerializeOptions) channelOffset() byte {
	if o.Channel == 2 {
		return 8
	}
	return 0
}

// Serialize encodes a token stream into odd-parity cc_data byte pairs. It owns
// all wire concerns: odd parity, control-code doubling, two-characters-per-pair
// packing, null-pair frame alignment of two-byte control codes, and the
// extended-character backspace-and-replace. The result always has an even
// length (whole byte pairs). Out-of-range fields are clamped to the nearest
// valid value; runes outside the 608 repertoire are encoded as '?'.
func Serialize(tokens []Token, opts SerializeOptions) []byte {
	e := encoder{opts: opts, pending: -1}
	for _, tok := range tokens {
		e.emit(tok)
	}
	e.flush()
	return e.out
}

type encoder struct {
	opts    SerializeOptions
	out     []byte
	pending int // a buffered single character byte awaiting a pair partner; -1 = none
}

// charByte packs a single standard-set character byte, two per pair.
func (e *encoder) charByte(b byte) {
	if e.pending < 0 {
		e.pending = int(b)
		return
	}
	e.out = append(e.out, oddParity(byte(e.pending)), oddParity(b))
	e.pending = -1
}

// flush emits any buffered character, padding the pair with a null low byte
// (0x00 -> 0x80 after parity) to keep two-byte control codes frame-aligned.
func (e *encoder) flush() {
	if e.pending < 0 {
		return
	}
	e.out = append(e.out, oddParity(byte(e.pending)), oddParity(0x00))
	e.pending = -1
}

// ctrl emits a two-byte control pair. It first flushes any buffered character
// (frame alignment), applies the channel offset to the first byte, and doubles
// the pair when double is set.
func (e *encoder) ctrl(b0, b1 byte, double bool) {
	e.flush()
	p0, p1 := oddParity(b0+e.opts.channelOffset()), oddParity(b1)
	e.out = append(e.out, p0, p1)
	if double {
		e.out = append(e.out, p0, p1)
	}
}

func (e *encoder) emit(tok Token) {
	dbl := e.opts.doubleControlCodes()
	switch t := tok.(type) {
	case Chars:
		e.emitChars(t.Text)
	case PAC:
		b0, b1 := encodePAC(t)
		e.ctrl(b0, b1, dbl)
	case MidRow:
		e.ctrl(0x11, encodeMidRow(t.Pen), dbl)
	case TabOffset:
		cols := clamp(t.Columns, 1, 3)
		e.ctrl(0x17, 0x20+byte(cols), dbl)
	case BackgroundAttr:
		if b0, b1, ok := encodeBackground(t.Pen); ok {
			e.ctrl(b0, b1, dbl)
		}
	case SetMode:
		e.ctrl(0x14, encodeSetMode(t), dbl)
	case Command:
		e.ctrl(0x14, encodeOp(t.Op), dbl)
	}
}

// emitChars encodes a run of runes: standard chars pack two per pair, special
// chars emit their 0x11 code, and extended chars emit a standard fallback then
// their two-byte code (backspace-and-replace). Character codes are never
// doubled.
func (e *encoder) emitChars(text string) {
	for _, r := range text {
		ic, ok := runeToInternal[r]
		if !ok {
			ic = '?' // 0x3F, standard set
		}
		switch {
		case ic <= 0x7f: // standard set
			e.charByte(ic)
		case ic <= 0x8f: // special set: (0x11, ic-0x50)
			e.ctrl(0x11, ic-0x50, false)
		case ic <= 0xaf: // extended set A: fallback + (0x12, ic-0x70)
			e.charByte(fallbackByte(ic))
			e.ctrl(0x12, ic-0x70, false)
		default: // extended set B: fallback + (0x13, ic-0x90)
			e.charByte(fallbackByte(ic))
			e.ctrl(0x13, ic-0x90, false)
		}
	}
}

// fallbackByte returns the standard-set byte emitted before an extended code.
func fallbackByte(ic byte) byte {
	if r, ok := extFallback[ic]; ok {
		if b, ok := runeToInternal[r]; ok {
			return b
		}
	}
	return '?' // 0x3F
}

// encodePAC returns the channel-1 byte pair for a PAC.
func encodePAC(p PAC) (byte, byte) {
	row := clamp(p.Row, 1, 15)
	rc := pacRowEncode[row]
	base := byte(0x40)
	if rc.upper {
		base = 0x60
	}
	var b1 byte
	if p.Indent == NoIndent {
		var colorPart byte
		if p.Pen.Italic {
			colorPart = 0x0e
		} else {
			colorPart = byte(2 * wireColorIndex(p.Pen.Color))
		}
		b1 = base + colorPart + underlineBit(p.Pen.Underline)
	} else {
		indent := clamp(p.Indent, 0, 28) / 4 * 2
		b1 = base + 0x10 + byte(indent) + underlineBit(p.Pen.Underline)
	}
	return rc.byte1, b1
}

// encodeMidRow returns the channel-1 second byte for a mid-row code.
func encodeMidRow(pen Pen) byte {
	if pen.Italic {
		return 0x2e + underlineBit(pen.Underline)
	}
	return 0x20 + byte(2*wireColorIndex(pen.Color)) + underlineBit(pen.Underline)
}

// encodeBackground returns the channel-1 byte pair for a background/foreground
// attribute, or ok=false if the pen expresses neither.
func encodeBackground(pen Pen) (b0, b1 byte, ok bool) {
	switch {
	case pen.Background == Transparent:
		return 0x17, 0x2d, true
	case pen.Background != ColDefault:
		return 0x10, 0x20 + byte(2*bgWireIndex(pen.Background)), true
	case pen.Color == Black:
		return 0x17, 0x2e + underlineBit(pen.Underline), true
	default:
		return 0, 0, false
	}
}

// encodeSetMode returns the channel-1 second byte for a mode switch.
func encodeSetMode(s SetMode) byte {
	switch s.Mode {
	case RollUp:
		return 0x23 + byte(clamp(s.RollUpRows, 2, 4)) // RU2=0x25, RU3=0x26, RU4=0x27
	case PaintOn:
		return 0x29 // RDC
	default: // PopOn
		return 0x20 // RCL
	}
}

// encodeOp returns the channel-1 second byte for a miscellaneous command.
func encodeOp(op Op) byte {
	switch op {
	case EOC:
		return 0x2f
	case EDM:
		return 0x2c
	case ENM:
		return 0x2e
	case CR:
		return 0x2d
	case BS:
		return 0x21
	case DER:
		return 0x24
	case TR:
		return 0x2a
	case RTD:
		return 0x2b
	case FON:
		return 0x28
	case AOF:
		return 0x22
	case AON:
		return 0x23
	default:
		return 0x2f // EOC as a safe default
	}
}

func underlineBit(u bool) byte {
	if u {
		return 1
	}
	return 0
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
