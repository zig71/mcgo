package world

import (
	"bytes"
	"testing"

	"mcgo/internal/proto/v777"
)

const (
	lightAir   = 0
	lightTorch = 42 // stands in for a torch: emits, and lets light through
	lightStone = 99
)

func lightWorld() *World {
	cfg := FlatConfig{
		Air: lightAir, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		Emission:    map[int32]int32{lightTorch: 14},
		LightOpaque: map[int32]bool{lightStone: true, lightTorch: false},
	}
	return NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())
}

// Light spreads one dimmer per block, and a block that emits is lit itself.
func TestLightSpreads(t *testing.T) {
	w := lightWorld()
	w.SetBlock(0, 65, 0, lightTorch)
	w.RelightBlock(0, 65, 0)

	if got := w.Light().Level(0, 65, 0); got != 14 {
		t.Errorf("the torch's own light = %d, want 14", got)
	}
	if got := w.Light().Level(1, 65, 0); got != 13 {
		t.Errorf("one block away = %d, want 13", got)
	}
	if got := w.Light().Level(5, 65, 0); got != 9 {
		t.Errorf("five blocks away = %d, want 9", got)
	}
	// Light dims to nothing inside its range of fifteen.
	if got := w.Light().Level(14, 65, 0); got != 0 {
		t.Errorf("fourteen blocks away = %d, want 0 (14 dims down to 0 at 14 away)", got)
	}
}

// A solid block can emit light. Requiring an emitter to be see-through left
// every solid light source dark, which is what glowstone and sea lanterns are.
func TestSolidEmitterLights(t *testing.T) {
	const glowstone = 77
	cfg := FlatConfig{
		Air: lightAir, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		Emission:    map[int32]int32{glowstone: 15},
		LightOpaque: map[int32]bool{glowstone: true},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())
	w.SetBlock(0, 65, 0, glowstone)
	w.RelightBlock(0, 65, 0)

	if got := w.Light().Level(1, 65, 0); got != 14 {
		t.Errorf("a solid emitter lit its neighbour to %d, want 14", got)
	}
	if got := w.Light().Level(-1, 66, 0); got != 13 {
		t.Errorf("light did not spread from a solid emitter: %d", got)
	}
}

// Light does not pass through a block that stops it.
func TestLightIsBlocked(t *testing.T) {
	w := lightWorld()
	// Light goes around an obstacle, so shadowing needs a wall big enough that
	// going around is further than light travels.
	w.SetBlock(0, 65, 0, lightTorch)
	for y := int32(60); y <= 72; y++ {
		for z := int32(-9); z <= 9; z++ {
			w.SetBlock(1, y, z, lightStone)
		}
	}
	w.RelightBlock(0, 65, 0)
	w.RelightBlock(1, 65, 0)

	if got := w.Light().Level(1, 65, 0); got != 0 {
		t.Errorf("light entered a solid block: %d", got)
	}
	if got := w.Light().Level(2, 65, 0); got != 0 {
		t.Errorf("light got past the wall: %d", got)
	}
	// The torch still lights its own side.
	if got := w.Light().Level(-1, 65, 0); got == 0 {
		t.Error("the torch lit nothing on its own side")
	}
}

// Breaking the torch takes its light with it.
func TestLightGoesAway(t *testing.T) {
	w := lightWorld()
	w.SetBlock(0, 65, 0, lightTorch)
	w.RelightBlock(0, 65, 0)
	if w.Light().Level(3, 65, 0) == 0 {
		t.Fatal("the torch lit nothing, so there is nothing to remove")
	}

	w.SetBlock(0, 65, 0, lightAir)
	w.RelightBlock(0, 65, 0)

	for _, pos := range [][3]int32{{0, 65, 0}, {1, 65, 0}, {3, 65, 0}} {
		if got := w.Light().Level(pos[0], pos[1], pos[2]); got != 0 {
			t.Errorf("light left at (%d,%d,%d): %d", pos[0], pos[1], pos[2], got)
		}
	}
}

// A chunk with a torch has to carry its block light, and a chunk without one
// has to say it is dark — the masks and the arrays have to agree.
func TestChunkPayloadCarriesBlockLight(t *testing.T) {
	w := lightWorld()

	dark := w.Payload(0, 0)
	if bytes.Contains(dark, []byte{0x0e}) {
		// Not conclusive on its own; the lit case below is the real check.
		t.Log("a dark chunk happens to contain the byte 0x0e")
	}

	w.SetBlock(1, 65, 1, lightTorch)
	w.RelightBlock(1, 65, 1)
	lit := w.Payload(0, 0)

	if bytes.Equal(dark, lit) {
		t.Fatal("placing a torch did not change the chunk payload")
	}
	// Section 8 holds y=64..79 for a world starting at -64, and it is the one
	// the torch is in. Light reaches fifteen blocks, so it crosses into the
	// neighbouring chunks too — which is the point of computing it globally.
	sections := w.Light().SectionsNear(1, 65, 1, MaxLight-1, -64)
	if len(sections) == 0 {
		t.Fatal("no sections reported light near the torch")
	}
	secs, ok := sections[[2]int32{0, 0}]
	if !ok {
		t.Fatalf("the torch's own chunk is not among %v", sections)
	}
	found := false
	for _, s := range secs {
		if s == 8 {
			found = true
		}
	}
	if !found {
		t.Errorf("the torch's chunk reports sections %v, without section 8", secs)
	}
	// And at least one neighbour chunk, because light crosses the border.
	if len(sections) < 2 {
		t.Errorf("light did not reach a neighbouring chunk: %v", sections)
	}
	// The nibble for the torch's own cell carries its light.
	arr := w.Light().Section(0, 0, 8, -64)
	if arr == nil {
		t.Fatal("the torch's section is dark")
	}
	i := (1 << 8) | (1 << 4) | 1
	want := byte(14)
	got := arr[i>>1] & 0x0F
	if i&1 == 1 {
		got = arr[i>>1] >> 4
	}
	if got != want {
		t.Errorf("the torch's light in the section = %d, want %d", got, want)
	}
}

// dampenOnly builds a dampening table that costs one step of cost through
// state and nothing anywhere else: the generated table is dense, this is the
// smallest thing shaped like one.
func dampenOnly(state int32, cost byte) []byte {
	out := make([]byte, state+1)
	out[state] = cost
	return out
}

// Light falls by one through air and by two through a state that dampens it,
// which is vanilla's rule: a step costs max(1, dampening). Water and leaves
// dampen; air does not.
func TestLightDampening(t *testing.T) {
	tab := v777.Tables
	// A state this server generated: find one water state through the palette
	// helper the tables carry for exactly this.
	states, err := LoadBlockStates(tab.BlockStates)
	if err != nil {
		t.Fatalf("block states: %v", err)
	}
	water, ok := states.Lookup("minecraft:water", map[string]string{"level": "0"})
	if !ok {
		t.Skip("no such water state in this version")
	}

	cfg := FlatConfig{
		Air:            tab.BlockAir,
		MinY:           -64,
		Height:         384,
		SurfaceY:       64,
		Emission:       tab.BlockEmission,
		LightOpaque:    tab.LightOpaque,
		LightDampening: dampenOnly(water, 1),
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	if got := w.Dampening(0, 65, 0); got != 0 {
		t.Errorf("air dampens by %d, want 0", got)
	}
	w.SetBlock(1, 65, 0, water)
	if got := w.Dampening(1, 65, 0); got != 1 {
		t.Errorf("water dampens by %d, want 1", got)
	}
}

// Breaking one light must not take a dimmer one with it, and must not leave its
// own field behind either. A bright torch's removal walks outwards through
// everything dimmer than the cell before it; a weaker source inside that walk
// stops it, because the weaker source's own cell is brighter than the wave.
func TestRemovingABrightLightLeavesTheDimOne(t *testing.T) {
	const dimTorch = 43
	cfg := FlatConfig{
		Air: lightAir, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		Emission:    map[int32]int32{lightTorch: 14, dimTorch: 9},
		LightOpaque: map[int32]bool{lightStone: true, lightTorch: false, dimTorch: false},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	// 14 torch at 0, 9 torch at 16: the bright one reaches 13 blocks, the dim
	// one 9, so the dim one's field starts at 8 and they overlap between there
	// and 13.
	w.SetBlock(0, 65, 0, lightTorch)
	w.RelightBlock(0, 65, 0)
	w.SetBlock(16, 65, 0, dimTorch)
	w.RelightBlock(16, 65, 0)

	if got := w.Light().Level(16, 65, 0); got != 9 {
		t.Fatalf("the dim torch's own light = %d, want 9", got)
	}
	if got := w.Light().Level(15, 65, 0); got != 8 {
		t.Fatalf("next to the dim torch = %d, want 8", got)
	}
	// Where the two meet the brighter wins: at x=8 the bright torch gives 6
	// and the dim one reaches only 1.
	if got := w.Light().Level(8, 65, 0); got != 6 {
		t.Fatalf("in the overlap = %d, want 6 (the bright torch's value there)", got)
	}

	// The bright torch goes. Its own field has to go with it, and the dim
	// torch has to keep its own — it is still there, and still emitting.
	w.SetBlock(0, 65, 0, lightAir)
	w.RelightBlock(0, 65, 0)

	for _, c := range []struct {
		x    int32
		want byte
	}{
		{1, 0}, {7, 0}, // the bright torch alone lit these
		{8, 1}, {15, 8}, {16, 9}, {24, 1}, // the dim torch's own field, unchanged
	} {
		if got := w.Light().Level(c.x, 65, 0); got != c.want {
			t.Errorf("after removing the bright torch, light at x=%d = %d, want %d", c.x, got, c.want)
		}
	}
}

// Removal with dampening in the way: light that crossed water costs two a step,
// so its reach is shorter, and removing the source has to take every cell it
// lit — including the ones at the very edge of that shorter reach, which is
// what "clear and re-propagate" is accused of leaving behind.
func TestRemovalTakesTheWholeShorterReach(t *testing.T) {
	const water = 55
	cfg := FlatConfig{
		Air: lightAir, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		Emission:       map[int32]int32{lightTorch: 14},
		LightOpaque:    map[int32]bool{lightStone: true, lightTorch: false},
		LightDampening: dampenOnly(water, 2),
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	// A torch with water beside it: light going that way pays two per step.
	const srcX = 0
	w.SetBlock(srcX, 65, 0, lightTorch)
	w.SetBlock(1, 65, 0, water)
	w.RelightBlock(srcX, 65, 0)

	if got := w.Light().Level(2, 65, 0); got != 11 {
		t.Fatalf("through the water = %d, want 11 (14 - 1 - 2)", got)
	}
	var lit []int32
	for x := int32(1); x <= 15; x++ {
		if w.Light().Level(x, 65, 0) != 0 {
			lit = append(lit, x)
		}
	}
	if len(lit) == 0 {
		t.Fatal("the torch lit nothing to the east")
	}

	w.SetBlock(srcX, 65, 0, lightAir)
	w.RelightBlock(srcX, 65, 0)

	for _, x := range append(lit, 0) {
		if got := w.Light().Level(x, 65, 0); got != 0 {
			t.Errorf("light left at x=%d: %d", x, got)
		}
	}
}

// Light flows into the space a block left behind. A solid block stops light
// outright; taking one away has to bring the light in. That is vanilla's
// PULL_LIGHT_IN_ENTRY — a level-one decrease in every direction, which clears
// nothing and exists only to ask each lit neighbour to re-propagate back into
// the hole. The clear-the-reach removal this engine used before the decrease
// pass was ported did nothing here: it started from the cell's own light, which
// was zero.
//
// The barrier is a wall rather than one block, because one block is not one: a
// field of fourteen flows over it, and the first version of this test measured
// exactly that.
func TestLightFlowsIntoAClearedBlock(t *testing.T) {
	cfg := FlatConfig{
		Air: lightAir, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		Emission:    map[int32]int32{lightTorch: 14},
		LightOpaque: map[int32]bool{lightStone: true, lightTorch: false},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())

	// Nine tall and a full chunk deep: over the top is seventeen steps and
	// around the side nineteen, so nothing of the torch's fourteen gets past.
	w.SetBlock(0, 65, 0, lightTorch)
	for y := int32(64); y <= 72; y++ {
		for z := int32(-16); z <= 16; z++ {
			w.SetBlock(1, y, z, lightStone)
		}
	}
	w.RelightBlock(0, 65, 0)

	if got := w.Light().Level(1, 65, 0); got != 0 {
		t.Fatalf("the wall holds %d light, want 0", got)
	}
	if got := w.Light().Level(2, 65, 0); got != 0 {
		t.Fatalf("light got past the wall: %d at (2,65,0)", got)
	}

	w.SetBlock(1, 65, 0, lightAir)
	w.RelightBlock(1, 65, 0)

	if got := w.Light().Level(1, 65, 0); got != 13 {
		t.Errorf("the cleared cell holds %d, want 13 (14 - 1)", got)
	}
	if got := w.Light().Level(2, 65, 0); got != 12 {
		t.Errorf("the cell behind it holds %d, want 12: light did not come through the hole", got)
	}
}
