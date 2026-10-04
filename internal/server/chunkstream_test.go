package server

import (
	"bytes"
	"testing"

	"mcgo/internal/proto/v777"
)

// A chunk a player walks away from is forgotten, and the one they walk back
// into is sent again — with the world as it now is, not as it was generated.
// That is the ghost report read the other way round: a chunk that came back
// carrying the generated world would show a block the player had removed, which
// is exactly the "the blocks I removed came back" symptom.
//
// Both halves are load-bearing. The send path hands the client `World.Payload`,
// so if an edit did not change those bytes, no chunk send could ever bring an
// old block back; and since nothing re-sends a chunk because a block changed,
// silence in the chunk log means the chunk was not sent at all — which is what
// rules a chunk payload out when a ghost turns up mid-session.
func TestARevisitedChunkIsSentWithTheEdits(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	// Stream the chunks around the origin.
	s.updateChunks(0.5, 0.5)
	if !s.sent[[2]int32{0, 0}] {
		t.Fatal("chunk (0,0) was not streamed")
	}
	before := s.world.Payload(0, 0)

	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	s.world.SetBlock(1, 65, 1, stone)
	after := s.world.Payload(0, 0)
	if bytes.Equal(before, after) {
		t.Fatal("the chunk payload did not change when a block did, so no chunk send could carry the edit")
	}

	// Far enough away that the chunk falls out of view, then back again.
	s.updateChunks(16*float64(viewRadius+3), 0.5)
	if s.sent[[2]int32{0, 0}] {
		t.Fatal("chunk (0,0) was not forgotten when the player left")
	}
	s.updateChunks(0.5, 0.5)
	if !s.sent[[2]int32{0, 0}] {
		t.Fatal("chunk (0,0) was not streamed again on the way back")
	}
	if got := s.world.Payload(0, 0); !bytes.Equal(got, after) {
		t.Error("the payload for the chunk changed between the edit and the return")
	}
	if s.world.EditsInChunk(0, 0) == 0 {
		t.Error("the chunk holds a player edit but reports no edits, so the send log would stay silent about it")
	}
}
