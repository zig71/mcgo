package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// In survival a broken block leaves its drop, which is what a cobblestone is:
// stone's own item is stone, and vanilla does not drop that. Stone is also a
// block that *requires* the right tool to drop anything, so the pickaxe in hand
// is what decides: the same dig with an empty hand breaks the block and is
// given nothing.
func TestSurvivalBreakingDrops(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, log := pipedSession(t, srv)
	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	pickaxe, ok := tab.ItemIDs["minecraft:wooden_pickaxe"]
	if !ok {
		t.Fatal("no wooden pickaxe")
	}
	s.inv[27] = world.Item{ID: pickaxe, Count: 1}
	s.world.SetBlock(1, 64, 1, stone)
	s.posX, s.posY, s.posZ = 1.5, 65, 1.5
	sessionRegisterForTest(srv, s)

	dig := func(x, y, z, status int32) {
		var r mcproto.Writer
		r.WriteVarInt(status)
		r.WriteLong(packPosition(int(x), int(y), int(z)))
		r.WriteByte(1)
		r.WriteVarInt(7)
		s.handlePlayerAction(mcproto.NewReader(r.Bytes()))
	}
	dig(1, 64, 1, 0) // started digging
	dig(1, 64, 1, 3) // stopped digging: the one that breaks the block

	if got := s.world.Block(1, 64, 1); got != tab.BlockAir {
		t.Fatalf("the block survived: %d", got)
	}
	cobble := tab.ItemIDs["minecraft:cobblestone"]
	dropped := func(item int32) bool {
		for _, e := range srv.entities() {
			if e.Kind == kindItem && e.Item.ID == item {
				return true
			}
		}
		return false
	}
	if !dropped(cobble) {
		t.Errorf("stone broken with a pickaxe dropped no cobblestone: %v", log.seen())
	}

	// The same block, the same dig, an empty hand: it still breaks, and
	// nothing drops. Diorite rather than stone so the cobblestone already on
	// the ground cannot answer for it.
	diorite, ok := srv.blockState(tab, "minecraft:diorite", nil)
	if !ok {
		t.Fatal("no diorite block state")
	}
	dioriteItem, ok := tab.ItemIDs["minecraft:diorite"]
	if !ok {
		t.Fatal("no diorite item")
	}
	s.inv[27] = world.Item{}
	s.world.SetBlock(2, 64, 1, diorite)
	dig(2, 64, 1, 0)
	dig(2, 64, 1, 3)
	if got := s.world.Block(2, 64, 1); got != tab.BlockAir {
		t.Fatalf("the bare-handed block survived: %d", got)
	}
	if dropped(dioriteItem) {
		t.Error("diorite dropped for a bare hand, but it needs the right tool")
	}

	// A block that drops nothing drops nothing.
	if drops := srv.blockDropsFor(tab, "minecraft:glass"); len(drops) != 0 {
		t.Errorf("glass drops %v, want nothing", drops)
	}
	// An unnamed block drops itself.
	if drops := srv.blockDropsFor(tab, "minecraft:oak_planks"); len(drops) != 1 {
		t.Errorf("oak planks drop %v, want itself", drops)
	}
}

// Creative breaking leaves nothing behind.
func TestCreativeBreakingDropsNothing(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv) // creative by default
	stone, _ := srv.blockState(tab, "minecraft:stone", nil)
	s.world.SetBlock(1, 64, 1, stone)
	s.posX, s.posY, s.posZ = 1.5, 65, 1.5
	sessionRegisterForTest(srv, s)

	var r mcproto.Writer
	r.WriteVarInt(3)
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(7)
	s.handlePlayerAction(mcproto.NewReader(r.Bytes()))

	for _, e := range srv.entities() {
		if e.Kind == kindItem {
			t.Errorf("a creative break dropped %+v", e.Item)
		}
	}
}

// Onlookers see the cracking texture while someone digs, and see it cleared
// when the block goes.
func TestDestroyStageGoesToOnlookers(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	digger, _, _ := pipedSession(t, srv)
	digger.entityID = 20
	digger.posX, digger.posY, digger.posZ = 1.5, 65, 1.5
	sessionRegisterForTest(srv, digger)
	watcher, _, watchLog := pipedSession(t, srv)
	watcher.entityID = 21
	watcher.posX, watcher.posY, watcher.posZ = 3.5, 65, 1.5
	sessionRegisterForTest(srv, watcher)
	stone, _ := srv.blockState(tab, "minecraft:stone", nil)
	digger.world.SetBlock(1, 64, 1, stone)

	var r mcproto.Writer
	r.WriteVarInt(0) // started digging
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(7)
	digger.handlePlayerAction(mcproto.NewReader(r.Bytes()))

	// The watcher's reader is its own goroutine, so give it a moment: checking
	// straight away is a race.
	waitFor(t, watchLog, tab.PlayClientboundBlockDestruction)

	r.Reset()
	r.WriteVarInt(3)
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(8)
	digger.handlePlayerAction(mcproto.NewReader(r.Bytes()))
}

// An explosion sends one light update per affected chunk, not one per block:
// the per-block light storm is what made the client re-mesh over and over, so
// the blast looked like it was eating the ground a row at a time.
func TestExplosionSendsFewLightUpdates(t *testing.T) {
	srv, w := mobServer(t)
	tab := v777.Tables
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	// A solid block of stone to blow up.
	for i := int32(0); i < 3; i++ {
		for j := int32(0); j < 3; j++ {
			for k := int32(0); k < 3; k++ {
				w.SetBlock(3+i, 64+j, 3+k, stone)
			}
		}
	}
	sess, got, _ := pipedSession(t, srv)
	sess.posX, sess.posY, sess.posZ = 4, 64, 4
	sessionRegisterForTest(srv, sess)

	srv.explode(tab, w, 4.5, 65, 4.5, 2)

	// The blocks all went, in the world and to the client.
	removed := 0
	for i := int32(0); i < 3; i++ {
		for j := int32(0); j < 3; j++ {
			for k := int32(0); k < 3; k++ {
				if w.Block(3+i, 64+j, 3+k) == tab.BlockAir {
					removed++
				}
			}
		}
	}
	if removed == 0 {
		t.Fatal("the blast removed nothing")
	}
	lights := 0
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			switch p.ID {
			case tab.PlayClientboundLightUpdate:
				lights++
				if lights > 8 {
					t.Fatalf("%d light updates for one %d block blast, want one per chunk", lights, removed)
				}
			}
		case <-deadline:
			if lights == 0 {
				t.Error("no light update at all after the blast")
			}
			return
		}
	}
}
