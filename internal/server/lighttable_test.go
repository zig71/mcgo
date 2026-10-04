package server

import (
	"testing"

	"mcgo/internal/proto/v777"
)

// The light cost of a state comes from the game's own dump: leaves and cobwebs
// dim a step by one, stone and tinted glass stop it, air costs nothing. The data
// generator's block report cannot answer this — it is computed from per-state
// shapes — which is why the table is dumped by running the jar
// (tools/dump_light/DumpLight.java, packed by tools/gen_light.py).
func TestLightDampeningComesFromTheGeneratedTable(t *testing.T) {
	tab := v777.Tables
	if len(tab.LightDampening) == 0 {
		t.Fatal("26.3 has no dampening table")
	}
	srv := fluidServer()
	w := srv.worldFor(tab)
	leaves, ok := srv.blockState(tab, "minecraft:oak_leaves", nil)
	if !ok {
		t.Fatal("no leaves block state")
	}
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	w.SetBlock(0, 65, 0, stone)
	w.SetBlock(1, 65, 0, leaves)
	if got := w.Dampening(0, 65, 0); got != 15 {
		t.Errorf("stone damps a step by %d, want 15", got)
	}
	// Leaves used to be opaque to this engine, so light stopped at a tree. The
	// table says a step through them costs one.
	if got := w.Dampening(1, 65, 0); got != 1 {
		t.Errorf("leaves damp a step by %d, want 1", got)
	}
	if got := w.Dampening(2, 65, 0); got != 0 {
		t.Errorf("air damps a step by %d, want 0", got)
	}
}

// Emission comes from the same dump. A lit furnace is the case that matters: it
// emits only in its lit state, and the hand-written table this replaced had 291
// emitting states missing, 58 that emit nothing in the game, and 47 at the wrong
// level — which is what a player sees as a light source that does not light.
func TestLightEmissionComesFromTheGeneratedTable(t *testing.T) {
	tab := v777.Tables
	if len(tab.LightEmission) == 0 {
		t.Fatal("26.3 has no emission table")
	}
	srv := fluidServer()
	w := srv.worldFor(tab)
	lit, ok := srv.blockState(tab, "minecraft:furnace", map[string]string{"facing": "north", "lit": "true"})
	if !ok {
		t.Fatal("no lit furnace state")
	}
	w.SetBlock(0, 65, 0, lit)
	want := tab.LightEmission[lit]
	if want == 0 {
		t.Fatal("the dump says a lit furnace emits nothing")
	}
	if got := w.Emission(0, 65, 0); got != want {
		t.Errorf("a lit furnace emits %d, want the table's %d", got, want)
	}
	if got := w.Emission(0, 66, 0); got != 0 {
		t.Errorf("air emits %d, want 0", got)
	}
}
