package cta608

// DemuxField splits one NTSC field's byte stream into its two in-field data
// channels and parses each into a token stream. The channel of every control
// pair is taken from the first byte's high nibble (0x10..0x17 -> channel 1,
// 0x18..0x1F -> channel 2); character and padding pairs are attributed to the
// most recently addressed channel (channel 1 until the first channel-2 control
// code). Field-2 XDS control codes (first byte 0x01..0x0F) are left to Parse,
// which skips them.
func DemuxField(fieldBytes []byte, opts ParseOptions) (ch1, ch2 []Token, err error) {
	var b1, b2 []byte
	current := 1
	for i := 0; i < len(fieldBytes); i += 2 {
		r0 := fieldBytes[i]
		var r1 byte
		if i+1 < len(fieldBytes) {
			r1 = fieldBytes[i+1]
		}
		c0 := r0 & 0x7f
		if c0 >= 0x10 && c0 <= 0x1f {
			if c0 <= 0x17 {
				current = 1
			} else {
				current = 2
			}
		}
		if current == 1 {
			b1 = append(b1, r0, r1)
		} else {
			b2 = append(b2, r0, r1)
		}
	}
	if ch1, err = Parse(b1, opts); err != nil {
		return nil, nil, err
	}
	if ch2, err = Parse(b2, opts); err != nil {
		return nil, nil, err
	}
	return ch1, ch2, nil
}

// MuxField joins two channels' token streams into one field byte stream by
// serializing each on its own channel and concatenating them. opts.Channel is
// ignored (channel 1 is used for ch1 and channel 2 for ch2); opts.Field and
// opts.Doubling apply to both. For the result to round-trip through DemuxField,
// the ch2 stream should begin with a channel-addressed control token (a PAC or
// SetMode), as real caption channels do.
func MuxField(ch1, ch2 []Token, opts SerializeOptions) []byte {
	o1 := opts
	o1.Channel = 1
	o2 := opts
	o2.Channel = 2
	out := Serialize(ch1, o1)
	return append(out, Serialize(ch2, o2)...)
}
