package world

import (
	"sync"
	"testing"

	"mcgo/internal/proto/v777"
)

// Encoding a chunk and changing a block at the same time is what two players
// doing this on a live server do. It used to deadlock: the encoder held the
// edits lock and then took the light lock, while relighting took the light lock
// and then the edits lock (through the environment callbacks). This test is
// that pair, run enough times to interleave.
func TestConcurrentPayloadAndRelight(t *testing.T) {
	tab := v777.Tables
	w := NewWorld(FlatConfig{
		Air:                 tab.BlockAir,
		Bedrock:             tab.BlockBedrock,
		Dirt:                tab.BlockDirt,
		Grass:               tab.BlockGrass,
		MinY:                -64,
		Height:              384,
		SurfaceY:            64,
		SignBlockEntityType: tab.SignBlockEntityType,
		LightOpaque:         tab.LightOpaque,
		Emission:            tab.BlockEmission,
	}, NewEdits(), NewSigns(), NewBanners())
	// Any block that is not air: the point is a change and a relight.
	stone := tab.BlockDirt

	const rounds = 300
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			w.Payload(0, 0)
			w.LightUpdate(0, 0, []int{4, 5})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			x, z := int32(i%16), int32(i%13)
			w.SetBlock(x, 65, z, stone)
			w.RelightBlock(x, 65, z)
		}
	}()

	wg.Wait()
}
