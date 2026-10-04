package world

import "testing"

// The flat world has to be solid all the way down. It used to be a four-block
// slab with void beneath, so any hole — dug, or blown by TNT — dropped whatever
// fell into it straight out of the world.
func TestFlatWorldIsDeep(t *testing.T) {
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3, Stone: 4,
		MinY: -64, Height: 384, SurfaceY: 64,
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	if got := w.Block(0, 64, 0); got != cfg.Grass {
		t.Errorf("the surface is %d, want grass", got)
	}
	if got := w.Block(0, 63, 0); got != cfg.Dirt {
		t.Errorf("under the surface is %d, want dirt", got)
	}
	if got := w.Block(0, 40, 0); got != cfg.Stone {
		t.Errorf("deep down is %d, want stone", got)
	}
	if got := w.Block(0, int32(cfg.MinY), 0); got != cfg.Bedrock {
		t.Errorf("the bottom is %d, want bedrock", got)
	}
	if got := w.Block(0, 65, 0); got != cfg.Air {
		t.Errorf("above the surface is %d, want air", got)
	}

	// No holes anywhere in the column.
	for y := int32(cfg.MinY); y <= int32(cfg.SurfaceY); y++ {
		if w.Block(0, y, 0) == cfg.Air {
			t.Fatalf("air at y=%d: the world has a hole in it", y)
		}
	}
}

// EditsInChunk counts the player's recorded changes inside a chunk, which is
// what decides whether a chunk that is being sent is carrying player work — and
// so whether the log line that hashes it appears at all. That log is what a
// ghost-block report is read with, so the count has to mean what it says.
//
// It counts *changes*, not differences: a block put back the way it was is
// still a recorded change, because the world keeps what the player did rather
// than what differs from the generated world.
func TestEditsInChunk(t *testing.T) {
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3, Stone: 4,
		MinY: -64, Height: 384, SurfaceY: 64,
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	if got := w.EditsInChunk(0, 0); got != 0 {
		t.Fatalf("a fresh world has %d edits in (0,0), want 0", got)
	}

	w.SetBlock(1, 65, 1, cfg.Dirt)
	w.SetBlock(2, 65, 1, cfg.Dirt)
	w.SetBlock(20, 65, 1, cfg.Dirt) // one chunk over

	if got := w.EditsInChunk(0, 0); got != 2 {
		t.Errorf("chunk (0,0) holds %d edits, want 2", got)
	}
	if got := w.EditsInChunk(1, 0); got != 1 {
		t.Errorf("chunk (1,0) holds %d edits, want 1", got)
	}
	if got := w.EditsInChunk(3, 3); got != 0 {
		t.Errorf("an untouched chunk holds %d edits, want 0", got)
	}
	// Chunk coordinates are per axis: the same x with a different z is a
	// different chunk.
	w.SetBlock(1, 65, 20, cfg.Dirt)
	if got := w.EditsInChunk(0, 1); got != 1 {
		t.Errorf("chunk (0,1) holds %d edits, want 1", got)
	}

	// Putting a block back does not un-record the change.
	w.SetBlock(1, 65, 1, cfg.Air)
	if got := w.EditsInChunk(0, 0); got != 2 {
		t.Errorf("chunk (0,0) holds %d edits after a block was put back, want 2", got)
	}
}
