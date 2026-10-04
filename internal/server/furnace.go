package server

import (
	"log"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// The four container data properties a furnace exposes, in order: fuel left,
// the total that fuel started with, cooking progress, and the cooking time the
// recipe needs.
const (
	furnaceDataLit       = 0
	furnaceDataLitTotal  = 1
	furnaceDataCook      = 2
	furnaceDataCookTotal = 3
)

// isFurnaceTitle reports whether a three-slot menu is one of the machines that
// cooks. An anvil also has three slots, so the size alone cannot decide.
func isFurnaceTitle(title string) bool {
	switch title {
	case "container.furnace", "container.blast_furnace", "container.smoker":
		return true
	}
	return false
}

// menuTitleOf reports the menu title for a block position, which is how the
// cooking code tells a furnace from a blast furnace or a smoker.
func (s *Server) menuTitleOf(t proto.Tables, w *world.World, x, y, z int32) (string, bool) {
	spec, ok := t.ContainerMenus[w.Block(x, y, z)]
	if !ok || !isFurnaceTitle(spec.Title) {
		return "", false
	}
	return spec.Title, true
}

// cookingTable picks the recipe table a machine uses. A vanilla blast furnace
// only accepts blasting recipes, a smoker only smoking ones, and a furnace
// smelting ones.
func cookingTable(t proto.Tables, title string) map[int32]proto.CookingRecipe {
	switch title {
	case "container.blast_furnace":
		return t.BlastRecipes
	case "container.smoker":
		return t.SmokerRecipes
	default:
		return t.FurnaceRecipes
	}
}

// fastCooking reports whether a machine burns fuel twice as fast (a blast
// furnace or a smoker does).
func fastCooking(title string) bool {
	return title == "container.blast_furnace" || title == "container.smoker"
}

// burnTicks is how long one item of fuel lasts in this machine.
func burnTicks(t proto.Tables, title string, itemID int32) int32 {
	burn := t.Fuel[itemID]
	if fastCooking(title) {
		burn /= 2
	}
	return burn
}

// canCook reports whether the machine's output slot can accept one more of the
// recipe's product.
func canCook(items []world.Item, rec proto.CookingRecipe) bool {
	out := items[2]
	if out.Empty() {
		return true
	}
	if out.ID != rec.Result {
		return false
	}
	return out.Count+rec.Count <= 64
}

// cookFurnaces advances every furnace the world knows about by one tick. It
// runs on the server tick loop, so a furnace cooks whether or not anyone has
// its screen open — and when a player does, they are sent the progress.
//
// Cooking follows AbstractFurnaceBlockEntity.serverTick: fuel lights the
// machine, the recipe advances only while lit, and a machine that loses its
// recipe cools down twice as fast as it heated.
func (s *Server) cookFurnaces() {
	t := s.newest
	w := s.worldFor(t)
	for _, f := range s.containers.Furnaces() {
		x, y, z := f.Pos[0], f.Pos[1], f.Pos[2]
		title, ok := s.menuTitleOf(t, w, x, y, z)
		if !ok {
			// The block is gone: stop tracking it. Its contents were dropped
			// when it was broken.
			s.containers.Remove(x, y, z)
			continue
		}
		items, st := s.containers.Furnace(x, y, z)
		if len(items) < 3 {
			continue
		}
		newSt, changed := advanceFurnace(t, title, items, st)
		if !changed {
			continue
		}
		s.containers.PutFurnace(x, y, z, items, newSt)
		s.setFurnaceLit(t, w, x, y, z, newSt.Lit > 0)
		s.notifyContainer(x, y, z, items, newSt)
	}
}

// advanceFurnace runs one tick of a furnace, mutating items and returning the
// new cooking state. changed is false when nothing about the machine moved.
func advanceFurnace(t proto.Tables, title string, items []world.Item, st world.FurnaceState) (world.FurnaceState, bool) {
	wasLit := st.Lit > 0
	isLit := wasLit
	if st.Lit > 0 {
		st.Lit--
		isLit = st.Lit > 0
	}

	ingredient, fuel := items[0], items[1]
	hasIngredient, hasFuel := !ingredient.Empty(), !fuel.Empty()
	changed := false

	if isLit || (hasFuel && hasIngredient) {
		if hasIngredient {
			if rec, ok := cookingTable(t, title)[ingredient.ID]; ok && canCook(items, rec) {
				if !isLit {
					burn := burnTicks(t, title, fuel.ID)
					st.Lit, st.LitTotal = burn, burn
					if burn > 0 {
						consumeFuel(t, items)
						isLit = true
						changed = true
					}
				}
				if isLit {
					if st.CookTotal != rec.Time {
						// A new recipe restarts progress, exactly as vanilla does
						// when the input slot's item changes.
						st.Cook, st.CookTotal = 0, rec.Time
					}
					st.Cook++
					if st.Cook >= st.CookTotal {
						st.Cook = 0
						produce(items, rec)
						changed = true
					}
				} else if st.Cook != 0 {
					st.Cook = 0
					changed = true
				}
			} else if st.Cook != 0 {
				st.Cook = 0
				changed = true
			}
			if st.CookTotal == 0 {
				st.CookTotal = recipeTime(t, title, ingredient)
			}
		} else if st.Cook != 0 {
			st.Cook = 0
			changed = true
		}
	} else if st.Cook > 0 {
		st.Cook = max(0, st.Cook-2)
		changed = true
	}

	if st.Lit != 0 || st.Cook != 0 {
		changed = true
	}
	if wasLit != isLit {
		changed = true
	}
	return st, changed || wasLit != isLit
}

// recipeTime is the cooking time of the recipe the input matches, or the
// vanilla default of 200 ticks when nothing matches.
func recipeTime(t proto.Tables, title string, ingredient world.Item) int32 {
	if rec, ok := cookingTable(t, title)[ingredient.ID]; ok {
		return rec.Time
	}
	return 200
}

// consumeFuel takes one fuel item, leaving the crafting remainder behind (a
// lava bucket becomes an empty bucket) when the slot empties.
func consumeFuel(t proto.Tables, items []world.Item) {
	fuel := items[1]
	fuel.Count--
	if fuel.Count <= 0 {
		if left, ok := t.CraftRemainder[fuel.ID]; ok {
			items[1] = world.Item{ID: left, Count: 1}
			return
		}
		items[1] = world.Item{}
		return
	}
	items[1] = fuel
}

// produce adds the recipe's product to the output slot and consumes one input.
func produce(items []world.Item, rec proto.CookingRecipe) {
	out := items[2]
	if out.Empty() {
		out = world.Item{ID: rec.Result, Count: rec.Count}
	} else {
		out.Count += rec.Count
	}
	items[2] = out
	items[0].Count--
	if items[0].Count <= 0 {
		items[0] = world.Item{}
	}
}

// setFurnaceLit flips the block's LIT property so the machine looks lit, and
// tells every client that can see it.
func (s *Server) setFurnaceLit(t proto.Tables, w *world.World, x, y, z int32, lit bool) {
	cur := w.Block(x, y, z)
	next, ok := t.FurnaceLitOff[cur]
	if lit {
		next, ok = t.FurnaceLitOn[cur]
	}
	if !ok || next == cur {
		return
	}
	s.srvSetBlock(w, x, y, z, next)
	log.Printf("furnace (%d,%d,%d) lit=%v", x, y, z, lit)
}

// srvSetBlock records a block change for the whole server and broadcasts it.
// Unlike a player's own edit there is no prediction to ack, so no ack is sent.
func (s *Server) srvSetBlock(w *world.World, x, y, z, id int32) {
	w.SetBlock(x, y, z, id)
	var u mcproto.Writer
	u.WriteLong(packPosition(int(x), int(y), int(z)))
	u.WriteVarInt(id)
	body := u.Bytes()
	for _, sess := range s.sessionList() {
		sess.sendQuiet(sess.t.PlayClientboundBlockUpdate, body)
	}
}

// notifyContainer pushes a container's contents, and a furnace's progress, to
// every session that has it open.
func (s *Server) notifyContainer(x, y, z int32, items []world.Item, st world.FurnaceState) {
	for _, sess := range s.sessionList() {
		sess.winMu.Lock()
		if !sess.containerOpen || sess.containerPos != [3]int32{x, y, z} {
			sess.winMu.Unlock()
			continue
		}
		sess.containerState++
		sess.sendContainerContentLocked(items)
		sess.sendFurnaceData(st)
		sess.winMu.Unlock()
	}
}

// sendContainerData pushes the four furnace properties for an open furnace.
func (s *session) sendContainerData(x, y, z int32) {
	items, st := s.srv.containers.Furnace(x, y, z)
	_ = items
	s.sendFurnaceData(st)
}

// sendFurnaceData sends the container properties the client draws the flame
// and the progress arrow from.
func (s *session) sendFurnaceData(st world.FurnaceState) {
	if !isFurnaceTitle(s.containerTitle) {
		return
	}
	props := [4]int32{st.Lit, st.LitTotal, st.Cook, st.CookTotal}
	for id, value := range props {
		var w mcproto.Writer
		w.WriteVarInt(containerWindowID)
		w.WriteShort(int16(id))
		w.WriteShort(int16(value))
		if err := s.sendQuiet(s.t.PlayClientboundContainerSetData, w.Bytes()); err != nil {
			return
		}
	}
}
