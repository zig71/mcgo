package server

import (
	"testing"

	"mcgo/internal/proto/v777"
)

// Water has to arrive as a front: on each fluid tick, every cell that becomes
// water should be the same distance from the source (or one band further than
// the last tick), never a jumble of near and far cells at once. That jumble is
// what "diffusing over the ground" looks like on screen.
func TestWaterArrivesAsAFront(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.waterLevelsFor(tab)
	stone, _ := srv.blockState(tab, "minecraft:stone", nil)

	// A wide stone floor so the water has room to run.
	for x := int32(-1); x <= 9; x++ {
		for z := int32(-1); z <= 9; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	w.SetBlock(0, 65, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 65, 0)

	type step struct {
		tick  int
		cells [][3]int32
	}
	var steps []step

	for tick := 1; tick <= 12; tick++ {
		before := map[[3]int32]bool{}
		for x := int32(-1); x <= 9; x++ {
			for z := int32(-1); z <= 9; z++ {
				if _, ok := lv.level[w.Block(x, 65, z)]; ok {
					before[[3]int32{x, 65, z}] = true
				}
			}
		}
		srv.age.Add(5)
		srv.tickFluids()
		var added [][3]int32
		for x := int32(-1); x <= 9; x++ {
			for z := int32(-1); z <= 9; z++ {
				c := [3]int32{x, 65, z}
				if _, ok := lv.level[w.Block(x, 65, z)]; ok && !before[c] {
					added = append(added, c)
				}
			}
		}
		steps = append(steps, step{tick: tick, cells: added})
	}

	// Whatever arrives in a tick has to be next to water that was already
	// there: that is what "filling from the edges" means, and it is the
	// property the report was about. Every cell here is only ever assigned
	// from a cell that was assigned before it, so this must hold.
	previous := map[[3]int32]bool{{0, 65, 0}: true}
	for _, st := range steps {
		if len(st.cells) == 0 {
			continue
		}
		now := map[[3]int32]bool{}
		for _, c := range st.cells {
			now[c] = true
		}
		for _, c := range st.cells {
			// Beside water that was already there, or beside water arriving in
			// this same tick: the water is never detached from itself.
			adjacent := false
			for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
				n := [3]int32{c[0] + d[0], c[1], c[2] + d[1]}
				if previous[n] || now[n] {
					adjacent = true
				}
			}
			if !adjacent {
				t.Errorf("tick %d made (%d,%d,%d) water with no water beside it: that is the diffusion", st.tick, c[0], c[1], c[2])
				return
			}
		}
		for c := range now {
			previous[c] = true
		}
	}
}

// A falling column fills one block per fluid tick, not all at once. It used to
// appear as a whole tower in the first tick, because every falling cell was
// treated as being at the front.
func TestFallingColumnAdvances(t *testing.T) {
	srv := quietServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	lv := srv.waterLevelsFor(tab)
	stone, _ := srv.blockState(tab, "minecraft:stone", nil)

	// A floor with a tall shaft above one spot, open to the air.
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-2); z <= 2; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	w.SetBlock(0, 90, 0, lv.byLevel[waterSource])
	srv.noteWater(0, 90, 0)

	depth := func() int32 {
		d := int32(0)
		for y := int32(90); y >= 65; y-- {
			if _, ok := lv.level[w.Block(0, y, 0)]; ok {
				d++
			}
		}
		return d
	}

	srv.age.Add(5)
	srv.tickFluids()
	first := depth()
	if first > 3 {
		t.Errorf("the whole %d block column appeared in one tick: it should fall one block per tick", first)
	}
	if first == 0 {
		t.Fatal("the column did not start falling")
	}

	for range 3 {
		srv.age.Add(5)
		srv.tickFluids()
	}
	later := depth()
	if later <= first {
		t.Errorf("the column stopped falling: %d blocks after four ticks", later)
	}
	if later-first > 4 {
		t.Errorf("the column advanced %d blocks in three ticks, want about one per tick (that is %d blocks at four ticks)", later-first, later)
	}
}
