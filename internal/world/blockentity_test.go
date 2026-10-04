package world

import (
	"bytes"
	"testing"
)

// A chunk has to list a block entity for every block that has one. The client
// draws nothing where a block entity is missing — an invisible chest — so this
// is what keeps a chest placed in an earlier session visible.
func TestChunkBlockEntities(t *testing.T) {
	const chestState, chestType = 42, 7
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		SignBlockEntityType: 8,
		BlockEntityState:    map[int32]BlockEntityType{chestState: {ID: chestType, Name: "minecraft:chest"}},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())
	w.SetBlock(1, 65, 1, chestState)

	bes := w.chunkBlockEntities(0, 0)
	if len(bes) != 1 {
		t.Fatalf("%d block entities, want 1", len(bes))
	}
	got := bes[0]
	if got.X != 1 || got.Y != 65 || got.Z != 1 || got.Type != chestType {
		t.Errorf("block entity = %+v, want a chest at (1,65,1)", got)
	}
	if !bytes.Contains(got.NBT, []byte("minecraft:chest")) {
		t.Errorf("the block entity's NBT does not name it: %x", got.NBT)
	}

	// A block without a block entity is not listed.
	w.SetBlock(2, 65, 1, 99)
	if n := len(w.chunkBlockEntities(0, 0)); n != 1 {
		t.Errorf("%d block entities after placing a plain block, want 1", n)
	}

	// Neither is one in another chunk.
	w.SetBlock(40, 65, 1, chestState)
	if n := len(w.chunkBlockEntities(0, 0)); n != 1 {
		t.Errorf("%d block entities, want only this chunk's", n)
	}
	if n := len(w.chunkBlockEntities(2, 0)); n != 1 {
		t.Errorf("the neighbouring chunk lists %d, want 1", n)
	}
}

// The block entity has to reach the chunk packet, not just the helper.
func TestChunkPayloadCarriesBlockEntities(t *testing.T) {
	const chestState, chestType = 42, 7
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		SignBlockEntityType: 8,
		BlockEntityState:    map[int32]BlockEntityType{chestState: {ID: chestType, Name: "minecraft:chest"}},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())
	w.SetBlock(1, 65, 1, chestState)
	w.SetBlock(3, 65, 1, chestState)

	payload := w.Payload(0, 0)
	if !bytes.Contains(payload, []byte("minecraft:chest")) {
		t.Error("the chunk payload does not name the chest")
	}
	if n := bytes.Count(payload, []byte("minecraft:chest")); n != 2 {
		t.Errorf("the chunk payload names %d chests, want 2", n)
	}
}

// A sign is sent with its text, from the sign store, and must not also be
// listed as a bare block entity.
func TestSignsAreNotDuplicated(t *testing.T) {
	const signState, signType = 42, 8
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		SignBlockEntityType: signType,
		BlockEntityState:    map[int32]BlockEntityType{signState: {ID: signType, Name: "minecraft:sign"}},
	}
	signs := NewSigns()
	w := NewWorld(cfg, NewEdits(), signs, NewBanners())
	w.SetBlock(1, 65, 1, signState)
	signs.Put(1, 65, 1, SignText{Front: [4]string{"hello"}})

	bes := w.chunkBlockEntities(0, 0)
	if len(bes) != 1 {
		t.Fatalf("%d block entities for one sign, want 1", len(bes))
	}
	if !bytes.Contains(bes[0].NBT, []byte("hello")) {
		t.Errorf("the sign's text is missing: %x", bes[0].NBT)
	}
}
