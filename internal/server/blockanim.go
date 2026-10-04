package server

import (
	"strings"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Block and container animations.
//
// Two mechanisms, and which one a block uses is a property of the block, not of
// the protocol:
//
//   - a **block event** (ClientboundBlockEventPacket) is a position, two bytes
//     whose meaning the block decides, and the block's registry id. A chest, an
//     ender chest and a shulker box animate their lid from it, carrying the
//     number of openers in the second byte.
//   - a **block state change** for the rest: a barrel's OPEN property, a door's
//     OPEN, a furnace's LIT, an end portal frame's eye. BarrelBlockEntity has no
//     block event at all — its openers counter flips the state.
//
// The sound is always a separate packet, which is why the container sound and
// the animation are sent from the same place but are not the same send.

// blockEvent sends one block event to everyone who can see the position.
func (s *Server) blockEvent(t proto.Tables, x, y, z int32, blockName string, b0, b1 int32) {
	if t.PlayClientboundBlockEvent == 0 {
		return
	}
	id, ok := t.BlockIDs[blockName]
	if !ok {
		return
	}
	var w mcproto.Writer
	w.WriteLong(packPosition(int(x), int(y), int(z)))
	w.WriteByte(byte(b0))
	w.WriteByte(byte(b1))
	w.WriteVarInt(id)
	body := w.Bytes()
	s.viewersAt(float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundBlockEvent, body)
	})
}

// containerOpened plays the opening or closing animation for a container block.
//
// The count is the number of players who have it open: a chest's second byte is
// that count, and the client shuts the lid only when it reaches zero. Two
// players at one chest therefore both see it stay open when the first closes,
// which a bare open/close flag would get wrong.
func (s *Server) containerOpened(t proto.Tables, w *world.World, x, y, z int32, open bool) {
	name, _, ok := s.blockStateName(t, w.Block(x, y, z))
	if !ok {
		return
	}
	pos := [3]int32{x, y, z}
	s.animMu.Lock()
	if s.openCounts == nil {
		s.openCounts = map[[3]int32]int32{}
	}
	count := s.openCounts[pos]
	if open {
		count++
	} else if count > 0 {
		count--
	}
	if count == 0 {
		delete(s.openCounts, pos)
	} else {
		s.openCounts[pos] = count
	}
	s.animMu.Unlock()

	switch {
	case name == "minecraft:barrel":
		s.setBarrelOpen(t, w, x, y, z, count > 0)
	case name == "minecraft:chest", name == "minecraft:ender_chest", strings.HasSuffix(name, "_shulker_box"):
		s.blockEvent(t, x, y, z, name, 1, count)
	}
}

// useEndPortalFrame fills an end portal frame with an eye of ender.
//
// The eye is part of the frame's block state rather than a block entity, so the
// client draws it from the block update, and the sound is the frame's own. The
// eye is spent in survival.
func (s *session) useEndPortalFrame(name string, x, y, z int32, item, seq int32) bool {
	if name != "minecraft:ender_eye" {
		return false
	}
	blockName, props, ok := s.srv.blockStateName(s.t, s.world.Block(x, y, z))
	if !ok || blockName != "minecraft:end_portal_frame" || props["eye"] != "false" {
		return false
	}
	props["eye"] = "true"
	state, ok := s.srv.blockState(s.t, blockName, props)
	if !ok {
		return false
	}
	s.setBlock(x, y, z, state, seq)
	s.srv.playBlockSound(s.t, x, y, z, blockName, "fill")
	s.consumeHeldItem(item, 0)
	return true
}

// setBarrelOpen flips a barrel's OPEN property. It is a block state change, not
// a block event, and the world records it so a chunk resent later still shows
// the lid open.
func (s *Server) setBarrelOpen(t proto.Tables, w *world.World, x, y, z int32, open bool) {
	name, props, ok := s.blockStateName(t, w.Block(x, y, z))
	if !ok || name != "minecraft:barrel" {
		return
	}
	if props == nil {
		props = map[string]string{}
	}
	props["open"] = "false"
	if open {
		props["open"] = "true"
	}
	state, ok := s.blockState(t, name, props)
	if !ok || state == w.Block(x, y, z) {
		return
	}
	s.srvSetBlock(w, x, y, z, state)
}
