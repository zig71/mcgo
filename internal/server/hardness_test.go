package server

import (
	"math"
	"testing"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// Bedrock cannot be broken: the hardness table dumped from the game says -1, and
// a client that asks anyway is refused. This is the first use of that table —
// without it a modified client could break anything, including the floor of the
// world.
func TestUnbreakableBlocksStay(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	srv.register(s)
	tab := v777.Tables
	if len(tab.BreakHardness) == 0 {
		t.Fatal("no hardness table for 26.3")
	}
	bedrock, ok := srv.blockState(tab, "minecraft:bedrock", nil)
	if !ok {
		t.Fatal("no bedrock block state")
	}
	if !unbreakable(tab, bedrock) {
		t.Error("the table does not call bedrock unbreakable")
	}
	s.world.SetBlock(4, 65, 4, bedrock)
	s.handlePlayerAction(actionPacket(3, 4, 65, 4, 1, 3))
	if s.world.Block(4, 65, 4) != bedrock {
		t.Error("bedrock was broken")
	}

	// An ordinary block still breaks, and stone says it needs the right tool.
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	if unbreakable(tab, stone) {
		t.Error("the table calls stone unbreakable")
	}
	if tab.BlockTools[stone]&0x40 == 0 {
		t.Error("stone does not require a tool, which the dump says it does")
	}
	s.world.SetBlock(5, 65, 4, stone)
	s.handlePlayerAction(actionPacket(3, 5, 65, 4, 1, 4))
	if s.world.Block(5, 65, 4) != tab.BlockAir {
		t.Error("stone survived a break")
	}
}

// Breaking takes the time the game says it takes. Progress per tick is
// toolSpeed / hardness / (correctTool ? 30 : 100), so stone's hardness of 1.5
// is six ticks with a diamond pickaxe, twenty-three with a wooden one and a
// hundred and fifty by hand — the familiar figures — and obsidian's fifty is
// nine and a half seconds with a diamond pickaxe.
//
// The last case is the material gate: an iron pickaxe is the right *class* for
// obsidian and still not the right tool, because obsidian is in
// #incorrect_for_iron_tool. Vanilla states that as a tag rather than a tier, and
// it is worth a case because getting it wrong makes iron mine obsidian as if it
// were diamond.
func TestBreakTime(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	if len(tab.BreakHardness) == 0 {
		t.Fatal("no hardness table for 26.3")
	}
	state := func(name string) int32 {
		st, ok := srv.blockState(tab, name, nil)
		if !ok {
			t.Fatalf("no %s block state", name)
		}
		return st
	}
	item := func(name string) world.Item {
		id, ok := tab.ItemIDs[name]
		if !ok {
			t.Fatalf("no %s item", name)
		}
		return world.Item{ID: id, Count: 1}
	}
	stone := state("minecraft:stone")
	obsidian := state("minecraft:obsidian")
	if h := hardness(tab, stone); h != 1.5 {
		t.Errorf("stone's hardness is %v, want 1.5", h)
	}
	if h := hardness(tab, obsidian); h != 50 {
		t.Errorf("obsidian's hardness is %v, want 50", h)
	}

	cases := []struct {
		what  string
		state int32
		item  world.Item
		ticks int
	}{
		{"stone with a diamond pickaxe", stone, item("minecraft:diamond_pickaxe"), 6},
		{"stone with a wooden pickaxe", stone, item("minecraft:wooden_pickaxe"), 23},
		{"stone with a sword", stone, item("minecraft:diamond_sword"), 150},
		{"stone by hand", stone, world.Item{}, 150},
		{"obsidian with a diamond pickaxe", obsidian, item("minecraft:diamond_pickaxe"), 188},
		{"obsidian with an iron pickaxe", obsidian, item("minecraft:iron_pickaxe"), 5000},
	}
	for _, c := range cases {
		progress := srv.destroyProgress(tab, c.state, c.item)
		if progress <= 0 {
			t.Errorf("%s: progress is %v, want a positive one", c.what, progress)
			continue
		}
		// A block goes when progress * (ticks spent + 1) reaches one.
		if got := int(math.Ceil(float64(1 / progress))); got != c.ticks {
			t.Errorf("%s takes %d ticks, want %d (progress %v)", c.what, got, c.ticks, progress)
		}
	}

	// A block with no hardness at all is a single tick, and one that no tool
	// can break never gets anywhere.
	if got := srv.destroyProgress(tab, state("minecraft:torch"), world.Item{}); got != 1 {
		t.Errorf("a torch's progress is %v, want 1", got)
	}
	if got := srv.destroyProgress(tab, state("minecraft:bedrock"), item("minecraft:diamond_pickaxe")); got != 0 {
		t.Errorf("bedrock's progress is %v, want 0", got)
	}
}
