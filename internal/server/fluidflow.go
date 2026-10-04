package server

import (
	"sort"

	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// The fluid engine proper: one tick of one fluid position, ported from
// FlowingFluid.
//
// The rule that matters is that a cell's level comes from its NEIGHBOURS
// (getNewLiquid), not from how far it is from the source that fed it. That is
// what lets a pool find its own shape as it settles, what makes two sources fill
// the block between them, and what makes the water left behind by a broken dam
// thin out and vanish once nothing feeds it — a spread that only counts distance
// from the original sources can do none of those.

// fluidDropOff is FlowingFluid.getDropOff: what one step costs. Water runs seven
// blocks from a source and lava three, which is 8/(spread+1) — one and two.
func fluidDropOff(lv *fluidLevels) int32 { return 8 / (lv.maxSpread + 1) }

// tickFluid advances every position that was touched since the last pass. Each
// one recomputes its own level and then spreads, which is FlowingFluid.tick.
//
// The positions are sorted so a pass does not depend on map iteration order.
// The engine this replaced reflowed the whole connected pool in map order, which
// made the outcome — and one of the tests — depend on that order.
func (s *Server) tickFluid(t proto.Tables, w *world.World, lv, other *fluidLevels, cells [][3]int32) {
	sort.Slice(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a[1] != b[1] {
			// Lower first: a fall is settled before the pool it feeds.
			return a[1] < b[1]
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return a[2] < b[2]
	})
	for _, c := range cells {
		state := w.Block(c[0], c[1], c[2])
		level, ok := lv.level[state]
		if !ok {
			// Not this fluid any more: a neighbour flows into it, it does not
			// flow itself.
			continue
		}
		if level != waterSource {
			newLevel, empty := s.newLiquid(t, w, lv, c)
			switch {
			case empty:
				s.applyFluid(t, w, c[0], c[1], c[2], t.BlockAir)
				continue
			case newLevel != level:
				s.applyFluid(t, w, c[0], c[1], c[2], lv.byLevel[newLevel])
				level = newLevel
			}
		}
		s.spreadFluid(t, w, lv, other, c, level)
	}
}

// newLiquid is FlowingFluid.getNewLiquid: the level this position should hold,
// read from its neighbours rather than from the parent that fed it. It reports
// empty when nothing feeds the cell any more, which is how a pool drains.
func (s *Server) newLiquid(t proto.Tables, w *world.World, lv *fluidLevels, c [3]int32) (int32, bool) {
	// Levels are stored as distance from the source where vanilla stores the
	// amount, so a level is worth 8-level and a falling cell is a full one.
	amountOf := func(level int32) int32 {
		if level >= waterFalling {
			return 8
		}
		return 8 - level
	}
	highest := int32(0)
	sources := 0
	for _, d := range horizontalOffsets {
		n := [3]int32{c[0] + d[0], c[1], c[2] + d[2]}
		l, ok := lv.level[w.Block(n[0], n[1], n[2])]
		if !ok {
			continue
		}
		if l == waterSource {
			sources++
		}
		if a := amountOf(l); a > highest {
			highest = a
		}
	}
	// Two sources beside a cell, over solid ground or over a source of the same
	// fluid, make it a source: this is what fills the block between two sources.
	// Water only, because LavaFluid.canConvertToSource is false.
	if sources >= 2 && lv.isWater {
		below := [3]int32{c[0], c[1] - 1, c[2]}
		_, belowIsThis := lv.level[w.Block(below[0], below[1], below[2])]
		if belowIsThis || s.isSolid(t, w, below) {
			return waterSource, false
		}
	}
	// The same fluid above makes this a falling column.
	above := [3]int32{c[0], c[1] + 1, c[2]}
	if _, ok := lv.level[w.Block(above[0], above[1], above[2])]; ok {
		return waterFalling, false
	}
	if highest == 0 {
		return 0, true
	}
	// One step costs the drop-off, and the level written is the amount's
	// complement: a source is amount 8, which is level 0.
	amount := highest - fluidDropOff(lv)
	if amount <= 0 {
		return 0, true
	}
	return 8 - amount, false
}

// spreadFluid is FlowingFluid.spread: a fluid flows down if it can and only then
// sideways, so a falling column keeps to itself until it lands. While falling it
// only feeds sideways when it is a source with three or more source neighbours,
// which is vanilla's rule for a wide waterfall.
func (s *Server) spreadFluid(t proto.Tables, w *world.World, lv, other *fluidLevels, c [3]int32, level int32) {
	below := [3]int32{c[0], c[1] - 1, c[2]}
	if s.canFlowInto(t, w, lv, other, below) {
		// LavaFluid.spreadTo: lava that falls into water sets the water into
		// stone and does not flow in. Falling is the one direction the two
		// fluids trade places at all — a fluid may take a water block only from
		// above — and the outcome here is stone, where the block that reacts to
		// water from the side is the lava, and sets into cobblestone or
		// obsidian (lavaReactsToWater).
		if !lv.isWater && other != nil {
			if _, waterBelow := other.level[w.Block(below[0], below[1], below[2])]; waterBelow {
				if stone, ok := s.blockState(t, "minecraft:stone", nil); ok {
					s.applyFluid(t, w, below[0], below[1], below[2], stone)
					s.fizz(t, below[0], below[1], below[2])
				}
				if s.sourceNeighbours(w, lv, c) >= 3 {
					s.spreadToSides(t, w, lv, other, c, level)
				}
				return
			}
		}
		if newLevel, empty := s.newLiquid(t, w, lv, below); !empty {
			s.writeFluid(t, w, lv, other, below, newLevel)
			if s.sourceNeighbours(w, lv, c) >= 3 {
				s.spreadToSides(t, w, lv, other, c, level)
			}
			return
		}
	}
	if level == waterSource || !s.isHole(t, w, lv, c) {
		s.spreadToSides(t, w, lv, other, c, level)
	}
}

// spreadToSides is FlowingFluid.spreadToSides: the amount left after the
// drop-off decides whether anything goes sideways at all, and getSpread decides
// which sides those are. Each side's own level is then read from its neighbours,
// so a cell fed by two flows takes the closer one.
func (s *Server) spreadToSides(t proto.Tables, w *world.World, lv, other *fluidLevels, c [3]int32, level int32) {
	amount := int32(8) - level
	if level >= waterFalling {
		amount = 7 // vanilla spreads a falling cell at full strength
	}
	if amount-fluidDropOff(lv) <= 0 {
		return
	}
	for _, d := range s.spreadDirections(t, w, lv, c) {
		n := [3]int32{c[0] + d[0], c[1] + d[1], c[2] + d[2]}
		if newLevel, empty := s.newLiquid(t, w, lv, n); !empty {
			s.writeFluid(t, w, lv, other, n, newLevel)
		}
	}
}

// canFlowInto reports whether fluid may be written here: air, its own kind, or
// the other fluid, which is how the two meet.
func (s *Server) canFlowInto(t proto.Tables, w *world.World, lv, other *fluidLevels, c [3]int32) bool {
	state := w.Block(c[0], c[1], c[2])
	if state == t.BlockAir {
		return true
	}
	if _, ok := lv.level[state]; ok {
		return true
	}
	if other != nil {
		if _, ok := other.level[state]; ok {
			return true
		}
	}
	return false
}

// sourceNeighbours is FlowingFluid.sourceNeighborCount: how many of the four
// sides hold a source of this fluid.
func (s *Server) sourceNeighbours(w *world.World, lv *fluidLevels, c [3]int32) int {
	n := 0
	for _, d := range horizontalOffsets {
		if l, ok := lv.level[w.Block(c[0]+d[0], c[1], c[2]+d[2])]; ok && l == waterSource {
			n++
		}
	}
	return n
}

// writeFluid places a fluid the flow decided on. What the two fluids make where
// they meet is not decided here: the block that reacts to water beside it is the
// lava, in the write that puts the water there (lavaReactsToWater), and lava
// falling onto water makes stone before it ever writes (spreadFluid). By the
// time a write lands, what it covers is air or its own kind.
func (s *Server) writeFluid(t proto.Tables, w *world.World, lv, other *fluidLevels, c [3]int32, level int32) {
	state := lv.byLevel[level]
	current := w.Block(c[0], c[1], c[2])
	if current == state {
		return
	}
	// A source is never written over. FlowingFluid.spreadTo refuses when the
	// target's fluid is a source, and without that a flowing cell writes back
	// over the source that feeds it: the source stops being a source, the
	// levels climb a step every pass, and the pool turns into a gradient.
	if l, ok := lv.level[current]; ok && l == waterSource {
		return
	}
	if other != nil {
		if l, ok := other.level[current]; ok && l == waterSource {
			return
		}
	}
	if !s.overwritable(t, w, lv, current) {
		return
	}
	s.applyFluid(t, w, c[0], c[1], c[2], state)
}
