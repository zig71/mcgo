package server

import (
	"mcgo/internal/mcproto"
)

// Entity metadata. A packet is a run of entries — a byte index, the value's
// serializer, then the value — ended by a byte of 255. Only the shared flags
// are sent here, which is what makes another player's sneak and sprint visible.
//
// Indexes, flag bits and serializer ids are from Entity and
// EntityDataSerializers in the game's own source.
const (
	metaSharedFlags = 0 // Entity.DATA_SHARED_FLAGS_ID
	// metaSkinParts is Avatar.DATA_PLAYER_MODE_CUSTOMISATION. Indexes are
	// assigned per class tree in field order: Entity defines 8, LivingEntity 7,
	// then Avatar's main hand and this one, so the count before it is 8+7+1.
	metaSkinParts  = 16
	serializerByte = 0 // the first serializer registered, BYTE
	metaTerminator = 255

	flagOnFire     = 1 << 0
	flagSneaking   = 1 << 1
	flagSprinting  = 1 << 3
	flagInvisible  = 1 << 5
	flagFallFlying = 1 << 7
)

// Player input flags, from the Input record the client sends.
const (
	inputFlagShift  = 1 << 5
	inputFlagSprint = 1 << 6
)

// Player command actions, in the order of ServerboundPlayerCommandPacket.Action.
const (
	actionStopSleeping = iota
	actionStartSprinting
	actionStopSprinting
	actionStartRidingJump
	actionStopRidingJump
	actionOpenInventory
	actionStartFallFlying
)

// metadataBody builds a Set Entity Data body carrying a player's shared flags
// and, once their client has told us, which of their skin layers to show.
func metadataBody(entityID int32, flags byte, skinParts byte, haveSkinParts bool) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteByte(metaSharedFlags)
	w.WriteVarInt(serializerByte)
	w.WriteByte(flags)
	if haveSkinParts {
		w.WriteByte(metaSkinParts)
		w.WriteVarInt(serializerByte)
		w.WriteByte(skinParts)
	}
	w.WriteByte(metaTerminator)
	return w.Bytes()
}

// flags is the shared-flags byte for this player.
func (s *session) flags() byte {
	var f byte
	if s.sneaking {
		f |= flagSneaking
	}
	if s.sprinting {
		f |= flagSprinting
	}
	return f
}

// broadcastMetadata tells every other player how this one is posed. Like the
// other entity packets it goes to everyone: this server tracks entities
// globally rather than per view distance.
func (s *session) broadcastMetadata() {
	for _, o := range s.srv.sessionList() {
		if o == s {
			continue
		}
		_ = o.sendQuiet(o.t.PlayClientboundSetEntityData, metadataBody(s.entityID, s.flags(), s.skinParts, s.haveSkinParts))
	}
}

// sendOthersMetadata tells one session how everyone else is posed, which is how
// a player who is sneaking is already crouched when you arrive.
func (s *session) sendOthersMetadata() {
	for _, o := range s.srv.sessionList() {
		if o == s {
			continue
		}
		_ = s.sendQuiet(s.t.PlayClientboundSetEntityData, metadataBody(o.entityID, o.flags(), o.skinParts, o.haveSkinParts))
	}
}

// handlePlayerInput reads the player's input bitfield: whether they are
// sneaking (shift) or sprinting. Neither is a movement packet, so this is the
// only way another player's client learns about it.
func (s *session) handlePlayerInput(r *mcproto.Reader) {
	flags, err := r.ReadByte()
	if err != nil {
		return
	}
	sneaking := flags&inputFlagShift != 0
	sprinting := flags&inputFlagSprint != 0
	s.setPose(sneaking, sprinting)
}

// handlePlayerCommand handles the actions the client sends directly, of which
// only the sprint ones change how a player looks.
func (s *session) handlePlayerCommand(r *mcproto.Reader) {
	if _, err := r.ReadVarInt(); err != nil { // entity id
		return
	}
	action, err := r.ReadVarInt()
	if err != nil {
		return
	}
	switch action {
	case actionStartSprinting:
		s.setPose(s.sneaking, true)
	case actionStopSprinting:
		s.setPose(s.sneaking, false)
	}
}

// setPose records the player's flags and, when they changed, tells the others.
func (s *session) setPose(sneaking, sprinting bool) {
	if s.sneaking == sneaking && s.sprinting == sprinting {
		return
	}
	s.sneaking, s.sprinting = sneaking, sprinting
	s.broadcastMetadata()
}

// entityMetadataBody builds a Set Entity Data body for one entry, whatever its
// value looks like: the index, the serializer, the value, then the terminator.
func (s *session) entityMetadataBody(entityID, index, serializer int32, value func(*mcproto.Writer)) []byte {
	var w mcproto.Writer
	w.WriteVarInt(entityID)
	w.WriteByte(byte(index))
	w.WriteVarInt(serializer)
	value(&w)
	w.WriteByte(metaTerminator)
	return w.Bytes()
}
