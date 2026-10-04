package server

import (
	"strings"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// timeOf reads the time of day out of a Set Time body: the world age, then one
// clock of (id, time, partial tick, rate).
func timeOf(t *testing.T, p mcproto.Packet) int64 {
	t.Helper()
	r := mcproto.NewReader(p.Data)
	if _, err := r.ReadLong(); err != nil { // world age
		t.Fatalf("world age: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // clock count
		t.Fatalf("clock count: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // clock id
		t.Fatalf("clock id: %v", err)
	}
	time, err := r.ReadVarLong()
	if err != nil {
		t.Fatalf("time: %v", err)
	}
	return time
}

// doDaylightCycle off holds the clock still.
func TestDaylightCycleRule(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	s.sendTime(100)
	twelve := timeOf(t, waitPacket(t, got, s.t.PlayClientboundSetTime))
	if twelve == 0 {
		t.Fatal("the clock is at zero, so this proves nothing")
	}

	if !srv.setRule("doDaylightCycle", false) {
		t.Fatal("doDaylightCycle is not a rule here")
	}
	s.sendTime(200) // a hundred ticks later
	if now := timeOf(t, waitPacket(t, got, s.t.PlayClientboundSetTime)); now != twelve {
		t.Errorf("the clock moved from %d to %d with daylight cycle off", twelve, now)
	}

	if !srv.setRule("doDaylightCycle", true) {
		t.Fatal("doDaylightCycle could not be turned back on")
	}
	s.sendTime(300)
	if now := timeOf(t, waitPacket(t, got, s.t.PlayClientboundSetTime)); now == twelve {
		t.Error("the clock is still frozen with the rule back on")
	}
}

// doTileDrops and doMobLoot turn drops off, which is what they are for.
func TestDropRules(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	sessionRegisterForTest(srv, s)

	stone, _ := srv.blockState(tab, "minecraft:stone", nil)
	s.world.SetBlock(1, 64, 1, stone)
	s.posX, s.posY, s.posZ = 1.5, 65, 1.5
	srv.setRule("doTileDrops", false)

	var r mcproto.Writer
	r.WriteVarInt(3) // stopped digging, which breaks the block
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(7)
	s.handlePlayerAction(mcproto.NewReader(r.Bytes()))
	for _, e := range srv.entities() {
		if e.Kind == kindItem {
			t.Errorf("a block dropped %+v with doTileDrops off", e.Item)
		}
	}

	// A killed mob drops nothing either — that is doMobLoot, a different rule
	// from the block drops above.
	srv.setRule("doMobLoot", false)
	srv.spawnEntity(kindMob, tab.EntityIDs["minecraft:zombie"], 2.5, 65, 0.5)
	mob := srv.entities()[0]
	srv.hurtMob(tab, mob, 100, s)
	for _, e := range srv.entities() {
		if e.Kind == kindItem {
			t.Errorf("a mob dropped %+v", e.Item)
		}
	}
}

// keepInventory off scatters what the player was carrying, and
// doImmediateRespawn puts them straight back.
func TestDeathRules(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.spawnX, s.spawnY, s.spawnZ = 0.5, 65, 0.5
	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	stone, _ := tab.ItemIDs["minecraft:stone"]
	s.inv[0] = world.Item{ID: stone, Count: 5}

	srv.setRule("keepInventory", false)
	srv.setRule("doImmediateRespawn", true)
	s.hurtPlayer(tab, 100, "minecraft:explosion", nil)

	// The player died and the immediate respawn put them straight back: being
	// alive here is the point, so what is checked is that they are whole and
	// back at the spawn, and that their inventory was scattered on the way.
	if s.health != maxHealth || s.posX != 0.5 {
		t.Errorf("immediate respawn left health %v at (%v,%v,%v)", s.health, s.posX, s.posY, s.posZ)
	}
	for _, it := range s.inv {
		if !it.Empty() {
			t.Errorf("the inventory still holds %+v", it)
		}
	}
	dropped := 0
	for _, e := range srv.entities() {
		if e.Kind == kindItem {
			dropped++
		}
	}
	if dropped != 1 {
		t.Errorf("%d stacks were scattered, want 1", dropped)
	}
}

// The command reports and sets, and refuses a rule it does not keep.
func TestGameruleCommand(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if reply := s.runCommand("gamerule"); !strings.Contains(reply, "doTileDrops") {
		t.Errorf("a bare /gamerule replied %q", reply)
	}
	if reply := s.runCommand("gamerule doTileDrops false"); !strings.Contains(reply, "false") {
		t.Errorf("setting replied %q", reply)
	}
	if value, _ := srv.rule("doTileDrops"); value {
		t.Error("the rule did not change")
	}
	if reply := s.runCommand("gamerule doTileDrops"); !strings.Contains(reply, "false") {
		t.Errorf("reporting replied %q", reply)
	}
	if reply := s.runCommand("gamerule notARule true"); !strings.Contains(reply, "Unknown gamerule") {
		t.Errorf("an unknown rule replied %q", reply)
	}
	if reply := s.runCommand("gamerule doTileDrops maybe"); !strings.Contains(reply, "boolean") {
		t.Errorf("a broken value replied %q", reply)
	}
}
