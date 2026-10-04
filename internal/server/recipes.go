package server

import (
	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
)

// The recipe book.
//
// In this protocol version Update Recipes does not carry the recipes: it
// carries the "what can I make out of this" sets the client uses for
// highlighting, and the recipes themselves go in Recipe Book Add as displays.
// This server fills the book with its crafting recipes and leaves the
// highlighting sets empty.

// sendRecipeBook gives the client its crafting recipes.
func (s *session) sendRecipeBook() {
	t := s.t
	if t.PlayClientboundUpdateRecipes == 0 || t.PlayClientboundRecipeBookAdd == 0 {
		return
	}

	// Update Recipes: no property sets and no stonecutter inputs, so the
	// client will not grey out recipes it cannot make from what is held.
	var w mcproto.Writer
	w.WriteVarInt(0)
	w.WriteVarInt(0)
	if err := s.send(t.PlayClientboundUpdateRecipes, w.Bytes()); err != nil {
		return
	}

	// Settings: closed, filtering on, for each of the four book types.
	if t.PlayClientboundRecipeBookSettings != 0 {
		w.Reset()
		for range 4 {
			w.WriteBool(false) // not open
			w.WriteBool(true)  // filtering: show only what can be made
		}
		if err := s.send(t.PlayClientboundRecipeBookSettings, w.Bytes()); err != nil {
			return
		}
	}

	// Add: every crafting recipe we know, as a display. The ids have to be
	// unique but are otherwise ours to choose.
	w.Reset()
	w.WriteVarInt(int32(len(t.CraftingRecipes)))
	for i, recipe := range t.CraftingRecipes {
		w.WriteVarInt(int32(i)) // recipe display id
		s.writeCraftingDisplay(&w, recipe)
		w.WriteVarInt(0) // no group
		w.WriteVarInt(t.RecipeBookCategoryIDs["minecraft:crafting_misc"])
		w.WriteBool(false) // no crafting requirements
		w.WriteByte(0)     // no notification flags
	}
	w.WriteBool(true) // replace whatever the client had
	_ = s.send(t.PlayClientboundRecipeBookAdd, w.Bytes())
}

// writeCraftingDisplay writes a shaped or shapeless crafting display.
func (s *session) writeCraftingDisplay(w *mcproto.Writer, recipe proto.CraftingRecipe) {
	if recipe.Shapeless {
		w.WriteVarInt(s.t.RecipeDisplayIDs["minecraft:crafting_shapeless"])
		w.WriteVarInt(int32(len(recipe.Slots)))
		for _, slot := range recipe.Slots {
			s.writeItemSlotDisplay(w, slot)
		}
	} else {
		w.WriteVarInt(s.t.RecipeDisplayIDs["minecraft:crafting_shaped"])
		w.WriteVarInt(recipe.Width)
		w.WriteVarInt(recipe.Height)
		w.WriteVarInt(int32(len(recipe.Slots)))
		for _, slot := range recipe.Slots {
			s.writeItemSlotDisplay(w, slot)
		}
	}
	s.writeResultDisplay(w, recipe)
	s.writeCraftingStationDisplay(w)
}

// writeItemSlotDisplay writes one ingredient: a slot index is an index plus one
// into the ingredient table, or zero for an empty slot. Vanilla shows a tag as
// a tag; this server's recipes carry the items a tag expands to, so the first
// of them stands for it.
func (s *session) writeItemSlotDisplay(w *mcproto.Writer, slot int32) {
	item, ok := s.ingredientItem(slot)
	if !ok {
		w.WriteVarInt(s.t.SlotDisplayIDs["minecraft:empty"])
		return
	}
	w.WriteVarInt(s.t.SlotDisplayIDs["minecraft:item"])
	w.WriteVarInt(item)
}

// ingredientItem is the item an ingredient slot stands for.
func (s *session) ingredientItem(slot int32) (int32, bool) {
	if slot <= 0 || int(slot) > len(s.t.CraftingIngredients) {
		return 0, false
	}
	set := s.t.CraftingIngredients[slot-1]
	if len(set) == 0 {
		return 0, false
	}
	return set[0], true
}

// writeResultDisplay writes the recipe's result, with its count.
func (s *session) writeResultDisplay(w *mcproto.Writer, recipe proto.CraftingRecipe) {
	count := recipe.Count
	if count <= 0 {
		count = 1
	}
	w.WriteVarInt(s.t.SlotDisplayIDs["minecraft:item_stack"])
	w.WriteVarInt(recipe.Result)
	w.WriteVarInt(count)
	w.WriteVarInt(0) // no components added
	w.WriteVarInt(0) // none removed
}

// writeCraftingStationDisplay writes what the recipe is made at, which for all
// of ours is a crafting table.
func (s *session) writeCraftingStationDisplay(w *mcproto.Writer) {
	table, ok := s.t.ItemIDs["minecraft:crafting_table"]
	if !ok {
		w.WriteVarInt(s.t.SlotDisplayIDs["minecraft:empty"])
		return
	}
	w.WriteVarInt(s.t.SlotDisplayIDs["minecraft:item"])
	w.WriteVarInt(table)
}
