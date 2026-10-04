package server

import (
	"bytes"
	"log"

	"mcgo/internal/mcproto"
	"mcgo/internal/nbt"
	"mcgo/internal/world"
)

// lecternTitle is the lectern menu's title key, which is how an open window is
// recognised as one.
const lecternTitle = "container.lectern"

// lecternPageProperty is the single container data property a lectern screen
// reads: the page it is showing. The count of pages is not sent — the client
// takes it from the book in the slot.
const lecternPageProperty = 0

// isBookName reports whether an item is one a lectern takes. Vanilla tests for
// the book's content component; the item name implies the same thing, and it is
// what this server can see without decoding that component.
func isBookName(name string) bool {
	return name == "minecraft:written_book" || name == "minecraft:writable_book"
}

// bookPageCount is LecternBlockEntity.getPageCount: how many pages the book's
// content component holds.
//
// The component's value is an NBT compound with a "pages" list, but how it is
// framed — a named root or a bare payload — depends on the component's codec,
// so both are tried rather than one assumed. A value this cannot read gives no
// page count, and the page is then only clamped at zero.
func bookPageCount(item world.Item) int {
	for _, c := range item.Components {
		if c.Removed || len(c.Value) == 0 {
			continue
		}
		if n := pageListLen(c.Value); n > 0 {
			return n
		}
	}
	return 0
}

// pageListLen reads the length of the "pages" list in an NBT compound, or 0
// when the value is not one this can read.
func pageListLen(value []byte) int {
	// Which framing a component's value uses is not visible from here: a bare
	// payload, a type byte and a payload (which is what the banner patterns
	// component carries), and a named root are all worth trying.
	if tag, err := nbt.ReadCompound(bytes.NewReader(value)); err == nil {
		if n := pagesIn(tag); n > 0 {
			return n
		}
	}
	if len(value) > 0 && nbt.Type(value[0]) == nbt.Compound {
		if tag, err := nbt.ReadCompound(bytes.NewReader(value[1:])); err == nil {
			if n := pagesIn(tag); n > 0 {
				return n
			}
		}
	}
	if _, tag, err := nbt.Read(bytes.NewReader(value)); err == nil {
		if n := pagesIn(tag); n > 0 {
			return n
		}
	}
	return 0
}

// pagesIn is the length of a compound's "pages" list, or 0.
func pagesIn(tag nbt.Tag) int {
	if tag.Type != nbt.Compound {
		return 0
	}
	pages, ok := tag.Comp["pages"]
	if !ok || pages.Type != nbt.List {
		return 0
	}
	return len(pages.List)
}

// heldItem is the whole stack in the selected slot, components included, which
// is what has to travel to a lectern. The hotbar sits at the end of the
// inventory array.
func (s *session) heldItem() world.Item {
	if s.heldSlot < 0 || s.heldSlot > 8 {
		return world.Item{}
	}
	return s.inv[27+s.heldSlot]
}

// useLectern puts a book on a lectern, or opens the one it already holds. It
// reports whether it handled the use, so the caller stops before the generic
// container path — a lectern is in the container table, and opening it that way
// would leave the page unset.
func (s *session) useLectern(x, y, z int32) bool {
	if s.blockName(x, y, z) != "minecraft:lectern" {
		return false
	}
	book := s.srv.containers.Get(x, y, z, 1)[0]
	if !book.Empty() {
		s.openLectern(x, y, z, book)
		return true
	}
	held := s.heldItem()
	name, ok := s.t.ItemName[held.ID]
	if !ok || !isBookName(name) {
		return false
	}
	// One book leaves the hand and goes onto the lectern, which then draws it.
	// The components ride along, so the pages the client shows are the ones the
	// item carried.
	s.srv.containers.Put(x, y, z, []world.Item{{ID: held.ID, Count: 1, Components: held.Components}})
	s.setLecternBook(x, y, z, true)
	s.consumeHeldItem(held.ID, 0)
	// item.book.put, which is what vanilla plays as the book lands.
	if id, ok := s.t.SoundEventIDs["minecraft:item.book.put"]; ok {
		s.srv.playSound(s.t, id, soundSourceBlocks, float32(x)+0.5, float32(y)+0.5, float32(z)+0.5, 1, 1)
	}
	return true
}

// setLecternBook turns a lectern's has_book property on or off, which is what
// makes the client draw a book on it. The block's other properties are kept.
func (s *session) setLecternBook(x, y, z int32, has bool) {
	name, props, ok := s.srv.blockStateName(s.t, s.world.Block(x, y, z))
	if !ok || name != "minecraft:lectern" {
		return
	}
	if props == nil {
		props = map[string]string{}
	}
	props["has_book"] = "false"
	if has {
		props["has_book"] = "true"
	}
	state, ok := s.srv.blockState(s.t, name, props)
	if !ok {
		return
	}
	s.setBlockUpdate(x, y, z, state)
}

// openLectern shows the book a lectern holds: the lectern menu, with the book
// in its one slot and the page as a data property. The client draws the book's
// pages itself, from the item's components.
func (s *session) openLectern(x, y, z int32, book world.Item) {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	spec, ok := s.t.ContainerMenus[s.world.Block(x, y, z)]
	if !ok {
		return
	}
	s.containerOpen = true
	s.containerPos = [3]int32{x, y, z}
	s.containerState = 0
	s.containerSize = spec.Slots
	s.containerKind = spec.ID
	s.containerTitle = spec.Title
	s.carried = world.Item{}
	s.lecternPage = 0
	s.lecternPages = int32(bookPageCount(book))

	var title mcproto.Writer
	title.WriteBytes(nbtTranslate(spec.Title))
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteVarInt(spec.ID)
	w.WriteBytes(title.Bytes())
	if err := s.send(s.t.PlayClientboundOpenScreen, w.Bytes()); err != nil {
		return
	}
	s.sendContainerContentLocked([]world.Item{book})
	s.sendLecternPage(0)
}

// sendLecternPage tells the client which page of the book to turn to.
func (s *session) sendLecternPage(page int32) {
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteShort(int16(lecternPageProperty))
	w.WriteShort(int16(page))
	_ = s.sendQuiet(s.t.PlayClientboundContainerSetData, w.Bytes())
}

// handleContainerButton is the lectern's menu buttons: 1 turns back a page, 2
// forward, 100 and up jump to a page (button minus 100), and 3 takes the book
// off the lectern.
func (s *session) handleContainerButton(r *mcproto.Reader) {
	window, err := r.ReadVarInt()
	if err != nil || window != containerWindowID {
		return
	}
	button, err := r.ReadByte()
	if err != nil {
		return
	}
	s.winMu.Lock()
	defer s.winMu.Unlock()
	if !s.containerOpen || s.containerTitle != lecternTitle {
		return
	}
	x, y, z := s.containerPos[0], s.containerPos[1], s.containerPos[2]
	book := s.srv.containers.Get(x, y, z, 1)[0]
	if book.Empty() {
		return
	}
	switch {
	case button == 3:
		// The book goes back to the player, and the lectern stops showing it.
		// A player with no room drops it where the lectern is, which is what
		// vanilla does rather than losing it.
		if !s.takeItem(&book) {
			s.srv.spawnItemEntity(s.t, book, float64(x)+0.5, float64(y)+1, float64(z)+0.5)
		}
		s.srv.containers.Put(x, y, z, []world.Item{{}})
		s.setLecternBook(x, y, z, false)
		if err := s.sendInventory(); err != nil {
			return
		}
		s.containerOpen = false
		s.carried = world.Item{}
		log.Printf("lectern book taken")
	case button == 1:
		s.setLecternPage(s.lecternPage - 1)
	case button == 2:
		s.setLecternPage(s.lecternPage + 1)
	case button >= 100:
		s.setLecternPage(int32(button) - 100)
	}
}

// setLecternPage clamps a page the way LecternBlockEntity.setPage does, and
// tells the client when it moved.
func (s *session) setLecternPage(page int32) {
	if s.lecternPages > 0 && page > s.lecternPages-1 {
		page = s.lecternPages - 1
	}
	if page < 0 {
		page = 0
	}
	if page == s.lecternPage {
		return
	}
	s.lecternPage = page
	s.sendLecternPage(page)
}
