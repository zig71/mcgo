package server

import (
	"log"

	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// A crafting table's window is a result slot followed by a 3x3 grid.
const (
	craftResultSlot = 0
	craftGridFirst  = 1
	craftGridWide   = 3
	craftGridCells  = craftGridWide * craftGridWide
)

// isCraftingMenu reports whether the open menu is the crafting table, by its
// slot layout: a result slot plus a 3x3 grid plus the player's 36.
func (s *session) isCraftingMenu() bool { return s.containerSize == craftGridFirst+craftGridCells }

// gridOf returns the template's 3x3 grid, in row-major order.
func gridOf(items []world.Item) []world.Item {
	grid := make([]world.Item, craftGridCells)
	for i := range grid {
		if craftGridFirst+i < len(items) {
			grid[i] = items[craftGridFirst+i]
		}
	}
	return grid
}

// craftingResult is the item the result slot should show for a 3x3 grid, or
// the empty item when nothing matches.
//
// Matching lifts vanilla's: a shaped recipe is compared against the grid's
// bounding box (and against its mirror image), a shapeless one needs the same
// multiset of ingredients in any arrangement.
func craftingResult(t proto.Tables, grid []world.Item) world.Item {
	if gridEmpty(grid) {
		return world.Item{}
	}
	// The bounding box trims empty rows and columns: a recipe for two planks
	// stacked vertically matches wherever in the grid the player puts them.
	minX, minY, maxX, maxY := craftGridWide, craftGridWide, -1, -1
	for y := range craftGridWide {
		for x := range craftGridWide {
			if grid[y*craftGridWide+x].Empty() {
				continue
			}
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	width, height := maxX-minX+1, maxY-minY+1
	trimmed := make([]world.Item, 0, width*height)
	for y := range height {
		for x := range width {
			trimmed = append(trimmed, grid[(minY+y)*craftGridWide+minX+x])
		}
	}
	used := 0
	for _, it := range grid {
		if !it.Empty() {
			used++
		}
	}

	for _, r := range t.CraftingRecipes {
		if r.Shapeless {
			if len(r.Slots) != used {
				continue
			}
			if matchesShapeless(t, r, grid) {
				return world.Item{ID: r.Result, Count: r.Count}
			}
			continue
		}
		if int(r.Width) != width || int(r.Height) != height || len(r.Slots) != len(trimmed) {
			continue
		}
		if matchesShaped(t, r.Slots, trimmed) || matchesShaped(t, r.Slots, mirror(trimmed, width, height)) {
			return world.Item{ID: r.Result, Count: r.Count}
		}
	}
	return world.Item{}
}

func gridEmpty(grid []world.Item) bool {
	for _, it := range grid {
		if !it.Empty() {
			return false
		}
	}
	return true
}

// mirror flips a trimmed grid horizontally, which vanilla allows for shaped
// recipes.
func mirror(cells []world.Item, width, height int) []world.Item {
	out := make([]world.Item, len(cells))
	for y := range height {
		for x := range width {
			out[y*width+x] = cells[y*width+(width-1-x)]
		}
	}
	return out
}

// matchesShaped compares a recipe's slots (index+1 into the ingredient sets,
// 0 meaning the cell must be empty) with the grid.
func matchesShaped(t proto.Tables, slots []int32, cells []world.Item) bool {
	for i, slot := range slots {
		if !accepts(t, slot, cells[i]) {
			return false
		}
	}
	return true
}

// matchesShapeless finds an assignment of the grid's items to the recipe's
// ingredients. There are at most nine, so a simple backtracking search is
// plenty (and it cannot miss a match the way a greedy pass would).
func matchesShapeless(t proto.Tables, r proto.CraftingRecipe, grid []world.Item) bool {
	items := make([]world.Item, 0, len(grid))
	for _, it := range grid {
		if !it.Empty() {
			items = append(items, it)
		}
	}
	used := make([]bool, len(items))
	var assign func(i int) bool
	assign = func(i int) bool {
		if i == len(r.Slots) {
			return true
		}
		for j, it := range items {
			if used[j] || !accepts(t, r.Slots[i], it) {
				continue
			}
			used[j] = true
			if assign(i + 1) {
				return true
			}
			used[j] = false
		}
		return false
	}
	return assign(0)
}

// accepts reports whether a grid item satisfies a recipe slot.
func accepts(t proto.Tables, slot int32, it world.Item) bool {
	if slot == 0 {
		return it.Empty()
	}
	if it.Empty() {
		return false
	}
	if int(slot) > len(t.CraftingIngredients) {
		return false
	}
	for _, id := range t.CraftingIngredients[slot-1] {
		if id == it.ID {
			return true
		}
	}
	return false
}

// consumeCrafting takes one of each ingredient the recipe used out of the grid
// and puts the crafting remainders back, which is what ResultSlot.onTake does.
func consumeCrafting(t proto.Tables, items []world.Item, grid []world.Item, recipe *proto.CraftingRecipe) {
	for i := range craftGridCells {
		if grid[i].Empty() {
			continue
		}
		slot := craftGridFirst + i
		items[slot].Count--
		if items[slot].Count <= 0 {
			items[slot] = world.Item{}
		}
		if left, ok := t.CraftRemainder[grid[i].ID]; ok {
			// A bucket or bottle comes back in the slot it was used from.
			if items[slot].Empty() {
				items[slot] = world.Item{ID: left, Count: 1}
			}
		}
	}
}

// craftOnce produces one result from the grid: it consumes the ingredients and
// returns the item that was crafted, or the empty item when nothing matches.
func (s *session) craftOnce(items []world.Item) world.Item {
	grid := gridOf(items)
	recipe := s.matchCrafting(grid)
	if recipe == nil {
		return world.Item{}
	}
	out := world.Item{ID: recipe.Result, Count: recipe.Count}
	consumeCrafting(s.t, items, grid, recipe)
	return out
}

// matchCrafting re-runs the match and returns the recipe itself, so its result
// can be handed out and its ingredients consumed.
func (s *session) matchCrafting(grid []world.Item) *proto.CraftingRecipe {
	res := craftingResult(s.t, grid)
	if res.Empty() {
		return nil
	}
	for i := range s.t.CraftingRecipes {
		r := &s.t.CraftingRecipes[i]
		if r.Result != res.ID || r.Count != res.Count {
			continue
		}
		if r.Shapeless {
			if len(r.Slots) == countNonEmpty(grid) && matchesShapeless(s.t, *r, grid) {
				return r
			}
			continue
		}
		minX, minY, maxX, maxY := craftGridWide, craftGridWide, -1, -1
		for y := range craftGridWide {
			for x := range craftGridWide {
				if grid[y*craftGridWide+x].Empty() {
					continue
				}
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x), max(maxY, y)
			}
		}
		width, height := maxX-minX+1, maxY-minY+1
		if int(r.Width) != width || int(r.Height) != height {
			continue
		}
		trimmed := make([]world.Item, 0, width*height)
		for y := range height {
			for x := range width {
				trimmed = append(trimmed, grid[(minY+y)*craftGridWide+minX+x])
			}
		}
		if matchesShaped(s.t, r.Slots, trimmed) || matchesShaped(s.t, r.Slots, mirror(trimmed, width, height)) {
			return r
		}
	}
	return nil
}

func countNonEmpty(grid []world.Item) int {
	n := 0
	for _, it := range grid {
		if !it.Empty() {
			n++
		}
	}
	return n
}

// refreshCraftingResult recomputes the result slot of an open crafting table.
func (s *session) refreshCraftingResult(items []world.Item) {
	if !s.isCraftingMenu() || len(items) == 0 {
		return
	}
	items[craftResultSlot] = craftingResult(s.t, gridOf(items))
}

// craftResultClick handles a click on the crafting result slot. The slot holds
// a preview rather than a stored stack, so taking it consumes the grid: a plain
// click puts one craft on the cursor, a shift-click crafts into the player's
// inventory until the grid or the inventory runs out.
func (s *session) craftResultClick(items []world.Item, w []world.Item, mode int32, carried *world.Item) bool {
	if !s.isCraftingMenu() {
		return false
	}
	size := int(s.containerSize)
	result := craftingResult(s.t, gridOf(items))
	if result.Empty() {
		return false
	}

	switch mode {
	case 0:
		if carried.Empty() {
			*carried = result
		} else if carried.Same(result) && carried.Count+result.Count <= 64 {
			carried.Count += result.Count
		} else {
			return false
		}
		s.craftOnce(items)
		log.Printf("crafted %d x item %d", result.Count, result.ID)
		return true

	case 1:
		crafted := 0
		for crafted < 64 {
			result = craftingResult(s.t, gridOf(items))
			if result.Empty() {
				break
			}
			if !addToInventory(w[size:], result) {
				break
			}
			s.craftOnce(items)
			crafted++
		}
		if crafted > 0 {
			log.Printf("crafted %d items into the inventory", crafted)
		}
		return true
	}
	return false
}

// reclaimResultSlot returns a stray item from a menu's result slot to the
// player's inventory. Result slots cannot be filled by hand, but a shift-click
// can move a stack into one; without this the item would be silently dropped
// when the result is recomputed.
func (s *session) reclaimResultSlot(items []world.Item, w []world.Item) {
	slot := int32(-1)
	switch {
	case s.isCraftingMenu():
		slot = craftResultSlot
	case isAnvilTitle(s.containerTitle):
		slot = anvilResultSlot
	default:
		return
	}
	if int(slot) >= len(items) {
		return
	}
	stray := items[slot]
	if stray.Empty() {
		return
	}
	items[slot] = world.Item{}
	size := int(s.containerSize)
	if addToInventory(w[size:], stray) {
		copy(s.inv[:], w[size:])
		return
	}
	// Nowhere to put it: keep it on the input side rather than destroying it.
	if !isAnvilTitle(s.containerTitle) {
		items[craftGridFirst] = stray
	} else {
		items[anvilInputSlot] = stray
	}
	log.Printf("result slot held %+v and the inventory was full", stray)
}

// closeCraftingGrid returns a crafting table's grid to the player when the
// window closes. Vanilla drops whatever does not fit; without item entities
// the leftovers stay in the table instead of being destroyed.
func (s *session) closeCraftingGrid(items []world.Item) []world.Item {
	if !s.isCraftingMenu() {
		return items
	}
	left := make([]world.Item, 0, craftGridCells)
	for i := range craftGridCells {
		slot := craftGridFirst + i
		it := items[slot]
		if it.Empty() {
			continue
		}
		if addToInventory(s.inv[:], it) {
			items[slot] = world.Item{}
		} else {
			left = append(left, it)
		}
	}
	if len(left) > 0 {
		log.Printf("%d crafting items had nowhere to go and stayed in the table", len(left))
	}
	return items
}

// addToInventory puts a stack into the player's half of the window, topping up
// matching stacks first. It reports whether the whole stack fitted.
func addToInventory(playerHalf []world.Item, it world.Item) bool {
	for i, cur := range playerHalf {
		if cur.Empty() || !cur.Same(it) || cur.Count >= 64 {
			continue
		}
		n := min(64-cur.Count, it.Count)
		cur.Count += n
		playerHalf[i] = cur
		it.Count -= n
		if it.Count == 0 {
			return true
		}
	}
	for i, cur := range playerHalf {
		if cur.Empty() {
			playerHalf[i] = it
			return true
		}
	}
	return false
}
