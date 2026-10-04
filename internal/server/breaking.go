package server

import (
	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Breaking blocks.
//
// The client works out how long a block takes from its own copy of the hardness
// tables and only tells the server when it has finished, so the *timing* is
// already right. What the server owes the client is the drop, which in creative
// mode is nothing at all, and the cracking texture on the block for whoever
// else is watching.

// blockDrops is what a broken block leaves behind, where that is not simply the
// block's own item. Vanilla reads this from a loot table per block; this is a
// table for the blocks a player actually breaks, and anything not in it drops
// itself.
var blockDrops = map[string][]string{
	"minecraft:stone":                 {"minecraft:cobblestone"},
	"minecraft:grass_block":           {"minecraft:dirt"},
	"minecraft:dirt_path":             {"minecraft:dirt"},
	"minecraft:farmland":              {"minecraft:dirt"},
	"minecraft:mycelium":              {"minecraft:dirt"},
	"minecraft:podzol":                {"minecraft:dirt"},
	"minecraft:coal_ore":              {"minecraft:coal"},
	"minecraft:deepslate_coal_ore":    {"minecraft:coal"},
	"minecraft:iron_ore":              {"minecraft:raw_iron"},
	"minecraft:deepslate_iron_ore":    {"minecraft:raw_iron"},
	"minecraft:copper_ore":            {"minecraft:raw_copper"},
	"minecraft:gold_ore":              {"minecraft:raw_gold"},
	"minecraft:deepslate_gold_ore":    {"minecraft:raw_gold"},
	"minecraft:diamond_ore":           {"minecraft:diamond"},
	"minecraft:deepslate_diamond_ore": {"minecraft:diamond"},
	"minecraft:emerald_ore":           {"minecraft:emerald"},
	"minecraft:redstone_ore":          {"minecraft:redstone"},
	"minecraft:lapis_ore":             {"minecraft:lapis_lazuli"},
	"minecraft:nether_quartz_ore":     {"minecraft:quartz"},
	"minecraft:bookshelf":             {"minecraft:book"},
	"minecraft:sea_lantern":           {"minecraft:prismarine_crystals"},
	"minecraft:snow_block":            {"minecraft:snowball"},
	"minecraft:clay":                  {"minecraft:clay_ball"},
	"minecraft:glass":                 {}, // a broken pane leaves nothing
	"minecraft:glass_pane":            {},
	"minecraft:ice":                   {},
	"minecraft:packed_ice":            {},
	"minecraft:oak_leaves":            {},
	"minecraft:spruce_leaves":         {},
	"minecraft:birch_leaves":          {},
	"minecraft:jungle_leaves":         {},
	"minecraft:acacia_leaves":         {},
	"minecraft:dark_oak_leaves":       {},
	"minecraft:short_grass":           {},
	"minecraft:tall_grass":            {},
	"minecraft:fern":                  {},
}

// blockDropsFor is what breaking a block leaves behind. A block that drops
// nothing has an empty, non-nil entry; a block that is not named drops itself.
func (s *Server) blockDropsFor(t proto.Tables, blockName string) []world.Item {
	names, named := blockDrops[blockName]
	if !named {
		item, ok := t.BlockItems[blockName]
		if !ok {
			return nil
		}
		return []world.Item{{ID: item, Count: 1}}
	}
	out := make([]world.Item, 0, len(names))
	for _, name := range names {
		id, ok := t.ItemIDs[name]
		if !ok {
			continue
		}
		out = append(out, world.Item{ID: id, Count: 1})
	}
	return out
}

// dropBrokenBlock leaves a block's drops on the ground. Vanilla drops them at
// the block's centre; the item entity carries them from there, which is how a
// survival player ends up with them.
func (s *Server) dropBrokenBlock(t proto.Tables, x, y, z int32, blockName string) {
	for _, item := range s.blockDropsFor(t, blockName) {
		s.spawnItemEntity(t, item, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5)
	}
}

// destroyStage tells everyone else how far along a break is. Progress runs 0 to
// 9 through the cracking texture, and -1 clears it.
//
// The stages are not timed: the client that is doing the breaking draws its own
// animation from the block's hardness, and this only spares the onlookers the
// sight of a block vanishing with no warning.
func (s *Server) destroyStage(t proto.Tables, breaker int32, x, y, z int32, progress int32) {
	if t.PlayClientboundBlockDestruction == 0 {
		return
	}
	var w mcproto.Writer
	w.WriteVarInt(breaker)
	w.WriteLong(packPosition(int(x), int(y), int(z)))
	w.WriteByte(byte(progress & 0xff))
	body := w.Bytes()

	s.viewersAt(float64(x), float64(y), float64(z), func(sess *session) {
		if sess.entityID == breaker {
			return // the client doing it draws its own
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundBlockDestruction, body)
	})
}
