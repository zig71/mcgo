package server

import (
	"log"

	"mcgo/internal/mcproto"
	"mcgo/internal/world"
)

// handlePickItemFromBlock answers a middle-click on a block: the client sends
// the block it is looking at, and the item that places it goes into the
// player's hand.
//
// The client does the ray cast itself and sends the position, so there is none
// to do here — only the block-to-item lookup.
func (s *session) handlePickItemFromBlock(r *mcproto.Reader) {
	pos, err := r.ReadLong()
	if err != nil {
		return
	}
	if _, err := r.ReadByte(); err != nil { // whether to include the block's data
		return
	}
	x, y, z := unpackPosition(pos)
	if s.pickItemPos(x, y, z) {
		if err := s.sendHeldSlot(); err != nil {
			return
		}
		_ = s.sendInventory()
	}
}

// pickItemPos puts the item for the block at a position into the player's hand.
func (s *session) pickItemPos(x, y, z int32) bool {
	state := s.world.Block(x, y, z)
	if state == s.t.BlockAir {
		return false
	}
	name, _, ok := s.srv.blockStateName(s.t, state)
	if !ok {
		return false
	}
	item, ok := s.t.BlockItems[name]
	if !ok {
		return false
	}
	return s.pickItem(item)
}

// pickItem is ServerGamePacketListenerImpl.tryPickItem: if a slot already holds
// the item it is selected — or swapped into the hand, if it was not on the
// hotbar — and otherwise, having infinite materials in creative, the item goes
// straight into the selected slot. It reports whether the inventory changed.
func (s *session) pickItem(item int32) bool {
	if item == 0 {
		return false
	}
	for i, it := range s.inv {
		if it.ID != item {
			continue
		}
		if i >= 27 { // already on the hotbar: just select it
			if s.heldSlot != int32(i-27) {
				s.heldSlot = int32(i - 27)
				return true
			}
			return false
		}
		// Elsewhere in the inventory: swap it into the hand.
		held := 27 + s.heldSlot
		s.inv[i], s.inv[held] = s.inv[held], s.inv[i]
		return true
	}
	held := 27 + s.heldSlot
	if s.inv[held].ID == item && s.inv[held].Count == 64 {
		return false
	}
	s.inv[held] = world.Item{ID: item, Count: 1}
	log.Printf("picked item %d into hotbar slot %d", item, s.heldSlot)
	return true
}
