package server

import (
	"bytes"
	"testing"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// The wiring, not just the helper: a chest placed by a client has to appear in
// the chunk payload this server actually builds, because that is what makes it
// visible again after a rejoin.
func TestPlacedChestAppearsInTheChunkPayload(t *testing.T) {
	srv := &Server{edits: world.NewEdits(), signs: world.NewSigns()}
	tab := v777.Tables

	worldBE := blockEntityStates(tab)
	if len(worldBE) == 0 {
		t.Fatal("no block states map to a block entity")
	}
	// The state a 26.3 client's chest item places.
	chestState, ok := tab.ItemBlockState[tab.ItemIDs["minecraft:chest"]]
	if !ok {
		t.Fatal("the chest item places no block")
	}
	if _, ok := worldBE[chestState]; !ok {
		t.Fatalf("chest state %d has no block entity in the map", chestState)
	}

	w := world.NewWorld(world.FlatConfig{
		Air: tab.BlockAir, Bedrock: tab.BlockBedrock, Dirt: tab.BlockDirt, Grass: tab.BlockGrass,
		MinY: -64, Height: 384, SurfaceY: 64,
		SignBlockEntityType: tab.SignBlockEntityType,
		BlockEntityState:    worldBE,
	}, srv.edits, srv.signs, world.NewBanners())

	// Place it the way a session does.
	w.SetBlock(1, 65, 1, chestState)

	payload := w.Payload(0, 0)
	if !bytes.Contains(payload, []byte("minecraft:chest")) {
		t.Error("the chunk payload does not carry the chest's block entity")
	}
}
