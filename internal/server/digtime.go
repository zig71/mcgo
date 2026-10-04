package server

import (
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Breaking, timed from the game's own numbers.
//
// The hardness table is dumped per state (tools/dump_light/DumpBreak.java): it
// is computed by the block, so the block report cannot carry it. Vanilla says
// -1 for a block no tool can break, and that is the first use of it here: a
// modified client can ask to break bedrock, and the server has to say no.
//
// What is timed is everything about a dig except the decision to break it.
// Vanilla's server times the dig itself and can refuse a STOP that comes in too
// early, but it computes that from the player's enchantments and effects; this
// server tracks neither, so the same check here would refuse a legitimate
// Efficiency V dig and drop the client's prediction of it — the ghosting this
// engine has just been cured of. The client's STOP stays authoritative, and the
// model is used where being slow costs nothing: the cracking the onlookers see,
// the cracking sounds, and what a block drops.

// toolSpeeds are ToolMaterial's speeds: what a correct tool of that tier works
// at. An Efficiency enchantment or a haste effect would multiply them, and
// neither is modelled.
var toolSpeeds = map[string]float32{
	"wooden": 2, "stone": 4, "iron": 6, "diamond": 8, "golden": 12, "netherite": 9,
}

// toolItem is what an item in hand does to a block: the block tag it is the
// correct tool for, the tag of blocks its material is too weak for, and the
// speed it works at.
type toolItem struct {
	class     string // mineable/<class>: what this tool is for
	incorrect string // incorrect_for_<material>_tool: what it is too weak for
	speed     float32
}

// toolItems is the item -> tool table, built from those materials so the
// classes cannot drift from the speeds. It is written out rather than dumped
// because the dump harness cannot read item components ("Components not bound
// yet"), and this is the whole of what vanilla's items contribute to breaking:
// DiggerItem covers the four mineable/* classes at its tier's speed, and
// SwordItem works on #sword_efficient blocks. Shears are their own case, in
// toolSpeed.
var toolItems = func() map[string]toolItem {
	out := make(map[string]toolItem, 7*len(toolSpeeds))
	for material, speed := range toolSpeeds {
		incorrect := "minecraft:incorrect_for_" + material + "_tool"
		for _, class := range []string{"pickaxe", "axe", "shovel", "hoe"} {
			out["minecraft:"+material+"_"+class] = toolItem{
				class: "minecraft:mineable/" + class, incorrect: incorrect, speed: speed,
			}
		}
		out["minecraft:"+material+"_sword"] = toolItem{
			class: "minecraft:sword_efficient", incorrect: incorrect, speed: speed,
		}
	}
	return out
}()

// hardness is a state's hardness, exactly as dumped: times a hundred, and -1
// for a block no tool can break.
func hardness(t proto.Tables, state int32) float32 {
	n := int(state) * 2
	if n < 0 || n+2 > len(t.BreakHardness) {
		return 0
	}
	v := int16(uint16(t.BreakHardness[n])<<8 | uint16(t.BreakHardness[n+1]))
	return float32(v) / 100
}

// tagKey is which tag's member set is wanted, in which protocol version.
type tagKey struct {
	protocol int32
	tag      string
}

// inBlockTag reports whether a block is in a block tag, using the tags the
// server already generates. The member set is built once and kept: the biggest
// of them (mineable/pickaxe) runs to a few hundred entries, and a dig asks this
// on every tick it is running.
func (s *Server) inBlockTag(t proto.Tables, tag, blockName string) bool {
	id, ok := t.BlockIDs[blockName]
	if !ok {
		return false
	}
	key := tagKey{protocol: t.Protocol, tag: tag}
	s.mu.Lock()
	set, ok := s.tagSets[key]
	if !ok {
		members := t.RegistryTags["minecraft:block"][tag]
		set = make(map[int32]bool, len(members))
		for _, m := range members {
			set[m] = true
		}
		if s.tagSets == nil {
			s.tagSets = make(map[tagKey]map[int32]bool)
		}
		s.tagSets[key] = set
	}
	s.mu.Unlock()
	return set[id]
}

// toolSpeed is Player.getDestroySpeed for the item in hand: the tier's speed
// when the item is the block's correct tool, and one otherwise, which is what
// makes a bare hand and the wrong tool as slow as each other. It reports the
// second value for whether the tool is the right one, which is also what the
// drop rule turns on.
func (s *Server) toolSpeed(t proto.Tables, state int32, item world.Item) (float32, bool) {
	name := t.ItemName[item.ID]
	blockName, _, _ := s.blockStateName(t, state)
	if name == "minecraft:shears" {
		// ShearsItem: fifteen on cobweb and leaves, five on wool, and correct
		// for the first two.
		switch {
		case blockName == "minecraft:cobweb", s.inBlockTag(t, "minecraft:leaves", blockName):
			return 15, true
		case s.inBlockTag(t, "minecraft:wool", blockName):
			return 5, false
		}
		return 1, false
	}
	tool, ok := toolItems[name]
	if !ok {
		return 1, false
	}
	// DiggerItem.isCorrectToolForDrops: the block has to be of this tool's
	// class and not one its material is too weak for. The second half is what
	// makes an iron pickaxe the right class for obsidian and still too weak for
	// it — both slow and unrewarded — which vanilla states as a tag rather than
	// a tier number.
	if !s.inBlockTag(t, tool.class, blockName) || s.inBlockTag(t, tool.incorrect, blockName) {
		return 1, false
	}
	return tool.speed, true
}

// destroyProgress is BlockBehaviour.getDestroyProgress: how much of a block one
// tick of digging takes off.
//
//	progress per tick = toolSpeed / hardness / (correctTool ? 30 : 100)
//
// A block breaks when progress * (ticks spent + 1) reaches one, so stone's
// hardness of 1.5 is six ticks with a diamond pickaxe and a hundred and fifty
// by hand. A block with no hardness at all — a torch, a flower — is a progress
// of one, and a hardness of -1 is zero, which no digging finishes.
func (s *Server) destroyProgress(t proto.Tables, state int32, item world.Item) float32 {
	h := hardness(t, state)
	if h < 0 {
		return 0
	}
	if h == 0 {
		return 1
	}
	speed, correct := s.toolSpeed(t, state, item)
	divisor := float32(100)
	if correct {
		divisor = 30
	}
	return speed / h / divisor
}

// startDig begins one: where, when, which way the cracking faces, and the first
// stage for everyone else.
func (s *session) startDig(x, y, z, face int32) {
	if s.world.Block(x, y, z) == s.t.BlockAir {
		return
	}
	s.digActive = true
	s.digPos = [3]int32{x, y, z}
	s.digStart = s.srv.Age()
	s.digDir = face
	s.digStage = -1
	s.tickDig()
}

// endDig forgets the dig in progress and clears the cracking for everyone else.
func (s *session) endDig() {
	if !s.digActive {
		return
	}
	s.digActive = false
	s.digStage = -1
	s.srv.destroyStage(s.t, s.entityID, s.digPos[0], s.digPos[1], s.digPos[2], -1)
}

// tickDig advances the cracking the onlookers see and plays its sound. This is
// the one thing about a dig the server can time for itself: the client doing the
// digging draws its own animation, so a stage that lags it costs nothing, while
// a single stage that never moves reads as a block vanishing with no warning.
func (s *session) tickDig() {
	if !s.digActive {
		return
	}
	x, y, z := s.digPos[0], s.digPos[1], s.digPos[2]
	state := s.world.Block(x, y, z)
	if state == s.t.BlockAir {
		s.digActive = false
		s.digStage = -1
		s.srv.destroyStage(s.t, s.entityID, x, y, z, -1)
		return
	}
	ticks := s.srv.Age() - s.digStart
	progress := s.srv.destroyProgress(s.t, state, s.mainHandItem()) * float32(ticks+1)
	stage := int32(progress * 10)
	if stage > 9 {
		stage = 9
	}
	if stage != s.digStage {
		s.srv.destroyStage(s.t, s.entityID, x, y, z, stage)
		s.digStage = stage
	}
	// The sound of a dig, every tick it runs: 2019 is the crack, and every
	// fourth tick the lower variant, as vanilla alternates them.
	event := int32(2019)
	if ticks%4 == 0 {
		event = 2020
	}
	s.srv.levelEvent(s.t, x, y, z, event, s.digDir)
}

// canHarvest is Block.canHarvestBlock: a block that needs the right tool drops
// only for that tool. Without it a player mines stone with a bare fist and is
// handed cobblestone, which vanilla never does.
func (s *session) canHarvest(state int32) bool {
	// A version whose dump has no tool table claims nothing, so it withholds
	// nothing either.
	if int(state) < 0 || int(state) >= len(s.t.BlockTools) {
		return true
	}
	if s.t.BlockTools[state]&0x40 == 0 {
		return true
	}
	_, correct := s.srv.toolSpeed(s.t, state, s.mainHandItem())
	return correct
}
