package mcproto

import "testing"

// A VarInt and a VarLong have to survive a round trip, including the negative
// values that fill all their bytes.
func TestVarLongRoundTrip(t *testing.T) {
	for _, want := range []int64{0, 1, -1, 127, 128, 24000, -24000, 1 << 40, -(1 << 40), 1<<62 - 1} {
		var w Writer
		w.WriteVarLong(want)
		got, err := NewReader(w.Bytes()).ReadVarLong()
		if err != nil {
			t.Fatalf("reading %d back: %v", want, err)
		}
		if got != want {
			t.Errorf("wrote %d, read %d", want, got)
		}
	}
}

// A VarLong that runs off the end is an error, not a wrong number.
func TestVarLongTruncated(t *testing.T) {
	if _, err := NewReader([]byte{0x80, 0x80}).ReadVarLong(); err == nil {
		t.Error("a truncated VarLong read without error")
	}
}
