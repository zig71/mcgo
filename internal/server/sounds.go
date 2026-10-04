package server

import (
	"math/rand/v2"
	"strings"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
)

// Sound.
//
// Only the block sounds a player hears themselves make are here: breaking and
// placing. Vanilla takes the sound from the block's sound type, which this
// server's block table does not carry, so the group is guessed from the block's
// name.

// soundSourceBlocks is SoundSource.BLOCKS, which is what decides the volume
// slider a sound follows.
const soundSourceBlocks = 4

// soundGroupOf is which of vanilla's block sound groups a block belongs to.
// It is a guess from the name, and the fallback is stone.
func soundGroupOf(blockName string) string {
	name := strings.TrimPrefix(blockName, "minecraft:")
	switch {
	case strings.Contains(name, "planks"), strings.Contains(name, "log"),
		strings.Contains(name, "wood"), strings.Contains(name, "fence"),
		strings.Contains(name, "door"), strings.Contains(name, "sign"),
		name == "crafting_table", name == "bookshelf", name == "chest":
		return "wood"
	case strings.Contains(name, "glass"), name == "glowstone", name == "beacon":
		return "glass"
	case strings.Contains(name, "wool"), strings.Contains(name, "carpet"):
		return "wool"
	case strings.Contains(name, "sand"), name == "soul_sand":
		return "sand"
	case strings.Contains(name, "gravel"):
		return "gravel"
	case strings.Contains(name, "grass"), strings.Contains(name, "leaves"),
		strings.Contains(name, "dirt"), strings.Contains(name, "crop"),
		strings.Contains(name, "flower"), strings.Contains(name, "sapling"):
		return "grass"
	}
	return "stone"
}

// playBlockSound plays a block group's break or place sound at a position.
func (s *Server) playBlockSound(t proto.Tables, x, y, z int32, blockName, action string) {
	volume := float32(0.25)
	if action == "step" {
		// A footstep is quieter than a block being broken or placed.
		volume = 0.15
	}
	s.playBlockSoundAt(t, x, y, z, blockName, action, volume)
}

// playBlockSoundAt is playBlockSound with the volume chosen by the caller.
func (s *Server) playBlockSoundAt(t proto.Tables, x, y, z int32, blockName, action string, volume float32) {
	group := soundGroupOf(blockName)
	id, ok := t.SoundEventIDs["minecraft:block."+group+"."+action]
	if !ok || t.PlayClientboundSoundEffect == 0 {
		return
	}
	s.playSound(t, id, soundSourceBlocks, float32(x)+0.5, float32(y)+0.5, float32(z)+0.5, volume, 1)
}

// playSound sends a sound for someone to hear at a position. The coordinates
// are in eighths of a block, which is how the packet carries them.
func (s *Server) playSound(t proto.Tables, soundID, source int32, x, y, z, volume, pitch float32) {
	var w mcproto.Writer
	// A sound is sent as a holder, whose codec writes the registry id plus one
	// so that zero can mean an inline sound. The bare id plays the neighbour.
	w.WriteVarInt(soundID + 1)
	w.WriteVarInt(source)
	w.WriteInt(int32(x * 8))
	w.WriteInt(int32(y * 8))
	w.WriteInt(int32(z * 8))
	w.WriteFloat(volume)
	w.WriteFloat(pitch)
	w.WriteLong(rand.Int64())
	body := w.Bytes()

	s.viewersAt(float64(x), float64(y), float64(z), func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundSoundEffect, body)
	})
}

// mobSoundGroup is the group a mob's sounds are registered under: a zombie's
// hurt sound is minecraft:entity.zombie.hurt. A mob with no sounds of its own
// falls back to the generic ones, which is what vanilla does for the rest.
func (s *Server) mobSoundGroup(t proto.Tables, entityName string) string {
	if entityName == "" {
		return "generic"
	}
	return strings.TrimPrefix(entityName, "minecraft:")
}

// playMobSound plays one of a mob's sounds, falling back to the generic one.
// Vanilla's creepers, for instance, have no ambient sound at all.
func (s *Server) playMobSound(t proto.Tables, entityName, action string, x, y, z float64, volume, pitch float32) {
	group := s.mobSoundGroup(t, entityName)
	id, ok := t.SoundEventIDs["minecraft:entity."+group+"."+action]
	if !ok {
		id, ok = t.SoundEventIDs["minecraft:entity.generic."+action]
	}
	if !ok || t.PlayClientboundSoundEffect == 0 {
		return
	}
	s.playSound(t, id, soundSourceHostile, float32(x), float32(y), float32(z), volume, pitch)
}

// soundSourceHostile is SoundSource.HOSTILE, the channel mob noise belongs on.
const soundSourceHostile = 5

// soundSourceNeutral is SoundSource.NEUTRAL, which is where a thrown thing's
// sound and the rest of the world's own noise belong.
const soundSourceNeutral = 6

// soundSourcePlayers is SoundSource.PLAYERS, which is the channel for what a
// player's own gear does: a bow's shot, an arrow's impact.
const soundSourcePlayers = 7

// playMobSoundFrom is playMobSound bound to the mob: the client keeps the sound
// with the entity, so a mob that walks away takes its noise with it. Vanilla
// sends a mob's own noises this way; a positional sound is for things that stay
// where they are.
func (s *Server) playMobSoundFrom(t proto.Tables, entityName, action string, entityID int32, x, y, z float64, volume, pitch float32) {
	group := s.mobSoundGroup(t, entityName)
	id, ok := t.SoundEventIDs["minecraft:entity."+group+"."+action]
	if !ok {
		id, ok = t.SoundEventIDs["minecraft:entity.generic."+action]
	}
	if !ok || t.PlayClientboundSoundEntity == 0 {
		return
	}
	s.playSoundEntity(t, id, soundSourceHostile, entityID, float32(x), float32(y), float32(z), volume, pitch)
}

// playSoundEntity sends a sound bound to an entity: the same fields as
// playSound, with the entity's id where the coordinates would be. It still goes
// to the players near the entity, which is who vanilla tracks the entity to.
func (s *Server) playSoundEntity(t proto.Tables, soundID, source, entityID int32, x, y, z, volume, pitch float32) {
	var w mcproto.Writer
	// A sound is a holder: its registry id plus one, so zero can mean an inline
	// sound. The tail is the same as the positional packet's.
	w.WriteVarInt(soundID + 1)
	w.WriteVarInt(source)
	w.WriteVarInt(entityID)
	w.WriteFloat(volume)
	w.WriteFloat(pitch)
	w.WriteLong(rand.Int64())
	body := w.Bytes()

	s.viewersAt(float64(x), float64(y), float64(z), func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundSoundEntity, body)
	})
}

// ambientTicks is how long a mob waits before making a noise of its own.
const ambientMinTicks, ambientMaxTicks = 80, 320

// containerSound plays the sound a container makes when it is opened or closed.
// Which sound it is comes from the block, so a barrel does not creak like a
// chest; a container with no sound of its own (a furnace, a crafting table)
// plays nothing, which is also what vanilla does.
func (s *session) containerSound(t proto.Tables, x, y, z int32, action string) {
	// The session's own world: the server's worldFor makes a fresh one, so a
	// block the player is standing at would look like air.
	name, _, ok := s.srv.blockStateName(t, s.world.Block(x, y, z))
	if !ok {
		return
	}
	group := ""
	short := strings.TrimPrefix(name, "minecraft:")
	switch {
	case short == "chest", short == "trapped_chest":
		group = "chest"
	case short == "barrel":
		group = "barrel"
	case short == "ender_chest":
		group = "ender_chest"
	case strings.Contains(short, "shulker_box"):
		group = "shulker_box"
	}
	if group == "" {
		return
	}
	id, ok := t.SoundEventIDs["minecraft:block."+group+"."+action]
	if !ok || t.PlayClientboundSoundEffect == 0 {
		return
	}
	s.srv.playSound(t, id, soundSourceBlocks, float32(x)+0.5, float32(y)+0.5, float32(z)+0.5, 0.5, 1)
}
