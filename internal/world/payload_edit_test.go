package world

import (
	"bytes"
	"testing"

	"mcgo/internal/proto/v777"
)

// After a block is broken, the chunk a client is sent must not still carry it.
// A stale payload is exactly what "the blocks I removed came back" looks like:
// a chunk resend hands the client the block again.
func TestChunkPayloadFollowsABreak(t *testing.T) {
	cfg := FlatConfig{
		Air: v777.Tables.BlockAir, Bedrock: v777.Tables.BlockBedrock,
		Dirt: v777.Tables.BlockDirt, Grass: v777.Tables.BlockGrass,
		Stone: v777.Tables.BlockStone,
		MinY:  -64, Height: 384, SurfaceY: 64,
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	// The ground: grass at y = 64.
	surface := w.Block(0, 64, 0)
	if surface == cfg.Air {
		t.Fatalf("no ground at y=64: state %d", surface)
	}
	before := w.Payload(0, 0)

	// Break it, the way a player would.
	w.SetBlock(0, 64, 0, cfg.Air)
	if w.Block(0, 64, 0) != cfg.Air {
		t.Fatal("the break did not reach the world")
	}
	if after := w.Payload(0, 0); bytes.Equal(before, after) {
		t.Fatal("the chunk payload is unchanged after a break, so a resend puts the block back")
	}

	// And a block placed then removed leaves nothing behind either.
	w.SetBlock(0, 65, 0, cfg.Stone)
	placed := w.Payload(0, 0)
	w.SetBlock(0, 65, 0, cfg.Air)
	if removed := w.Payload(0, 0); bytes.Equal(placed, removed) {
		t.Error("the chunk payload is unchanged after removing a placed block")
	}
}

// The same for a position in a section the flat world fills with one block:
// those are the sections an edit is most likely to be dropped from.
func TestChunkPayloadFollowsABreakDeepUnderground(t *testing.T) {
	cfg := FlatConfig{
		Air: v777.Tables.BlockAir, Bedrock: v777.Tables.BlockBedrock,
		Dirt: v777.Tables.BlockDirt, Grass: v777.Tables.BlockGrass,
		Stone: v777.Tables.BlockStone,
		MinY:  -64, Height: 384, SurfaceY: 64,
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	before := w.Payload(0, 0)
	w.SetBlock(3, 0, 3, cfg.Air) // dig out a block well below the surface
	if after := w.Payload(0, 0); bytes.Equal(before, after) {
		t.Fatal("a break underground does not reach the chunk payload")
	}
}
