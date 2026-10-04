package server

import (
	"testing"
	"time"

	"mcgo/internal/proto/v777"
)

// The block update goes out BEFORE the ack. The ack is what stops the client
// predicting the change, and the client then draws the server's state — so if
// the update has not arrived yet it draws what was there before, and the block
// flickers: placed, gone for a frame, placed again.
func TestBlockUpdatePrecedesTheAck(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	srv.register(s)
	tab := v777.Tables
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	s.world.SetBlock(4, 65, 4, stone)

	// A break, through the handler, which is where the ack belongs.
	s.handlePlayerAction(actionPacket(3, 4, 65, 4, 1, 7))

	// The ack is the last thing the handler sends, so drain until it turns up.
	var order []int32
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case p := <-got:
			order = append(order, p.ID)
			if p.ID == tab.PlayClientboundBlockChangedAck {
				goto check
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatalf("no ack arrived; saw %v", order)

check:
	updateAt, ackAt := -1, -1
	for i, id := range order {
		if id == tab.PlayClientboundBlockUpdate && updateAt < 0 {
			updateAt = i
		}
		if id == tab.PlayClientboundBlockChangedAck && ackAt < 0 {
			ackAt = i
		}
	}
	if updateAt < 0 {
		t.Fatalf("no block update arrived; saw %v", order)
	}
	if updateAt > ackAt {
		t.Errorf("the ack is at %d and the block update at %d: the update has to come first", ackAt, updateAt)
	}
}

// Fluid saved in the world file is put back in the tick queue on the first tick.
// Without that, a lava column that was falling when the server stopped hangs in
// the air for good, because nothing has touched it since it was loaded.
func TestSavedFluidsFlowAfterARestart(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lava := srv.lavaLevelsFor(tab)
	if lava == nil {
		t.Fatal("no lava block states")
	}
	// What a restart leaves behind: a stream that was flowing when the server
	// stopped — a source with the column under it already falling — and nothing
	// queued to tick it.
	w.SetBlock(0, 81, 0, lava.byLevel[waterSource])
	w.SetBlock(0, 80, 0, lava.byLevel[waterFalling])

	fell := false
	for range 4 {
		srv.tickFluids()
		if _, ok := lava.level[w.Block(0, 79, 0)]; ok {
			fell = true
			break
		}
	}
	if !fell {
		t.Errorf("the saved stream did not resume falling: %d below the column", w.Block(0, 79, 0))
	}
}
