package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// placer builds a session that can resolve placement states. It has a real
// world because placement looks at the blocks around the one being placed: a
// chest pairs with the chest beside it, and a bed's head lands next to its foot.
func placer(yaw, pitch float32) *session {
	tab := v777.Tables
	w := world.NewWorld(world.FlatConfig{
		Air:                 tab.BlockAir,
		Bedrock:             tab.BlockBedrock,
		Dirt:                tab.BlockDirt,
		Grass:               tab.BlockGrass,
		MinY:                -64,
		Height:              384,
		SurfaceY:            64,
		SignBlockEntityType: tab.SignBlockEntityType,
	}, world.NewEdits(), world.NewSigns(), world.NewBanners())
	return &session{t: tab, srv: &Server{}, world: w, yaw: yaw, pitch: pitch}
}

func itemOf(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := v777.Tables.ItemIDs[name]
	if !ok || id == 0 {
		t.Fatalf("unknown item %s", name)
	}
	return id
}

func TestSignRotationFollowsYaw(t *testing.T) {
	// RotationSegment.convertToSegment(yaw + 180) with 16 segments: a player
	// facing south (yaw 0) places a sign at segment 8, and one facing north at
	// segment 0.
	for _, c := range []struct {
		yaw  float32
		want string
	}{{0, "8"}, {90, "12"}, {180, "0"}, {270, "4"}} {
		s := placer(c.yaw, 0)
		got := s.placeSpec(itemOf(t, "minecraft:oak_sign"), faceUp, 0.5, 0.5, 0.5, 0, 65, 0)
		if got.name != "minecraft:oak_sign" {
			t.Fatalf("yaw %v: placed %s, want a standing sign", c.yaw, got.name)
		}
		if got.props["rotation"] != c.want {
			t.Errorf("yaw %v: rotation = %s, want %s", c.yaw, got.props["rotation"], c.want)
		}
	}
}

func TestSignOnAWallBecomesWallSign(t *testing.T) {
	s := placer(0, 0)
	got := s.placeSpec(itemOf(t, "minecraft:oak_sign"), faceEast, 0.5, 0.5, 0.5, 0, 65, 0)

	if got.name != "minecraft:oak_wall_sign" {
		t.Fatalf("placed %s, want a wall sign", got.name)
	}
	if got.props["facing"] != "east" {
		t.Errorf("facing = %s, want east", got.props["facing"])
	}
}

func TestBannerAndTorchWallVariants(t *testing.T) {
	s := placer(0, 0)
	banner := s.placeSpec(itemOf(t, "minecraft:white_banner"), faceWest, 0.5, 0.5, 0.5, 0, 65, 0)
	if banner.name != "minecraft:white_wall_banner" || banner.props["facing"] != "west" {
		t.Errorf("banner placed as %s %v", banner.name, banner.props)
	}
	torch := s.placeSpec(itemOf(t, "minecraft:torch"), faceNorth, 0.5, 0.5, 0.5, 0, 65, 0)
	if torch.name != "minecraft:wall_torch" || torch.props["facing"] != "north" {
		t.Errorf("torch placed as %s %v", torch.name, torch.props)
	}
}

func TestAnvilFacesClockwise(t *testing.T) {
	// AnvilBlock uses getHorizontalDirection().getClockWise(): a player facing
	// south gets an anvil facing west.
	s := placer(0, 0)
	got := s.placeSpec(itemOf(t, "minecraft:anvil"), faceUp, 0.5, 0.5, 0.5, 0, 65, 0)
	if got.props["facing"] != "west" {
		t.Errorf("anvil facing = %s, want west", got.props["facing"])
	}
}

func TestChestAndFurnaceFaceThePlayer(t *testing.T) {
	for _, name := range []string{"minecraft:chest", "minecraft:furnace", "minecraft:white_glazed_terracotta"} {
		s := placer(0, 0) // facing south
		got := s.placeSpec(itemOf(t, name), faceUp, 0.5, 0.5, 0.5, 0, 65, 0)
		if got.props["facing"] != "north" {
			t.Errorf("%s facing = %s, want north (the player looks south)", name, got.props["facing"])
		}
	}
}

func TestPillarAxisFollowsTheFace(t *testing.T) {
	s := placer(0, 0)
	for _, c := range []struct {
		face int32
		want string
	}{{faceUp, "y"}, {faceNorth, "z"}, {faceEast, "x"}} {
		got := s.placeSpec(itemOf(t, "minecraft:oak_log"), c.face, 0.5, 0.5, 0.5, 0, 65, 0)
		if got.props["axis"] != c.want {
			t.Errorf("face %d: axis = %s, want %s", c.face, got.props["axis"], c.want)
		}
	}
}

func TestStairsFacingHalfAndShape(t *testing.T) {
	// Stairs take the player's own direction, not its opposite.
	s := placer(90, 0) // facing west
	bottom := s.placeSpec(itemOf(t, "minecraft:oak_stairs"), faceUp, 0.5, 0.9, 0.5, 0, 65, 0)
	if bottom.props["facing"] != "west" {
		t.Errorf("stairs facing = %s, want west", bottom.props["facing"])
	}
	if bottom.props["half"] != "bottom" {
		t.Errorf("stairs half = %s, want bottom when placing on top of a block", bottom.props["half"])
	}

	top := s.placeSpec(itemOf(t, "minecraft:oak_stairs"), faceEast, 0.5, 0.9, 0.5, 0, 65, 0)
	if top.props["half"] != "top" {
		t.Errorf("clicking the upper half gave half = %s, want top", top.props["half"])
	}
}

func TestSlabTypeFollowsTheCursor(t *testing.T) {
	s := placer(0, 0)
	bottom := s.placeSpec(itemOf(t, "minecraft:oak_slab"), faceEast, 0.5, 0.2, 0.5, 0, 65, 0)
	if bottom.props["type"] != "bottom" {
		t.Errorf("slab type = %s, want bottom", bottom.props["type"])
	}
	top := s.placeSpec(itemOf(t, "minecraft:oak_slab"), faceEast, 0.5, 0.8, 0.5, 0, 65, 0)
	if top.props["type"] != "top" {
		t.Errorf("slab type = %s, want top", top.props["type"])
	}
}

func TestDoorPlacesBothHalves(t *testing.T) {
	s := placer(0, 0) // facing south
	lower := s.placeSpec(itemOf(t, "minecraft:oak_door"), faceUp, 0.5, 0.5, 0.5, 0, 65, 0)
	if lower.props["facing"] != "south" || lower.props["half"] != "lower" {
		t.Fatalf("door = %s %v", lower.name, lower.props)
	}
	if lower.props["hinge"] != "left" && lower.props["hinge"] != "right" {
		t.Errorf("door hinge = %q", lower.props["hinge"])
	}
	upper, ok := s.upperDoorState(lower)
	if !ok || upper == 0 {
		t.Fatal("no upper half for a door")
	}
	// The upper half must differ from the lower one.
	if upper == lower.state {
		t.Error("the upper half is the same state as the lower half")
	}
}

func TestLanternHangsWhenLookingUp(t *testing.T) {
	up := placer(0, -30) // pitch below zero looks up
	if got := up.placeSpec(itemOf(t, "minecraft:lantern"), faceDown, 0.5, 0.5, 0.5, 0, 65, 0); got.props["hanging"] != "true" {
		t.Errorf("looking up gave hanging = %q, want true", got.props["hanging"])
	}
	down := placer(0, 30)
	if got := down.placeSpec(itemOf(t, "minecraft:lantern"), faceUp, 0.5, 0.5, 0.5, 0, 65, 0); got.props["hanging"] != "" {
		t.Errorf("looking down gave hanging = %q, want the default (false)", got.props["hanging"])
	}
}

// A bed is two blocks. Placing only the half the player stands on leaves the
// bed cut in half once the client's prediction is corrected.
func TestBedPlacesBothHalves(t *testing.T) {
	s := placer(0, 0)
	foot := s.placeSpec(itemOf(t, "minecraft:red_bed"), faceUp, 0.5, 0.5, 0.5, 0, 65, 0)

	if foot.name != "minecraft:red_bed" {
		t.Fatalf("placed %s", foot.name)
	}
	if foot.props["part"] == "head" {
		t.Error("the block the player clicked should be the foot of the bed")
	}
	head, x, y, z, ok := s.otherHalf(foot, 0, 65, 0)
	if !ok || head == 0 {
		t.Fatal("a bed placed no head half")
	}
	if head == foot.state {
		t.Error("the head half is the same state as the foot")
	}
	// The player faces south, so the head lies one block south.
	if x != 0 || y != 65 || z != 1 {
		t.Errorf("the head half is at (%d,%d,%d), want (0,65,1)", x, y, z)
	}
}

// Two chests side by side are one double chest, which the client renders from
// the states: the second chest's type, and the first chest's, both change.
func TestChestsPairIntoADouble(t *testing.T) {
	s := placer(0, 0)
	chest := itemOf(t, "minecraft:chest")

	// The player faces south, so a chest placed now faces north.
	first := s.placeSpec(chest, faceUp, 0.5, 0.5, 0.5, 1, 65, 0)
	if first.props["facing"] != "north" {
		t.Fatalf("first chest faces %s, want north", first.props["facing"])
	}
	if first.props["type"] != "single" {
		t.Fatalf("a lone chest is %q, want single", first.props["type"])
	}
	s.world.SetBlock(1, 65, 0, first.state)

	// Placed to its west, this one looks east and finds the first.
	second := s.placeSpec(chest, faceUp, 0.5, 0.5, 0.5, 0, 65, 0)
	if second.props["type"] == "single" || second.props["type"] == "" {
		t.Fatalf("the second chest is %q, want it to pair", second.props["type"])
	}

	// And the chest that was already there must be told, or the pair renders as
	// two singles. Vanilla's updateShape gives the neighbour the opposite type,
	// so the two halves are one left and one right.
	s.world.SetBlock(0, 65, 0, second.state)

	next, pos, ok := s.chestPartnerState(second, 0, 65, 0)
	if !ok {
		t.Fatal("the chest already there was not updated")
	}
	if pos != [3]int32{1, 65, 0} {
		t.Errorf("the updated chest is at %v, want (1,65,0)", pos)
	}
	_, props, ok := s.srv.blockStateName(s.t, next)
	if !ok {
		t.Fatal("the neighbour's state did not resolve")
	}
	want := "right"
	if second.props["type"] == "right" {
		want = "left"
	}
	if props["type"] != want {
		t.Errorf("the existing chest becomes %q, want %q (a pair is one left, one right)", props["type"], want)
	}
}

// Right-clicking a door opens and closes it, both halves at once, and plays the
// door's own sound.
func TestDoorOpens(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5

	lower, ok := srv.blockState(tab, "minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false",
	})
	if !ok {
		t.Fatal("no such door state")
	}
	upper, ok := srv.blockState(tab, "minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false",
	})
	if !ok {
		t.Fatal("no such door state")
	}
	s.world.SetBlock(2, 65, 2, lower)
	s.world.SetBlock(2, 66, 2, upper)

	use := func() {
		var r mcproto.Writer
		r.WriteVarInt(0)                    // hand
		r.WriteLong(packPosition(2, 65, 2)) // the lower half
		r.WriteVarInt(1)                    // face
		r.WriteFloat(0.5)                   // cursor
		r.WriteFloat(0.5)
		r.WriteFloat(0.5)
		r.WriteBool(false) // not inside the block
		r.WriteBool(false) // did not hit the world border
		r.WriteVarInt(0)   // sequence
		s.handleUseItemOn(mcproto.NewReader(r.Bytes()))
	}

	use()
	if name, props, ok := srv.blockStateName(tab, s.world.Block(2, 65, 2)); !ok || name != "minecraft:oak_door" || props["open"] != "true" {
		t.Errorf("the lower half is %s %v, want an open door", name, props)
	}
	if _, props, ok := srv.blockStateName(tab, s.world.Block(2, 66, 2)); !ok || props["open"] != "true" {
		t.Errorf("the upper half did not open: %v", props)
	}

	// The sound, then closing again.
	want := tab.SoundEventIDs["minecraft:block.wooden_door.open"]
	if want != 0 {
		heard := false
		deadline := time.After(2 * time.Second)
		for !heard {
			select {
			case p := <-got:
				if p.ID == tab.PlayClientboundSoundEffect && soundIDOf(t, p) == want {
					heard = true
				}
			case <-deadline:
				t.Fatal("opening a door made no sound")
			}
		}
	}
	use()
	if _, props, ok := srv.blockStateName(tab, s.world.Block(2, 65, 2)); !ok || props["open"] != "false" {
		t.Errorf("the second click left it %v, want closed", props)
	}
}

// Clicking something that does not open leaves it alone.
func TestOpenableLeavesOtherBlocksAlone(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	stone, _ := srv.blockState(tab, "minecraft:stone", nil)
	s.world.SetBlock(1, 65, 0, stone)

	if s.toggleOpenable(1, 65, 0) {
		t.Error("a stone block was treated as something that opens")
	}
	if got := s.world.Block(1, 65, 0); got != stone {
		t.Errorf("the stone changed to %d", got)
	}
}
