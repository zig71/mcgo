package server

import (
	"testing"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// craftWindow builds a crafting table's ten slots: the result slot then a 3x3
// grid, filled row by row from the names given ("" leaves a cell empty).
func craftWindow(names ...string) []world.Item {
	items := make([]world.Item, craftGridFirst+craftGridCells)
	for i, name := range names {
		if name == "" {
			continue
		}
		items[craftGridFirst+i] = world.Item{ID: itemID(v777.Tables, name), Count: 1}
	}
	return items
}

func TestShapelessCrafting(t *testing.T) {
	tab := v777.Tables
	items := craftWindow("minecraft:oak_log")

	got := craftingResult(tab, gridOf(items))

	if got.ID != itemID(tab, "minecraft:oak_planks") || got.Count != 4 {
		t.Fatalf("one log crafted %+v, want four oak planks", got)
	}
}

// The chest recipe is a ring of planks with an empty middle: it proves a
// shaped recipe's empty cells must stay empty.
func TestShapedCrafting(t *testing.T) {
	tab := v777.Tables
	planks := "minecraft:oak_planks"
	items := craftWindow(
		planks, planks, planks,
		planks, "", planks,
		planks, planks, planks,
	)

	got := craftingResult(tab, gridOf(items))

	if got.ID != itemID(tab, "minecraft:chest") || got.Count != 1 {
		t.Fatalf("chest pattern crafted %+v", got)
	}

	// The same pattern with the middle filled is not a chest.
	full := craftWindow(planks, planks, planks, planks, planks, planks, planks, planks, planks)
	if got := craftingResult(tab, gridOf(full)); got.ID == itemID(tab, "minecraft:chest") {
		t.Error("a full 3x3 of planks was taken for the chest recipe")
	}
}

func TestCraftingIgnoresNonRecipe(t *testing.T) {
	tab := v777.Tables
	items := craftWindow("minecraft:dirt")

	if got := craftingResult(tab, gridOf(items)); !got.Empty() {
		t.Fatalf("a lone dirt block crafted %+v", got)
	}
}

func TestCraftingConsumesIngredients(t *testing.T) {
	tab := v777.Tables
	items := craftWindow("minecraft:oak_log")
	s := &session{t: tab, containerSize: craftGridFirst + craftGridCells}

	got := s.craftOnce(items)

	if got.ID != itemID(tab, "minecraft:oak_planks") {
		t.Fatalf("craftOnce returned %+v", got)
	}
	if !items[craftGridFirst].Empty() {
		t.Errorf("the log is still in the grid: %+v", items[craftGridFirst])
	}
	if got := craftingResult(tab, gridOf(items)); !got.Empty() {
		t.Errorf("the grid still matches a recipe: %+v", got)
	}
}

// Shift-clicking the result crafts into the player's inventory, not onto the
// cursor.
func TestCraftingShiftClickFillsInventory(t *testing.T) {
	tab := v777.Tables
	s := &session{t: tab, containerSize: craftGridFirst + craftGridCells}
	planks := "minecraft:oak_planks"
	items := craftWindow(
		planks, planks, planks,
		planks, "", planks,
		planks, planks, planks,
	)
	w := s.window(items)

	carried := world.Item{}
	if !s.craftResultClick(items, w, 1, &carried) {
		t.Fatal("shift-click on the result slot did nothing")
	}
	if !carried.Empty() {
		t.Errorf("shift-click left %+v on the cursor", carried)
	}
	chests := 0
	for _, it := range w[craftGridFirst+craftGridCells:] {
		if it.ID == itemID(tab, "minecraft:chest") {
			chests += int(it.Count)
		}
	}
	if chests != 1 {
		t.Errorf("inventory holds %d chests, want 1", chests)
	}
	for i := range craftGridCells {
		if !items[craftGridFirst+i].Empty() {
			t.Fatalf("the grid was not consumed: %+v", items[craftGridFirst+i])
		}
	}
}
