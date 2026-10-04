package server

import (
	"log"
	"strings"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// An anvil's window is the item being worked on, an addition, and a result.
const (
	anvilInputSlot  = 0
	anvilAddSlot    = 1
	anvilResultSlot = 2
	// anvilCostProperty is the container data slot the client reads the
	// experience cost from; it shows "Too Expensive" above 40.
	anvilCostProperty = 0
	// anvilMaxNameLength is the limit AnvilMenu.validateName enforces.
	anvilMaxNameLength = 50
)

// isAnvilTitle reports whether an open menu is an anvil. An anvil has three
// slots like a furnace, so the title is what tells them apart.
func isAnvilTitle(title string) bool { return title == "container.repair" }

// anvilResult works out what the anvil should show, and what it costs.
//
// The name is a component, so a rename is a component change like any other,
// and the item keeps every other component it had.
//
// This is a faithful subset of AnvilMenu.createResult for the items this
// server models. Vanilla combines damage and enchantments, but items here have
// neither, and vanilla itself produces no result for an undamaged, unenchanted
// pair — so the meaningful operation left is renaming, which is also the one
// players use an anvil for in creative.
func anvilResult(t proto.Tables, left, addition world.Item, name string) (world.Item, int32) {
	if left.Empty() || !addition.Empty() {
		return world.Item{}, 0
	}
	trimmed := filterAnvilName(name)
	current := itemName(t, left)
	if trimmed != "" && trimmed != current {
		out := left
		setItemName(t, &out, trimmed)
		return out, 1
	}
	if trimmed == "" && current != "" {
		out := left
		setItemName(t, &out, "")
		return out, 1
	}
	return world.Item{}, 0
}

// filterAnvilName drops the characters vanilla strips from a name and rejects
// anything longer than the limit, which invalidates the whole rename.
func filterAnvilName(name string) string {
	var b strings.Builder
	for _, r := range name {
		// StringUtil.filterText: no section signs, no control characters.
		if r == 0x00A7 || r < 0x20 || r == 0x7F {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > anvilMaxNameLength {
		return ""
	}
	return out
}

// refreshAnvilResult recomputes the result slot and cost of an open anvil.
func (s *session) refreshAnvilResult(items []world.Item) {
	if !isAnvilTitle(s.containerTitle) || len(items) < 3 {
		return
	}
	result, cost := anvilResult(s.t, items[anvilInputSlot], items[anvilAddSlot], s.anvilName)
	items[anvilResultSlot] = result
	s.anvilCost = cost
}

// anvilClick handles a click on the anvil result slot. As with the crafting
// table the slot holds a preview, so taking it consumes the input.
func (s *session) anvilClick(items []world.Item, w []world.Item, mode int32, carried *world.Item) bool {
	if !isAnvilTitle(s.containerTitle) {
		return false
	}
	result, _ := anvilResult(s.t, items[anvilInputSlot], items[anvilAddSlot], s.anvilName)
	if result.Empty() {
		return false
	}

	take := func(dst *world.Item) bool {
		if dst.Empty() {
			*dst = result
		} else if dst.Same(result) && dst.Count+result.Count <= 64 {
			dst.Count += result.Count
		} else {
			return false
		}
		// The item on the anvil is used up; only a rename leaves the
		// addition slot alone, which is AnvilMenu.onTake's onlyRenaming path.
		items[anvilInputSlot] = world.Item{}
		s.anvilName = ""
		return true
	}

	size := int(s.containerSize)
	switch mode {
	case 0:
		if take(carried) {
			log.Printf("anvil produced %d x item %d (cost %d)", result.Count, result.ID, s.anvilCost)
			s.refreshAnvilResult(items)
			return true
		}
	case 1:
		crafted := 0
		for crafted < 64 {
			res, _ := anvilResult(s.t, items[anvilInputSlot], items[anvilAddSlot], s.anvilName)
			if res.Empty() {
				break
			}
			if !addToInventory(w[size:], res) {
				break
			}
			items[anvilInputSlot] = world.Item{}
			s.anvilName = ""
			crafted++
		}
		s.refreshAnvilResult(items)
		return true
	}
	return false
}

// sendAnvilCost sends the experience cost the client draws beside the anvil.
func (s *session) sendAnvilCost(cost int32) {
	if !isAnvilTitle(s.containerTitle) {
		return
	}
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteShort(anvilCostProperty)
	w.WriteShort(int16(cost))
	if err := s.sendQuiet(s.t.PlayClientboundContainerSetData, w.Bytes()); err != nil {
		return
	}
}

// handleRenameItem applies the name the player typed at the anvil.
func (s *session) handleRenameItem(r *mcproto.Reader) {
	name, err := r.ReadString()
	if err != nil {
		return
	}
	s.winMu.Lock()
	defer s.winMu.Unlock()
	if !s.containerOpen || !isAnvilTitle(s.containerTitle) {
		return
	}
	if filterAnvilName(name) == s.anvilName && name != s.anvilName {
		return // the name was rejected, so nothing changed
	}
	s.anvilName = filterAnvilName(name)
	x, y, z := s.containerPos[0], s.containerPos[1], s.containerPos[2]
	items := s.srv.containers.Get(x, y, z, int(s.containerSize))
	s.refreshAnvilResult(items)
	s.srv.containers.Put(x, y, z, items)
	s.sendAnvilCost(s.anvilCost)
	s.containerState++
	s.sendContainerContentLocked(items)
}
