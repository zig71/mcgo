package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
)

// Every block action carries a sequence and every one is acked, including the
// actions that change nothing. A sequence left un-acked is a prediction the
// client never lets go of: it shows a block the server disagrees with, and the
// disagreement surfaces whenever that chunk is sent again — which is what a
// block that was removed appearing to come back looks like.
func TestActionsThatChangeNothingAreStillAcked(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables

	// Ringing a bell: the server sends the swing and keeps the bell.
	t.Run("bell", func(t *testing.T) {
		s, got, _ := pipedSession(t, srv)
		srv.register(s)
		bell, ok := srv.blockState(tab, "minecraft:bell", nil)
		if !ok {
			t.Fatal("no bell block state")
		}
		s.world.SetBlock(4, 65, 4, bell)

		s.handlePlayerAction(actionPacket(3, 4, 65, 4, 1, 11))
		if seq := ackSequence(t, got, tab); seq != 11 {
			t.Errorf("the ack is for sequence %d, want 11", seq)
		}
	})

	// Breaking something unbreakable: refused, and acked.
	t.Run("bedrock", func(t *testing.T) {
		s, got, _ := pipedSession(t, srv)
		srv.register(s)
		bedrock, ok := srv.blockState(tab, "minecraft:bedrock", nil)
		if !ok {
			t.Fatal("no bedrock block state")
		}
		s.world.SetBlock(6, 65, 4, bedrock)

		s.handlePlayerAction(actionPacket(3, 6, 65, 4, 1, 12))
		if seq := ackSequence(t, got, tab); seq != 12 {
			t.Errorf("the ack is for sequence %d, want 12", seq)
		}
		if s.world.Block(6, 65, 4) != bedrock {
			t.Error("bedrock was broken")
		}
	})
}

// ackSequence waits for a block-changed ack and returns its sequence.
func ackSequence(t *testing.T, got chan mcproto.Packet, tab proto.Tables) int32 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case p := <-got:
			if p.ID != tab.PlayClientboundBlockChangedAck {
				continue
			}
			r := mcproto.NewReader(p.Data)
			seq, err := r.ReadVarInt()
			if err != nil {
				t.Fatalf("sequence: %v", err)
			}
			return seq
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("no ack arrived")
	return 0
}
