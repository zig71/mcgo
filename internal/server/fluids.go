package server

import (
	"math"
	"strconv"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Fluid levels, as the level property the water and lava blocks share: 0 is a
// source, 1-7 run thinner with distance (lava stops at 3), and 8 is the
// "falling" form a fluid takes under itself.
const (
	waterSource  = 0
	waterFalling = 8

	// waterMaxSpread is how far water runs from its source, and lavaMaxSpread
	// how far lava does. Lava also ticks six times more slowly.
	waterMaxSpread = 7
	lavaMaxSpread  = 3
	waterTick      = 5
	lavaTick       = 30
)

// fluidLevels is a version's block states for one fluid, by level. Resolved
// once per fluid per version.
type fluidLevels struct {
	name    string
	byLevel [16]int32 // level -> block state
	level   map[int32]int32
	// maxSpread is how far this fluid runs from its source, slopeFind is how
	// far the search for a drop looks (FlowingFluid's slope find distance), and
	// source is the state it starts from.
	maxSpread int32
	slopeFind int32
	tick      int64
	// isWater is what decides which fluid wins when the two meet.
	isWater bool
}

// waterLevelsFor and lavaLevelsFor resolve a fluid's states in a version, and
// the reverse lookup from a state back to its level.
func (s *Server) waterLevelsFor(t proto.Tables) *fluidLevels {
	return s.fluidLevelsFor(t, "minecraft:water", waterMaxSpread, 4, waterTick, true)
}

func (s *Server) lavaLevelsFor(t proto.Tables) *fluidLevels {
	return s.fluidLevelsFor(t, "minecraft:lava", lavaMaxSpread, 2, lavaTick, false)
}

func (s *Server) fluidLevelsFor(t proto.Tables, name string, spread, slopeFind int32, tick int64, isWater bool) *fluidLevels {
	key := fluidKey{protocol: t.Protocol, water: isWater}
	s.mu.Lock()
	if lv, ok := s.fluidStates[key]; ok {
		s.mu.Unlock()
		return lv
	}
	s.mu.Unlock()
	lv := &fluidLevels{
		name: name, level: make(map[int32]int32, 16),
		maxSpread: spread, slopeFind: slopeFind, tick: tick, isWater: isWater,
	}
	for n := range 16 {
		state, ok := s.blockState(t, name, map[string]string{"level": strconv.Itoa(n)})
		if !ok {
			break
		}
		lv.byLevel[n] = state
		lv.level[state] = int32(n)
	}
	if len(lv.level) == 0 {
		lv = nil
	}
	s.mu.Lock()
	if s.fluidStates == nil {
		s.fluidStates = make(map[fluidKey]*fluidLevels)
	}
	s.fluidStates[key] = lv
	s.mu.Unlock()
	return lv
}

// fluidKey is which fluid's table is wanted, in which protocol version.
type fluidKey struct {
	protocol int32
	water    bool
}

// enqueueFluids marks the water around a block change for re-evaluation. Water
// is settled until something next to it changes, which is exactly when it has
// to flow again.
func (s *Server) enqueueFluids(x, y, z int32) {
	s.fluidMu.Lock()
	defer s.fluidMu.Unlock()
	if s.fluidCells == nil {
		s.fluidCells = make(map[[3]int32]bool)
	}
	for _, d := range neighbourOffsets {
		s.fluidCells[[3]int32{x + d[0], y + d[1], z + d[2]}] = true
	}
}

var neighbourOffsets = [7][3]int32{{0, 0, 0}, {1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

// horizontalOffsets are the four sideways neighbours. Water spreads to these
// and never upwards: slicing the list above wrongly took up and down for
// sideways, which made water climb.
var horizontalOffsets = [4][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}}

// noteWater records a cell as water, so the tick loop keeps tracking it.
func (s *Server) noteWater(x, y, z int32) {
	s.fluidMu.Lock()
	if s.fluidCells == nil {
		s.fluidCells = make(map[[3]int32]bool)
	}
	if s.lavaCells == nil {
		s.lavaCells = make(map[[3]int32]bool)
	}
	// A change next to either fluid affects both: they meet each other.
	s.fluidCells[[3]int32{x, y, z}] = true
	s.lavaCells[[3]int32{x, y, z}] = true
	s.fluidMu.Unlock()
}

// tickFluids advances water. Vanilla ticks fluid every five game ticks, which
// is why water flows in steps rather than instantly.
func (s *Server) tickFluids() {
	age := s.Age()
	water := age%waterTick == 0
	lava := age%lavaTick == 0
	if !water && !lava {
		return
	}
	s.seedSavedFluids(s.newest)
	s.fluidMu.Lock()
	var waterCells, lavaCells map[[3]int32]bool
	if water {
		waterCells = s.fluidCells
		s.fluidCells = make(map[[3]int32]bool)
	}
	if lava {
		lavaCells = s.lavaCells
		s.lavaCells = make(map[[3]int32]bool)
	}
	s.fluidMu.Unlock()

	t := s.newest
	w := s.worldFor(t)
	if water && lava {
		// Whichever runs, the other fluid's cells are still worth a look: a
		// change beside water is also a change beside whatever is next to it.
		for pos := range lavaCells {
			waterCells[pos] = true
		}
	}

	if water {
		if lv := s.waterLevelsFor(t); lv != nil && len(waterCells) > 0 {
			seeds := make([][3]int32, 0, len(waterCells))
			for pos := range waterCells {
				seeds = append(seeds, pos)
			}
			s.tickFluid(t, w, lv, s.lavaLevelsFor(t), seeds)
		}
	}
	if lava {
		if lv := s.lavaLevelsFor(t); lv != nil && len(lavaCells) > 0 {
			seeds := make([][3]int32, 0, len(lavaCells))
			for pos := range lavaCells {
				seeds = append(seeds, pos)
			}
			s.tickFluid(t, w, lv, s.waterLevelsFor(t), seeds)
		}
	}
}

// seedSavedFluids puts every fluid in the world file back in the tick queue,
// once per process.
//
// Fluid a player poured is saved like any other block, and nothing has touched
// it since the restart — so a lava column that was falling when the server
// stopped hangs in the air until something wakes it. This is the chunk-load
// scheduling vanilla does: a stream that was flowing keeps flowing.
func (s *Server) seedSavedFluids(t proto.Tables) {
	s.fluidMu.Lock()
	seeded := s.fluidsSeeded
	s.fluidsSeeded = true
	s.fluidMu.Unlock()
	if seeded {
		return
	}
	water, lava := s.waterLevelsFor(t), s.lavaLevelsFor(t)
	s.edits.Each(func(pos [3]int32, id int32) {
		if water != nil {
			if _, ok := water.level[id]; ok {
				s.noteWater(pos[0], pos[1], pos[2])
				return
			}
		}
		if lava != nil {
			if _, ok := lava.level[id]; ok {
				s.noteWater(pos[0], pos[1], pos[2])
			}
		}
	})
}

// reactionDirs are the directions a lava block looks for water in, which is
// LiquidBlock.POSSIBLE_FLOW_DIRECTIONS with the opposite taken: up, north,
// south, east and west. Down is deliberately not one of them — vanilla leaves
// water below alone here, because lava falling onto water makes stone instead.
var reactionDirs = [5][3]int32{{0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {1, 0, 0}, {-1, 0, 0}}

// everyDir is the six directions a change is felt from.
var everyDir = [6][3]int32{{0, 1, 0}, {0, -1, 0}, {0, 0, -1}, {0, 0, 1}, {1, 0, 0}, {-1, 0, 0}}

// levelEventFizz is the level event a block plays when water and lava meet: the
// hiss and the smoke, at the position that set.
const levelEventFizz = 1501

// fluidNeighbourChanged is LiquidBlock.neighborChanged for what a change at
// (x,y,z) does to the lava around it: the lava blocks next to it react to water
// that now touches them. Vanilla reacts to the change, not to the fluid tick,
// which is what makes pouring a bucket next to the other fluid work.
func (s *Server) fluidNeighbourChanged(t proto.Tables, w *world.World, x, y, z int32) {
	for _, d := range everyDir {
		s.lavaReactsToWater(t, w, x+d[0], y+d[1], z+d[2])
	}
}

// lavaReactsToWater is LiquidBlock.shouldSpreadLiquid: a lava block with water
// in one of those five directions sets into obsidian when it is a source and
// into cobblestone when it flows, and fizzes where it changes.
//
// The distinction is the whole point: a source touched by water is obsidian,
// running lava is cobblestone, and the block that changes is the lava, never
// the water.
func (s *Server) lavaReactsToWater(t proto.Tables, w *world.World, x, y, z int32) {
	lava := s.lavaLevelsFor(t)
	if lava == nil {
		return
	}
	level, isLava := lava.level[w.Block(x, y, z)]
	if !isLava {
		return
	}
	water := s.waterLevelsFor(t)
	if water == nil {
		return
	}
	for _, d := range reactionDirs {
		if _, ok := water.level[w.Block(x+d[0], y+d[1], z+d[2])]; !ok {
			continue
		}
		name := "minecraft:cobblestone"
		if level == waterSource {
			name = "minecraft:obsidian"
		}
		if solid, ok := s.blockState(t, name, nil); ok {
			s.applyFluid(t, w, x, y, z, solid)
			s.fizz(t, x, y, z)
		}
		return
	}
}

// fizz plays that level event.
func (s *Server) fizz(t proto.Tables, x, y, z int32) {
	s.levelEvent(t, x, y, z, levelEventFizz, 0)
}

// overwritable reports whether a fluid may write over what is at a position:
// air, or the same fluid. Anything else is a solid, and solids win — which is
// what keeps the stone and cobblestone the two fluids set into from being
// undone by the flow that made them. It is also why lava never flows sideways
// over water: WaterFluid.canBeReplacedWith lets a fluid take a water block only
// from above, and from above the two make stone instead (spreadFluid).
func (s *Server) overwritable(t proto.Tables, w *world.World, lv *fluidLevels, state int32) bool {
	if state == t.BlockAir {
		return true
	}
	_, isThisFluid := lv.level[state]
	return isThisFluid
}

// isWater reports whether a block state is any water.
func isWater(lv *fluidLevels, state int32) bool {
	_, ok := lv.level[state]
	return ok
}

// applyFluid writes a fluid block and tells everyone who can see it.
func (s *Server) applyFluid(t proto.Tables, w *world.World, x, y, z, state int32) {
	if w.Block(x, y, z) == state {
		return
	}
	s.setWorldBlock(t, w, x, y, z, state)
	s.noteWater(x, y, z)
	s.enqueueFluids(x, y, z)
	// The block changed, so the lava around it is due a look: this is what
	// turns a flow that reaches water into cobblestone, and a source into
	// obsidian, without waiting for a fluid tick that may never come.
	s.fluidNeighbourChanged(t, w, x, y, z)
}

// airFor is the air block state for a version.
func (s *Server) airFor(t proto.Tables, w *world.World) int32 {
	return t.BlockAir
}

// setWorldBlock records a block change made by the world itself (water
// flowing), and tells every player who can see it.
func (s *Server) setWorldBlock(t proto.Tables, w *world.World, x, y, z, state int32) {
	cx, cz, secs := w.SetBlock(x, y, z, state)
	// A block appearing or going changes the light around it: the sky light if
	// the column's height changed, and the block light always.
	if len(secs) > 0 {
		body := w.LightUpdate(cx, cz, secs)
		for _, sess := range s.sessionList() {
			if sess.t.Protocol == t.Protocol && sess.sees(x, y, z) {
				_ = sess.sendQuiet(sess.t.PlayClientboundLightUpdate, body)
			}
		}
	}
	w.RelightBlock(x, y, z)
	for chunk, sections := range w.Light().SectionsNear(x, y, z, world.MaxLight-1, w.MinY()) {
		body := w.LightUpdate(chunk[0], chunk[1], sections)
		for _, sess := range s.sessionList() {
			if sess.t.Protocol == t.Protocol && sess.sees(x, y, z) {
				_ = sess.sendQuiet(sess.t.PlayClientboundLightUpdate, body)
			}
		}
	}
	var u mcproto.Writer
	u.WriteLong(packPosition(int(x), int(y), int(z)))
	u.WriteVarInt(state)
	body := u.Bytes()
	for _, sess := range s.sessionList() {
		if sess.t.Protocol != t.Protocol {
			continue
		}
		if !sess.sees(x, y, z) {
			continue
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundBlockUpdate, body)
	}
}

// pourWater places a water source where the player poured it.
func (s *session) pourWater(x, y, z int32) {
	t := s.t
	lv := s.srv.waterLevelsFor(t)
	if lv == nil {
		return
	}
	if s.world.Block(x, y, z) != t.BlockAir {
		return
	}
	s.setBlockUpdate(x, y, z, lv.byLevel[waterSource])
	// A player starting a flow: the cell and everything around it is due a
	// fluid tick.
	s.srv.noteWater(x, y, z)
}

// pourLava places a lava source where the player poured it. Lava runs on the
// same engine as water with its own numbers — six times the tick, three blocks
// from a source instead of seven, and no new sources from two neighbours
// (LavaFluid.canConvertToSource) — and it is the fluid that reacts where the
// two meet.
func (s *session) pourLava(x, y, z int32) {
	t := s.t
	lv := s.srv.lavaLevelsFor(t)
	if lv == nil {
		return
	}
	if s.world.Block(x, y, z) != t.BlockAir {
		return
	}
	state, ok := s.srv.blockState(t, "minecraft:lava", map[string]string{"level": "0"})
	if !ok {
		return
	}
	s.setBlockUpdate(x, y, z, state)
	s.srv.noteWater(x, y, z)
}

// consumeHeldItem replaces the held item with what the action leaves behind — a
// water bucket becomes an empty bucket — and sends the new slot.
func (s *session) consumeHeldItem(used, left int32) {
	// Creative does not use things up: a water bucket stays full, a flint and
	// steel stays lit, a spawn egg is never spent.
	if s.gameMode == gameModeCreative {
		return
	}
	held := 27 + s.heldSlot
	if s.inv[held].ID != used || s.inv[held].Count <= 0 {
		return
	}
	s.inv[held].Count--
	if s.inv[held].Count <= 0 {
		if left != 0 {
			s.inv[held] = world.Item{ID: left, Count: 1}
		} else {
			s.inv[held] = world.Item{}
		}
	}
	// The player's own inventory window is slot 0; its hotbar starts at 36.
	s.sendSlot(36+s.heldSlot, s.inv[held])
}

// sendSlot tells the client what one inventory slot now holds.
func (s *session) sendSlot(slot int32, item world.Item) {
	var w mcproto.Writer
	w.WriteVarInt(0) // the player's inventory
	w.WriteVarInt(0) // state id
	w.WriteShort(int16(slot))
	s.writeSlot(&w, item)
	_ = s.send(s.t.PlayClientboundContainerSetSlot, w.Bytes())
}

// The spread rules below are ported from FlowingFluid in the decompiled server:
// getSpread, getSlopeDistance, getDropOff and spreadToSides. The behaviour that
// matters and that a plain distance-based flood gets wrong: water does not
// spread evenly in every direction, it spreads towards the NEAREST DROP, and
// only in those directions. That is what makes water flow downhill rather than
// pond outwards.

// spreadDirections is FlowingFluid.getSpread: which of the four horizontal
// directions a cell sends water to, and how far each of them is from a drop.
//
// In vanilla's code these are two loops over Direction.Plane.HORIZONTAL, one
// finding the lowest slope distance and one keeping the directions that reach
// it. Both are here, in one pass with the lowest kept.
func (s *Server) spreadDirections(t proto.Tables, w *world.World, lv *fluidLevels, c [3]int32) [][3]int32 {
	lowest := int32(1000)
	var best [][3]int32
	for _, d := range horizontalOffsets {
		side := [3]int32{c[0] + d[0], c[1] + d[1], c[2] + d[2]}
		bs := w.Block(side[0], side[1], side[2])
		if bs != t.BlockAir && !isWater(lv, bs) {
			continue
		}
		distance := int32(0)
		if !s.isHole(t, w, lv, side) {
			distance = s.slopeDistance(t, w, lv, side, 1, [3]int32{-d[0], -d[1], -d[2]})
		}
		if distance < lowest {
			best = best[:0]
		}
		if distance <= lowest {
			best = append(best, d)
			lowest = distance
		}
	}
	return best
}

// isHole is FlowingFluid.SpreadContext.isHole: a position water can fall from.
func (s *Server) isHole(t proto.Tables, w *world.World, lv *fluidLevels, c [3]int32) bool {
	below := [3]int32{c[0], c[1] - 1, c[2]}
	b := w.Block(below[0], below[1], below[2])
	if b == t.BlockAir {
		return true
	}
	return !isWater(lv, b) && !s.isSolid(t, w, below)
}

// isSolid reports whether a block stops water: anything that is not air and not
// this fluid, which is the approximation this server has for a shape.
func (s *Server) isSolid(t proto.Tables, w *world.World, c [3]int32) bool {
	return w.Block(c[0], c[1], c[2]) != t.BlockAir
}

// fluidHeightOf is FlowingFluid.getOwnHeight: a source and a falling column
// stand eight ninths of a block tall, and a flowing one is shorter by its level.
func fluidHeightOf(level int32) float64 {
	if level == waterSource || level >= waterFalling {
		return 8.0 / 9.0
	}
	return float64(8-level) / 9.0
}

// fluidCurrentAt is FlowingFluid.getFlow for one block: a neighbour pulls the
// flow towards itself by how much lower it stands (thisHeight minus its height),
// and a falling column drags down. The other fluid is skipped, since water and
// lava do not feed each other's flow.
func fluidCurrentAt(w *world.World, lv, other *fluidLevels, x, y, z int32) (float64, float64, float64, bool) {
	level, ok := lv.level[w.Block(x, y, z)]
	if !ok {
		return 0, 0, 0, false
	}
	this := fluidHeightOf(level)
	var vx, vz float64
	for _, d := range horizontalOffsets {
		nx, nz := x+d[0], z+d[2]
		ns := w.Block(nx, y, nz)
		if other != nil {
			if _, isOther := other.level[ns]; isOther {
				continue
			}
		}
		if l, ok := lv.level[ns]; ok {
			if dist := this - fluidHeightOf(l); dist != 0 {
				vx += float64(d[0]) * dist
				vz += float64(d[2]) * dist
			}
			continue
		}
		// An empty neighbour: the fluid sitting below it still pulls, which is
		// what carries a flow over a ledge.
		if l, ok := lv.level[w.Block(nx, y-1, nz)]; ok {
			if dist := this - (fluidHeightOf(l) - 0.8888889); dist != 0 {
				vx += float64(d[0]) * dist
				vz += float64(d[2]) * dist
			}
		}
	}
	vy := 0.0
	if level >= waterFalling {
		vy = -6
	}
	length := math.Sqrt(vx*vx + vy*vy + vz*vz)
	if length < 1e-5 {
		return 0, 0, 0, false
	}
	return vx / length, vy / length, vz / length, true
}

// fluidCurrent is the velocity a fluid adds to an entity in one tick, following
// Entity.updateFluidInteraction: the flow under the entity's feet, damped when
// the fluid is only a film over them, scaled by the fluid's current strength —
// water 0.014, lava 0.00233 (EnvironmentAttributes.FAST_LAVA, which this server
// does not model, would make lava's 0.007).
func (s *Server) fluidCurrent(t proto.Tables, w *world.World, e *Entity) (float64, float64, float64) {
	water, lava := s.waterLevelsFor(t), s.lavaLevelsFor(t)
	var vx, vy, vz float64
	for _, f := range []struct {
		lv, other *fluidLevels
		scale     float64
	}{
		{water, lava, 0.014},
		{lava, water, 0.0023333333333333335},
	} {
		if f.lv == nil {
			continue
		}
		cx, cy, cz := int32(math.Floor(e.X)), int32(math.Floor(e.Y)), int32(math.Floor(e.Z))
		// The feet, or the block below them: an entity resting on the bottom of
		// a pool is in the current too.
		if _, ok := f.lv.level[w.Block(cx, cy, cz)]; !ok {
			if _, ok := f.lv.level[w.Block(cx, cy-1, cz)]; !ok {
				continue
			}
			cy--
		}
		dx, dy, dz, ok := fluidCurrentAt(w, f.lv, f.other, cx, cy, cz)
		if !ok {
			continue
		}
		// A fluid shallower than four tenths of a block pushes proportionally
		// less, which is what stops a puddle at the edge of a flow from
		// carrying an entity away.
		if depth := float64(cy) + fluidHeightOf(f.lv.level[w.Block(cx, cy, cz)]) - e.Y; depth < 0.4 {
			dx, dy, dz = dx*depth, dy*depth, dz*depth
		}
		vx += dx * f.scale
		vy += dy * f.scale
		vz += dz * f.scale
	}
	// A current has to overcome a still entity's grip: vanilla lifts an impulse
	// short of four and a half thousandths to exactly that much.
	if math.Abs(e.VX) < 0.003 && math.Abs(e.VZ) < 0.003 {
		if l := math.Sqrt(vx*vx + vy*vy + vz*vz); l > 0 && l < 0.0045 {
			vx, vy, vz = vx/l*0.0045, vy/l*0.0045, vz/l*0.0045
		}
	}
	return vx, vy, vz
}

// slopeDistance is FlowingFluid.getSlopeDistance: how many steps from here to
// the nearest drop, searching no further than the fluid's slope find distance
// (four for water, two for lava).
func (s *Server) slopeDistance(t proto.Tables, w *world.World, lv *fluidLevels, c [3]int32, pass int32, from [3]int32) int32 {
	lowest := int32(1000)
	for _, d := range horizontalOffsets {
		if d == from {
			continue
		}
		side := [3]int32{c[0] + d[0], c[1] + d[1], c[2] + d[2]}
		bs := w.Block(side[0], side[1], side[2])
		if bs != t.BlockAir && !isWater(lv, bs) {
			continue
		}
		if s.isHole(t, w, lv, side) {
			return pass
		}
		if pass < lv.slopeFind {
			if v := s.slopeDistance(t, w, lv, side, pass+1, [3]int32{-d[0], -d[1], -d[2]}); v < lowest {
				lowest = v
			}
		}
	}
	return lowest
}
