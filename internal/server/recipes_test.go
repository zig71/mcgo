package server

import (
	"bytes"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
)

// A shaped crafting display, written out field by field from the packet's own
// codec: the display type, the size, the ingredients as slot displays, the
// result, and what it is made at.
func TestShapedDisplayBody(t *testing.T) {
	tab := v777.Tables
	s := placer(0, 0)
	s.t = tab

	recipe := proto.CraftingRecipe{Width: 2, Height: 1, Slots: []int32{1, 0}, Result: 983, Count: 4}
	var w mcproto.Writer
	s.writeCraftingDisplay(&w, recipe)

	var want mcproto.Writer
	want.WriteVarInt(tab.RecipeDisplayIDs["minecraft:crafting_shaped"])
	want.WriteVarInt(2) // width
	want.WriteVarInt(1) // height
	want.WriteVarInt(2) // two ingredients
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item"])
	want.WriteVarInt(tab.CraftingIngredients[0][0])         // slot 1 is ingredient index 0
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:empty"]) // slot 0 is empty
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item_stack"])
	want.WriteVarInt(983)
	want.WriteVarInt(4)
	want.WriteVarInt(0) // no components added
	want.WriteVarInt(0)
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item"])
	want.WriteVarInt(tab.ItemIDs["minecraft:crafting_table"])

	if !bytes.Equal(w.Bytes(), want.Bytes()) {
		t.Errorf("shaped display\n got %x\nwant %x", w.Bytes(), want.Bytes())
	}
}

// A shapeless display has no size, just the ingredients.
func TestShapelessDisplayBody(t *testing.T) {
	tab := v777.Tables
	s := placer(0, 0)
	s.t = tab

	recipe := proto.CraftingRecipe{Shapeless: true, Slots: []int32{1, 1}, Result: 404, Count: 1}
	var w mcproto.Writer
	s.writeCraftingDisplay(&w, recipe)

	var want mcproto.Writer
	want.WriteVarInt(tab.RecipeDisplayIDs["minecraft:crafting_shapeless"])
	want.WriteVarInt(2)
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item"])
	want.WriteVarInt(tab.CraftingIngredients[0][0])
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item"])
	want.WriteVarInt(tab.CraftingIngredients[0][0])
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item_stack"])
	want.WriteVarInt(404)
	want.WriteVarInt(1)
	want.WriteVarInt(0)
	want.WriteVarInt(0)
	want.WriteVarInt(tab.SlotDisplayIDs["minecraft:item"])
	want.WriteVarInt(tab.ItemIDs["minecraft:crafting_table"])

	if !bytes.Equal(w.Bytes(), want.Bytes()) {
		t.Errorf("shapeless display\n got %x\nwant %x", w.Bytes(), want.Bytes())
	}
}

// The book arrives as three packets: the (empty) property sets, the settings,
// and one entry per crafting recipe.
func TestRecipeBookOnJoin(t *testing.T) {
	srv := fluidServer()
	s, got, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	s.sendRecipeBook()

	waitFor(t, log, s.t.PlayClientboundRecipeBookAdd)
	waitFor(t, log, s.t.PlayClientboundUpdateRecipes)
	if !log.has(s.t.PlayClientboundRecipeBookSettings) {
		t.Errorf("no recipe book settings were sent: %v", log.seen())
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			switch p.ID {
			case s.t.PlayClientboundUpdateRecipes:
				if !bytes.Equal(p.Data, []byte{0, 0}) {
					t.Errorf("update recipes should carry no sets: %x", p.Data)
				}
			case s.t.PlayClientboundRecipeBookSettings:
				want := []byte{0, 1, 0, 1, 0, 1, 0, 1} // closed, filtering, four times
				if !bytes.Equal(p.Data, want) {
					t.Errorf("settings\n got %x\nwant %x", p.Data, want)
				}
			case s.t.PlayClientboundRecipeBookAdd:
				r := mcproto.NewReader(p.Data)
				n, err := r.ReadVarInt()
				if err != nil {
					t.Fatalf("reading the entry count: %v", err)
				}
				if int(n) != len(s.t.CraftingRecipes) {
					t.Errorf("%d recipes announced, want %d", n, len(s.t.CraftingRecipes))
				}
				return
			}
		case <-deadline:
			t.Fatal("the recipe book packets did not all arrive")
		}
	}
}
