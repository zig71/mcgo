package server

import (
	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// setEquipmentBody builds a Set Equipment body for one slot. Vanilla packs
// several slots into one packet, marking "another entry follows" with the high
// bit of the slot byte; one entry needs no such flag.
//
// Slot ids are EquipmentSlot's order: 0 main hand, 1 off hand, 2 feet,
// 3 legs, 4 chest, 5 head, 6 body.
func (s *session) setEquipmentBody(entityID int32, slot byte, item world.Item) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteByte(slot & 0x7f) // high bit clear: the last (only) entry
	s.writeSlot(&w, item)
	return w.Bytes()
}

// mainHandItem is what the player is holding, which is what other players see
// in their hand.
func (s *session) mainHandItem() world.Item {
	if s.heldSlot < 0 || s.heldSlot > 8 {
		return world.Item{}
	}
	return s.inv[27+s.heldSlot]
}

// broadcastMainHand tells every other player what this one is holding.
//
// It goes to everyone, like Add Entity and the movement broadcasts: this server
// tracks entities globally rather than per view distance, and filtering
// equipment by distance here would leave a distant player holding nothing while
// their body is fully visible. Distance culling belongs together with entity
// tracking, not half-applied here.
func (s *session) broadcastMainHand() {
	for _, o := range s.srv.sessionList() {
		if o == s {
			continue
		}
		// Built per recipient: the custom-name component id differs by version.
		_ = o.sendQuiet(o.t.PlayClientboundSetEquipment,
			o.setEquipmentBody(s.entityID, slotMainHand, s.mainHandItem()))
	}
}

// sendOthersEquipment tells one session what everybody else is holding, which
// is how a player appears already holding something when you join.
func (s *session) sendOthersEquipment() {
	for _, o := range s.srv.sessionList() {
		if o == s {
			continue
		}
		s.sendQuiet(s.t.PlayClientboundSetEquipment,
			s.setEquipmentBody(o.entityID, slotMainHand, o.mainHandItem()))
	}
}

const slotMainHand byte = 0

// Swing animations. The animation itself is item data, and no item in this
// version overrides it, so every swing uses SwingAnimation's default: a
// "whack" of six ticks.
const (
	swingAnimationWhack   byte  = 1
	swingAnimationDefault int32 = 6
)

// swingBody builds a Swing Animation body: the entity, the hand, then the
// animation (its type, then its duration).
func swingBody(entityID int32) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteByte(0) // InteractionHand.MAIN_HAND
	w.WriteByte(swingAnimationWhack)
	w.WriteVarInt(swingAnimationDefault)
	return w.Bytes()
}

// animateBody is the older form still used by 26.2 and 1.21.8, where a swing is
// an Animate packet with action 0.
func animateBody(entityID int32) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteByte(0) // SWING_MAIN_HAND
	return w.Bytes()
}

// swingPacket picks the packet and body a swing takes in a version. 26.3 has a
// Swing Animation packet; earlier versions express the same thing as an Animate
// with action 0. A version with neither has no way to show it.
func swingPacket(t proto.Tables, entityID int32) (int32, []byte, bool) {
	switch {
	case t.PlayClientboundSwingAnimation != 0:
		return t.PlayClientboundSwingAnimation, swingBody(entityID), true
	case t.PlayClientboundAnimate != 0:
		return t.PlayClientboundAnimate, animateBody(entityID), true
	}
	return 0, nil, false
}

// broadcastSwing shows the player's arm swinging to everyone else. The client
// animates its own arm, so the packet is only for the other players — and the
// version decides how it is expressed: 26.3 has a Swing Animation packet,
// earlier versions send Animate.
func (s *session) broadcastSwing() {
	id, body, ok := swingPacket(s.t, s.entityID)
	if !ok {
		return
	}
	for _, o := range s.srv.sessionList() {
		if o == s || !o.seesPos(s.posX, s.posY, s.posZ) {
			continue
		}
		_ = o.sendQuiet(id, body)
	}
}
