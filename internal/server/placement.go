package server

import (
	"math"
	"strconv"
	"strings"

	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Face numbers as the protocol sends them, which is also the order
// world.faceOffset uses.
const (
	faceDown int32 = iota
	faceUp
	faceNorth
	faceSouth
	faceWest
	faceEast
)

var faceNames = [6]string{"down", "up", "north", "south", "west", "east"}

// blockState resolves a block name plus properties through a version's
// block-state table, which is loaded once per protocol.
func (s *Server) blockState(t proto.Tables, name string, props map[string]string) (int32, bool) {
	tab := s.blockStates(t)
	if tab == nil {
		return 0, false
	}
	return tab.Lookup(name, props)
}

// blockStates is the per-version block-state table, loaded once. Placement
// needs it in both directions: properties to a state id, and a neighbour's id
// back to its block and properties.
func (s *Server) blockStates(t proto.Tables) *world.BlockStateTable {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tab, ok := s.blockStateTables[t.Protocol]; ok {
		return tab
	}
	tab, err := world.LoadBlockStates(t.BlockStates)
	if err != nil {
		return nil
	}
	if s.blockStateTables == nil {
		s.blockStateTables = make(map[int32]*world.BlockStateTable)
	}
	s.blockStateTables[t.Protocol] = tab
	return tab
}

// blockStateName resolves a state id back to its block and properties.

// toggleOpenable flips a door, a trapdoor or a fence gate, and reports whether
// the block was one of those. The state table already answers what a placed
// block's properties are, so this is a lookup and a re-encode rather than a
// guess.
//
// A double door's other half goes with it: the two halves of one door are the
// same block at y and y+1 (or y-1). What is not here is the pairing of two
// doors side by side, which is a shape rule of its own.
func (s *session) toggleOpenable(x, y, z int32) bool {
	t := s.t
	state := s.world.Block(x, y, z)
	name, props, ok := s.srv.blockStateName(t, state)
	if !ok || props == nil {
		return false
	}
	open, ok := props["open"]
	if !ok {
		return false
	}
	want := "true"
	action := "open"
	if open == "true" {
		want = "false"
		action = "close"
	}
	props["open"] = want
	next, ok := s.srv.blockState(t, name, props)
	if !ok {
		return false
	}
	s.setBlockUpdate(x, y, z, next)
	s.srv.playOpenableSound(t, x, y, z, name, action)

	// The other half of the same door.
	half := props["half"]
	if half == "" || props["hinge"] == "" {
		return true // a trapdoor or a gate: one block
	}
	oy := y + 1
	if half == "upper" {
		oy = y - 1
	}
	if otherName, otherProps, ok := s.srv.blockStateName(t, s.world.Block(x, oy, z)); ok && otherName == name && otherProps != nil {
		otherProps["open"] = want
		if otherState, ok := s.srv.blockState(t, otherName, otherProps); ok {
			s.setBlockUpdate(x, oy, z, otherState)
		}
	}
	return true
}

// playOpenableSound plays the sound a door, trapdoor or gate makes. Their
// sounds are not in the block sound groups (a door is not "wood"), so the base
// name comes from the block.
func (s *Server) playOpenableSound(t proto.Tables, x, y, z int32, blockName, action string) {
	short := strings.TrimPrefix(blockName, "minecraft:")
	base := ""
	switch {
	case strings.HasSuffix(short, "_door"):
		base = "wooden_door"
		if short == "iron_door" {
			base = "iron_door"
		}
	case strings.HasSuffix(short, "_trapdoor"):
		base = "wooden_trapdoor"
		if short == "iron_trapdoor" {
			base = "iron_trapdoor"
		}
	case strings.HasSuffix(short, "_fence_gate"):
		base = "fence_gate"
	}
	if base == "" {
		return
	}
	id, ok := t.SoundEventIDs["minecraft:block."+base+"."+action]
	if !ok || t.PlayClientboundSoundEffect == 0 {
		return
	}
	s.playSound(t, id, soundSourceBlocks, float32(x)+0.5, float32(y)+0.5, float32(z)+0.5, 0.3, 1)
}

func (s *Server) blockStateName(t proto.Tables, state int32) (string, map[string]string, bool) {
	tab := s.blockStates(t)
	if tab == nil {
		return "", nil, false
	}
	return tab.NameOf(state)
}

// horizontalFromYaw is Direction.fromYRot: the horizontal direction a player
// facing that yaw is looking. The 2D data values run south, west, north, east.
func horizontalFromYaw(yaw float32) string {
	d := int(math.Floor(float64(yaw)/90+0.5)) & 3
	order := [4]string{"south", "west", "north", "east"}
	return order[d]
}

func oppositeFace(name string) string {
	switch name {
	case "north":
		return "south"
	case "south":
		return "north"
	case "east":
		return "west"
	default:
		return "east"
	}
}

// clockwiseFace is Direction.getClockWise, which an anvil uses: it faces the
// player from the side rather than looking back at them.
func clockwiseFace(name string) string {
	switch name {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	default:
		return "north"
	}
}

// rotationSegment is RotationSegment.convertToSegment for 16 segments, which is
// what signs and banners use: round(degrees * 16 / 360) wrapped to 16.
func rotationSegment(yaw float32) int {
	return int(math.Round(float64(yaw+180)*16/360)) & 15
}

// placement is what a player is about to place: the block, the properties
// derived from how they are standing and where they clicked, and the state id
// those properties resolve to.
type placement struct {
	name  string
	props map[string]string
	state int32
}

// placeSpec works out the block a player places, following vanilla's per-block
// getStateForPlacement rules. Falling back to the item's default state (which is
// what this used to do for everything) left every placed block facing north
// whatever direction the player looked.
func (s *session) placeSpec(itemID, face int32, hitX, hitY, hitZ float32, x, y, z int32) placement {
	t := s.t
	name, ok := t.ItemBlockName[itemID]
	if !ok {
		return placement{state: s.t.ItemBlockState[itemID]}
	}
	has := func(p string) bool {
		for _, prop := range t.BlockProps[name] {
			if prop == p {
				return true
			}
		}
		return false
	}
	props := map[string]string{}
	faceName := faceNames[face&7]
	horizontalFace := face >= faceNorth
	playerDir := horizontalFromYaw(s.yaw)

	// A wall-mounted twin wins whenever the player clicks a vertical face —
	// that is what turns a sign into a wall sign, and a torch into a wall
	// torch. It is decided by the block being placed, not by its properties:
	// a standing torch has no facing property at all.
	if wall, ok := t.WallVariant[name]; ok && horizontalFace {
		name, props["facing"] = wall, faceName
	} else {
		switch {
		case has("rotation"):
			props["rotation"] = strconv.Itoa(rotationSegment(s.yaw))

		case has("facing"):
			switch {
			case t.SixWayFacing[name]:
				// Blocks that face up and down as well take the clicked face.
				props["facing"] = faceName
			case strings.HasSuffix(name, "_trapdoor"):
				if horizontalFace {
					props["facing"] = faceName
				} else {
					props["facing"] = oppositeFace(playerDir)
				}
			case strings.HasSuffix(name, "_door"), strings.HasSuffix(name, "_stairs"),
				// A bed's facing runs from the foot to the head, and the head
				// is placed the way the player is looking.
				strings.HasSuffix(name, "_bed"):
				props["facing"] = playerDir
			case strings.HasSuffix(name, "anvil"):
				props["facing"] = clockwiseFace(playerDir)
			default:
				// Chests, furnaces, terracotta and the rest face the player.
				props["facing"] = oppositeFace(playerDir)
			}
		}
	}

	if has("type") && isChestBlock(name) {
		// Adjacent chests pair into a double chest, which the client renders
		// from the states alone.
		props["type"] = "single"
	}
	if has("axis") {
		// A pillar takes the clicked face's axis, or the player's own when the
		// top or bottom was clicked.
		switch {
		case face <= faceUp:
			props["axis"] = "y"
		case face <= faceSouth:
			props["axis"] = "z"
		default:
			props["axis"] = "x"
		}
	}

	// The half arises from where on the block the player clicked. Vanilla's
	// expression below means "bottom" and is written once so the two callers
	// cannot drift apart; a click on the up face is a bottom half, and a click
	// on a side counts as bottom so long as it is not above the middle.
	bottomHalf := func() bool {
		return face != faceDown && (face == faceUp || hitY <= 0.5)
	}
	switch {
	case has("half") && strings.HasSuffix(name, "_trapdoor"):
		switch {
		case horizontalFace:
			props["half"] = topOrBottom(hitY > 0.5)
		case face == faceUp:
			props["half"] = "bottom"
		default:
			props["half"] = "top"
		}
	case has("half") && strings.HasSuffix(name, "_door"):
		props["half"] = "lower"
	case has("half"):
		props["half"] = topOrBottom(!bottomHalf())
	}
	if has("type") && strings.HasSuffix(name, "_slab") {
		// Slabs share the property name with chests, so this has to be about
		// slabs specifically — a chest's type is single, left or right.
		props["type"] = topOrBottom(!bottomHalf())
	}
	if has("hinge") { // doors
		props["hinge"] = doorHinge(playerDir, hitX, hitZ)
	}
	if has("hanging") { // lanterns hang when the player is looking up
		if s.pitch < 0 {
			props["hanging"] = "true"
		}
	}

	if props["type"] == "single" {
		if typ, _, ok := s.chestType(name, x, y, z, props["facing"]); ok {
			props["type"] = typ
		}
	}

	state, ok := s.srv.blockState(t, name, props)
	if !ok {
		return placement{name: name, props: props, state: s.t.ItemBlockState[itemID]}
	}
	return placement{name: name, props: props, state: state}
}

// topOrBottom names a half: slabs and stairs share the words, trapdoors use
// the same two values.
func topOrBottom(top bool) string {
	if top {
		return "top"
	}
	return "bottom"
}

// doorHinge is the geometric half of DoorBlock.getHinge. Vanilla also weighs
// which neighbouring blocks are solid; without a collision-shape table this
// uses the position of the click, which is what vanilla falls back to.
func doorHinge(facing string, hitX, hitZ float32) string {
	var stepX, stepZ int
	switch facing {
	case "north":
		stepZ = -1
	case "south":
		stepZ = 1
	case "west":
		stepX = -1
	default:
		stepX = 1
	}
	left := (stepX >= 0 || hitZ >= 0.5) && (stepX <= 0 || hitZ <= 0.5) &&
		(stepZ >= 0 || hitX <= 0.5) && (stepZ <= 0 || hitX >= 0.5)
	if left {
		return "left"
	}
	return "right"
}

// isChestBlock reports whether a block is a chest. The plain chest's name ends
// with a colon, not an underscore, so a suffix test alone misses it — and with
// it trapped and copper chests, which do end in "_chest".
func isChestBlock(name string) bool {
	return name == "minecraft:chest" || strings.HasSuffix(name, "_chest")
}

// chestType works out whether a newly placed chest joins the chest beside it,
// following ChestBlock.getChestType: look to each side in turn and, when the
// neighbour is a single chest, a chest facing the same way as this one pairs
// with it — on the left, or on the right.
func (s *session) chestType(name string, x, y, z int32, facing string) (string, [3]int32, bool) {
	var found [3]int32
	partner := func(dir string) bool {
		dx, dy, dz := faceOffset(faceOf(dir))
		px, py, pz := x+dx, y+dy, z+dz
		state := s.world.Block(px, py, pz)
		otherName, otherProps, ok := s.srv.blockStateName(s.t, state)
		if !ok || otherName != name || otherProps["type"] != "single" {
			return false
		}
		if otherProps["facing"] != facing {
			return false
		}
		found = [3]int32{px, py, pz}
		return true
	}
	if partner(clockwiseFace(facing)) {
		return "left", found, true
	}
	if partner(counterClockwiseFace(facing)) {
		return "right", found, true
	}
	return "single", [3]int32{}, false
}

// pairChest updates the chest beside a newly placed one. This is
// ChestBlock.updateShape: when a chest is placed beside a single chest that
// faces the same way, the neighbour takes the *opposite* type, which is what
// makes the pair render as one double chest.
func (s *session) pairChest(p placement, bx, by, bz int32) {
	next, pos, ok := s.chestPartnerState(p, bx, by, bz)
	if !ok {
		return
	}
	s.setBlockUpdate(pos[0], pos[1], pos[2], next)
}

// chestPartnerState is the state the neighbouring chest takes, and where it is.
// Keeping the decision separate from the sending is what lets it be tested
// without a connection.
func (s *session) chestPartnerState(p placement, bx, by, bz int32) (int32, [3]int32, bool) {
	if !isChestBlock(p.name) {
		return 0, [3]int32{}, false
	}
	switch p.props["type"] {
	case "left", "right":
	default:
		return 0, [3]int32{}, false
	}
	_, partner, ok := s.chestType(p.name, bx, by, bz, p.props["facing"])
	if !ok {
		return 0, [3]int32{}, false
	}
	name, props, ok := s.srv.blockStateName(s.t, s.world.Block(partner[0], partner[1], partner[2]))
	if !ok || name != p.name || props["facing"] != p.props["facing"] || props["type"] != "single" {
		return 0, [3]int32{}, false
	}
	opposite := "right"
	if p.props["type"] == "right" {
		opposite = "left"
	}
	next, ok := s.srv.blockState(s.t, name, map[string]string{"facing": props["facing"], "type": opposite})
	if !ok {
		return 0, [3]int32{}, false
	}
	return next, partner, true
}

// counterClockwiseFace is Direction.getCounterClockWise, the mirror of
// clockwiseFace.
func counterClockwiseFace(name string) string {
	switch name {
	case "north":
		return "west"
	case "west":
		return "south"
	case "south":
		return "east"
	default:
		return "north"
	}
}

// faceOf is the face number for a horizontal direction name.
func faceOf(name string) int32 {
	switch name {
	case "north":
		return faceNorth
	case "south":
		return faceSouth
	case "west":
		return faceWest
	default:
		return faceEast
	}
}

// otherHalf places a bed's head or a door's upper half: both blocks vanilla
// places in one go, and both of which look broken on their own — half a bed, or
// a door with nothing above it.
//
// It returns the state and the offset from the block the player clicked.
func (s *session) otherHalf(p placement, bx, by, bz int32) (int32, int32, int32, int32, bool) {
	switch {
	case strings.HasSuffix(p.name, "_bed"):
		facing := p.props["facing"]
		dx, _, dz := faceOffset(faceOf(facing))
		head := make(map[string]string, len(p.props)+1)
		for k, v := range p.props {
			head[k] = v
		}
		head["part"] = "head"
		state, ok := s.srv.blockState(s.t, p.name, head)
		if !ok {
			return 0, 0, 0, 0, false
		}
		return state, bx + dx, by, bz + dz, true
	case strings.HasSuffix(p.name, "_door"):
		upper := make(map[string]string, len(p.props)+1)
		for k, v := range p.props {
			upper[k] = v
		}
		upper["half"] = "upper"
		state, ok := s.srv.blockState(s.t, p.name, upper)
		if !ok {
			return 0, 0, 0, 0, false
		}
		return state, bx, by + 1, bz, true
	}
	return 0, 0, 0, 0, false
}

// upperDoorState is the state of the block above a door: the same door with its
// half flipped. Vanilla places both halves at once, so a door without its upper
// half would be a floating lower half.
func (s *session) upperDoorState(p placement) (int32, bool) {
	if !strings.HasSuffix(p.name, "_door") {
		return 0, false
	}
	upper := make(map[string]string, len(p.props)+1)
	for k, v := range p.props {
		upper[k] = v
	}
	upper["half"] = "upper"
	return s.srv.blockState(s.t, p.name, upper)
}
