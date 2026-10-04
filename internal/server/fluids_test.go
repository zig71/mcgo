package server

import (
	"testing"

	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// fluidServer is a server with a world but no connections, so water can be
// ticked without any of the sending.
//
// It goes through New, so it has the same defaults a real server does — the
// game rules, the scoreboard, the entity store. Building a Server by hand left
// those at their zero values three times over: every rule false (so nothing
// dropped and the clock was frozen), every player in survival, and a nil
// scoreboard.
func fluidServer() *Server { return withFluidCells(New(Config{})) }

// quietServer is fluidServer with the autosave and tick loops off. A test that
// drives ticks by hand must not have the server ticking them at the same time,
// or a pass is stolen from the fluid queue at an unpredictable moment and the
// result depends on timing — which is how the drain test flaked.
func quietServer() *Server { return withFluidCells(New(Config{NoBackground: true})) }

// withFluidCells gives a test server the newest tables and the fluid queues.
func withFluidCells(srv *Server) *Server {
	srv.newest = v777.Tables
	srv.fluidCells = map[[3]int32]bool{}
	srv.lavaCells = map[[3]int32]bool{}
	return srv
}

func TestWaterSpreadsFromASource(t *testing.T) {
	srv := quietServer()
	w := srv.worldFor(v777.Tables)
	lv := srv.waterLevelsFor(v777.Tables)
	if lv == nil {
		t.Fatal("no water block states")
	}

	// A source on the ground, with air beside it and a solid block below.
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)

	for range 3 {
		srv.tickFluids()
	}

	east := w.Block(1, 65, 0)
	level, ok := lv.level[east]
	if !ok {
		t.Fatalf("nothing spread east: state %d", east)
	}
	if level == waterSource || level > waterMaxSpread {
		t.Errorf("spread water has level %d, want between 1 and %d", level, waterMaxSpread)
	}
	if got := lv.level[w.Block(0, 65, 0)]; got != waterSource {
		t.Errorf("the source is no longer a source (level %d)", got)
	}
}

// A source falls *and* spreads along the surface it sits on, while falling
// water keeps to its own column: pour into a one-wide shaft and the shaft's
// walls stay dry.
func TestWaterFallsInAColumn(t *testing.T) {
	srv := quietServer()
	w := srv.worldFor(v777.Tables)
	lv := srv.waterLevelsFor(v777.Tables)
	stone, ok := srv.blockState(v777.Tables, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}

	// A one-wide shaft three blocks tall: walls around the source, so the only
	// way out is down. The flat world's floor is at y=64, so the water lands at
	// y=65 and floods there — which is what water does.
	w.SetBlock(0, 68, 0, lv.byLevel[waterSource])
	for _, d := range [4][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}} {
		w.SetBlock(d[0], 68, d[2], stone)
	}
	srv.noteWater(0, 68, 0)
	for range 4 {
		srv.tickFluids()
	}

	// It fell the whole way down.
	for _, y := range []int32{67, 66, 65} {
		if _, ok := lv.level[w.Block(0, y, 0)]; !ok {
			t.Fatalf("the water did not fall to y=%d", y)
		}
	}
	// Mid-column it keeps to itself.
	for _, y := range []int32{67, 66} {
		for _, d := range [4][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}} {
			if _, isWater := lv.level[w.Block(d[0], y, d[2])]; isWater {
				t.Errorf("the column leaked sideways at (%d,%d,%d)", d[0], y, d[2])
			}
		}
	}
	// Where it landed, it spreads.
	if _, ok := lv.level[w.Block(1, 65, 0)]; !ok {
		t.Error("the water did not spread where it landed")
	}
}

// Removing the source lets the water it fed drain away.
func TestWaterDrainsWhenTheSourceGoes(t *testing.T) {
	srv := quietServer()
	w := srv.worldFor(v777.Tables)
	lv := srv.waterLevelsFor(v777.Tables)

	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)
	for range 3 {
		srv.tickFluids()
	}
	if _, ok := lv.level[w.Block(1, 65, 0)]; !ok {
		t.Fatal("the water never spread, so there is nothing to drain")
	}

	// Take the source away, as breaking a block would.
	w.SetBlock(0, 65, 0, v777.Tables.BlockAir)
	srv.enqueueFluids(0, 65, 0)
	// The flow thins and recedes rather than vanishing: each pass drops a cell's
	// level by two as it loses the neighbour that fed it, so the water is gone a
	// handful of passes later. The harness runs no tick loop, so these passes are
	// the only ones.
	for range 20 {
		srv.tickFluids()
	}

	if state := w.Block(1, 65, 0); state != v777.Tables.BlockAir {
		level, _ := lv.level[state]
		t.Errorf("the flowed water is still there (level %d, state %d)", level, state)
	}
}

// Water spreads outwards one block per fluid tick, rather than the whole pool
// appearing at once: that is what the front is for.
func TestWaterFlowsOutwards(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.waterLevelsFor(tab)
	if lv == nil {
		t.Fatal("no water levels for this version")
	}
	// A stone floor with air above it.
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for x := int32(-2); x <= 4; x++ {
		for z := int32(-2); z <= 2; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)

	srv.tickFluids()
	if got := w.Block(1, 65, 0); got == tab.BlockAir {
		t.Error("the first step of the front did not appear")
	}
	if got := w.Block(3, 65, 0); got != tab.BlockAir {
		t.Error("water reached three blocks out on the first tick: the front is not being held back")
	}

	for range 2 {
		srv.tickFluids()
	}
	if got := w.Block(3, 65, 0); got == tab.BlockAir {
		t.Error("the front did not reach three blocks after three ticks")
	}
	if got := w.Block(6, 65, 0); got != tab.BlockAir {
		t.Error("water reached six blocks out, which is further than the front should have travelled")
	}
}

// Creative does not use things up: a water bucket stays full.
func TestCreativeBucketIsNotUsed(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	bucket, ok := tab.ItemIDs["minecraft:water_bucket"]
	if !ok {
		t.Fatal("no water bucket in the item registry")
	}
	empty, ok := tab.ItemIDs["minecraft:bucket"]
	if !ok {
		t.Fatal("no empty bucket in the item registry")
	}
	held := 27 + s.heldSlot

	// Creative: untouched.
	s.inv[held] = world.Item{ID: bucket, Count: 1}
	s.consumeHeldItem(bucket, empty)
	if s.inv[held].ID != bucket || s.inv[held].Count != 1 {
		t.Errorf("creative used the bucket up: %+v", s.inv[held])
	}

	// Survival: it becomes the empty one.
	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	s.inv[held] = world.Item{ID: bucket, Count: 1}
	s.consumeHeldItem(bucket, empty)
	if s.inv[held].ID != empty {
		t.Errorf("survival left %+v, want an empty bucket", s.inv[held])
	}
}

// Lava spreads three blocks from its source and no further, and it does so on
// its own, much slower, schedule.
func TestLavaSpread(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.lavaLevelsFor(tab)
	if lv == nil {
		t.Fatal("no lava levels for this version")
	}
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for x := int32(-1); x <= 6; x++ {
		for z := int32(-1); z <= 1; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)

	// Lava ticks every 30: five water ticks go by with nothing happening.
	for range 5 {
		srv.age.Add(5)
		srv.tickFluids()
	}
	if got := w.Block(1, 65, 0); got != tab.BlockAir {
		t.Error("lava moved on the water's schedule")
	}

	// Enough lava ticks to settle.
	for range 20 {
		srv.age.Add(5)
		srv.tickFluids()
	}
	if got := w.Block(3, 65, 0); got == tab.BlockAir {
		t.Error("lava did not reach three blocks")
	}
	if got := w.Block(5, 65, 0); got != tab.BlockAir {
		t.Error("lava ran further than three blocks")
	}
}

// fluidWorld is a fresh server over a stone floor at y=64. Each case below
// takes one: the fluids of one case spread into the next otherwise, and water
// reaching a later case's lava turns it to obsidian before its own tick ever
// runs — which is exactly how this test first failed.
func fluidWorld(t *testing.T) (*Server, proto.Tables, *world.World) {
	t.Helper()
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for x := int32(-1); x <= 5; x++ {
		for z := int32(-3); z <= 3; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	return srv, tab, w
}

// Water and lava react where they meet, and the three solids vanilla makes are
// three different rules: the block that reacts to water at its side is the LAVA
// — obsidian when it is a source, cobblestone when it flows (LiquidBlock
// .shouldSpreadLiquid) — while water UNDER lava is what a fall sets into STONE
// (LavaFluid.spreadTo). Water below a lava block is deliberately not one of the
// sides the reaction looks at, or the rule that makes stone would never run.
//
// The reaction fires when a fluid is written next to the other, which is what
// LiquidBlock.neighborChanged does, so the second fluid in each case is placed
// with applyFluid — the same call the flow engine makes — not straight into the
// world.
func TestWaterAndLavaReact(t *testing.T) {
	// A lava source with water poured beside it: obsidian.
	srv, tab, w := fluidWorld(t)
	lava, water := srv.lavaLevelsFor(tab), srv.waterLevelsFor(tab)
	srv.applyFluid(tab, w, 0, 65, 0, lava.byLevel[waterSource])
	srv.applyFluid(tab, w, 1, 65, 0, water.byLevel[waterSource])
	if name, _, _ := srv.blockStateName(tab, w.Block(0, 65, 0)); name != "minecraft:obsidian" {
		t.Errorf("a lava source reached by water is %q, want obsidian", name)
	}

	// Flowing lava with water poured beside it: cobblestone, not obsidian.
	srv, tab, w = fluidWorld(t)
	lava, water = srv.lavaLevelsFor(tab), srv.waterLevelsFor(tab)
	srv.applyFluid(tab, w, 0, 65, 0, lava.byLevel[1])
	srv.applyFluid(tab, w, 1, 65, 0, water.byLevel[waterSource])
	if name, _, _ := srv.blockStateName(tab, w.Block(0, 65, 0)); name != "minecraft:cobblestone" {
		t.Errorf("flowing lava reached by water is %q, want cobblestone", name)
	}

	// Water above a flow counts as reaching it.
	srv, tab, w = fluidWorld(t)
	lava, water = srv.lavaLevelsFor(tab), srv.waterLevelsFor(tab)
	srv.applyFluid(tab, w, 0, 65, 0, lava.byLevel[1])
	srv.applyFluid(tab, w, 0, 66, 0, water.byLevel[waterSource])
	if name, _, _ := srv.blockStateName(tab, w.Block(0, 65, 0)); name != "minecraft:cobblestone" {
		t.Errorf("flowing lava with water above it is %q, want cobblestone", name)
	}

	// Water below lava is not a side the reaction reads, so pouring it leaves
	// the lava alone; the lava's own tick then sets that water into stone.
	srv, tab, w = fluidWorld(t)
	lava, water = srv.lavaLevelsFor(tab), srv.waterLevelsFor(tab)
	srv.applyFluid(tab, w, 3, 65, 0, lava.byLevel[waterSource])
	srv.applyFluid(tab, w, 3, 64, 0, water.byLevel[waterSource])
	if name, _, _ := srv.blockStateName(tab, w.Block(3, 65, 0)); name != "minecraft:lava" {
		t.Errorf("lava with water below it is %q, want lava: below is the fall case, not a side of the reaction", name)
	}
	for range 20 {
		srv.age.Add(5)
		srv.tickFluids()
	}
	if name, _, _ := srv.blockStateName(tab, w.Block(3, 64, 0)); name != "minecraft:stone" {
		t.Errorf("water under lava is %q, want stone", name)
	}
}

// A current carries whatever is in it: an item dropped into a flow drifts
// downhill, and one sitting in a still source stays where it was put. Without
// the current, a stream moved the blocks around an entity and not the entity.
func TestFluidCurrentCarriesEntities(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.waterLevelsFor(tab)
	if lv == nil {
		t.Fatal("no water block states")
	}
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone")
	}
	for x := int32(-16); x <= 16; x++ {
		w.SetBlock(x, 64, 0, stone)
	}
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)
	for range 5 {
		srv.tickFluids()
	}
	if _, ok := lv.level[w.Block(1, 65, 0)]; !ok {
		t.Fatalf("no water spread east of the source: state %d", w.Block(1, 65, 0))
	}

	// Downhill from the source the flow points east, so the item goes east.
	e := &Entity{Kind: kindItem, X: 1.5, Y: 65, Z: 0.5}
	for range 10 {
		srv.stepEntity(tab, w, e)
	}
	if e.X <= 1.5 {
		t.Errorf("the current did not carry the item east: x = %.3f", e.X)
	}

	// The source itself is level on every side, so nothing pushes off it.
	still := &Entity{Kind: kindItem, X: 0.5, Y: 65, Z: 0.5}
	for range 10 {
		srv.stepEntity(tab, w, still)
	}
	if still.X < 0.49 || still.X > 0.51 || still.Z < 0.49 || still.Z > 0.51 {
		t.Errorf("a source carried the item: %.3f, %.3f", still.X, still.Z)
	}
}

// Two sources with a block between them fill that block with a source of its
// own: vanilla's source conversion, which is what makes a pool infinite. Without
// it the gap stays flowing water, so the pool shrinks when a source is taken
// away instead of staying full.
func TestWaterSourcesFillTheGapBetweenThem(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.waterLevelsFor(tab)
	if lv == nil {
		t.Fatal("no water block states")
	}
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone")
	}
	for x := int32(-8); x <= 8; x++ {
		w.SetBlock(x, 64, 0, stone)
	}
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	w.SetBlock(2, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)
	srv.noteWater(2, 65, 0)
	for range 12 {
		srv.tickFluids()
	}
	level, ok := lv.level[w.Block(1, 65, 0)]
	if !ok {
		t.Fatalf("nothing filled the gap: state %d", w.Block(1, 65, 0))
	}
	if level != waterSource {
		t.Errorf("the block between two sources is level %d, want a source", level)
	}
}
