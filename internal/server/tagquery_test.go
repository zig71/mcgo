package server

import (
	"bytes"
	"testing"

	"mcgo/internal/world"
)

// TestTagQueryReply pins the two shapes of a tag query reply. The transaction
// id must come back intact — the client matches the answer to its request by
// that id, and a reply it cannot match is worse than no reply — and an absent
// tag is a single zero byte, not a compound with nothing in it.
func TestTagQueryReply(t *testing.T) {
	const id = 7

	absent := tagQueryReply(id, world.SignText{}, false)
	if len(absent) != 2 {
		t.Errorf("absent reply is %d bytes, want 2 (varint id + TAG_End)", len(absent))
	}
	if absent[0] != id {
		t.Errorf("transaction id = %d, want %d", absent[0], id)
	}
	if absent[1] != 0 {
		t.Errorf("absent tag byte = %d, want 0", absent[1])
	}

	text := world.SignText{}
	got := tagQueryReply(id, text, true)
	if got[0] != id {
		t.Errorf("transaction id = %d, want %d", got[0], id)
	}
	if got[1] != 0x0A {
		t.Errorf("tag type = %#x, want 0x0A (compound)", got[1])
	}
	// A compound payload follows with no name, and must match the sign tag the
	// block entity data path already sends.
	nbt := world.SignNBT(text)
	if !bytes.Equal(got[2:], nbt[1:]) {
		t.Errorf("reply tag differs from the sign tag: %d bytes vs %d", len(got)-2, len(nbt)-1)
	}
}
