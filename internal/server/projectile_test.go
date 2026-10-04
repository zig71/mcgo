package server

import (
	"math"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// useItemPacket is the air right-click the client sends for a throw: which hand,
// then the sequence number.
func useItemPacket() []byte {
	var r mcproto.Writer
	r.WriteVarInt(0)
	r.WriteVarInt(1)
	return r.Bytes()
}

// thrownServer is a surviving player standing on the flat world's surface,
// looking due south and level, with the given item in hand.
func thrownServer(t *testing.T, item string, count int32) (*Server, *session, *packetLog, proto.Tables) {
	t.Helper()
	srv := fluidServer()
	tab := v777.Tables
	s, _, log := pipedSession(t, srv)
	if err := s.setGameMode(gameModeSurvival); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	id, ok := tab.ItemIDs[item]
	if !ok {
		t.Fatalf("no %s item", item)
	}
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5
	s.yaw, s.pitch = 0, 0
	s.inv[27] = world.Item{ID: id, Count: count}
	sessionRegisterForTest(srv, s)
	return srv, s, log, tab
}

// thrown is the projectile this server has, if any.
func thrown(srv *Server) *Entity {
	for _, e := range srv.entities() {
		if e.Kind == kindProjectile {
			return e
		}
	}
	return nil
}

// A snowball leaves the hand along the look vector at 1.5 blocks a tick, flies
// on ThrowableProjectile's numbers — gravity 0.03, drag 0.99 — and spends itself
// doing it.
func TestThrowingASnowball(t *testing.T) {
	srv, s, _, tab := thrownServer(t, "minecraft:snowball", 3)

	s.handleUseItem(mcproto.NewReader(useItemPacket()))

	e := thrown(srv)
	if e == nil {
		t.Fatal("nothing was thrown")
	}
	if got := proto.EntityTypeName(tab, e.Type); got != "minecraft:snowball" {
		t.Errorf("the thrown entity is %q, want a snowball", got)
	}
	if math.Abs(e.VZ-1.5) > 0.001 || math.Abs(e.VX) > 0.001 || math.Abs(e.VY) > 0.001 {
		t.Errorf("the throw leaves at (%v,%v,%v), want straight south at 1.5", e.VX, e.VY, e.VZ)
	}
	if e.Y != 65+eyeHeight {
		t.Errorf("the throw starts at y=%v, want the thrower's eyes", e.Y)
	}
	if e.Owner != s.entityID {
		t.Errorf("the throw belongs to entity %d, want %d", e.Owner, s.entityID)
	}
	if s.inv[27].Count != 2 {
		t.Errorf("the stack holds %d after one throw, want 2", s.inv[27].Count)
	}

	// One tick of flight: it falls, it slows, and it keeps going.
	vy, vz := e.VY, e.VZ
	srv.tickEntities()
	if !(e.VY < vy) {
		t.Errorf("vertical speed went from %v to %v: nothing is falling", vy, e.VY)
	}
	if !(e.VZ < vz) || math.Abs(e.VZ-1.5*projectileDrag) > 0.001 {
		t.Errorf("horizontal speed after a tick is %v, want %v", e.VZ, 1.5*projectileDrag)
	}
	if thrown(srv) == nil {
		t.Error("the snowball vanished in mid-air")
	}
}

// A snowball bursts where it hits a block: the entity goes, and the client is
// sent the burst it draws the puff and the sound from.
func TestASnowballBurstsOnABlock(t *testing.T) {
	srv, s, log, tab := thrownServer(t, "minecraft:snowball", 1)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	s.world.SetBlock(0, 66, 3, stone)

	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	for range 5 {
		srv.tickEntities()
	}

	if e := thrown(srv); e != nil {
		t.Errorf("the snowball is still flying at (%v,%v,%v)", e.X, e.Y, e.Z)
	}
	waitFor(t, log, tab.PlayClientboundEntityEvent)
}

// An ender pearl carries whoever threw it to where it lands, and costs them five
// damage for the trip. It does not burst on the thrower's own body on the way
// out: vanilla ignores the owner for the throw's first two ticks, and without
// that a level throw would end where it began.
func TestAnEnderPearlTakesItsThrower(t *testing.T) {
	srv, s, _, tab := thrownServer(t, "minecraft:ender_pearl", 1)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone block state")
	}
	s.world.SetBlock(0, 66, 3, stone)
	health := s.health

	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	if thrown(srv) == nil {
		t.Fatal("no pearl was thrown")
	}
	srv.tickEntities()
	if thrown(srv) == nil {
		t.Fatal("the pearl burst on the thrower's own body")
	}

	for range 5 {
		srv.tickEntities()
	}
	if e := thrown(srv); e != nil {
		t.Fatalf("the pearl is still flying at (%v,%v,%v)", e.X, e.Y, e.Z)
	}
	if s.posZ < 2 {
		t.Errorf("the thrower is at z=%v, want to have been carried to the wall", s.posZ)
	}
	if !(s.health < health) {
		t.Errorf("health is %v after a pearl, want less than %v", s.health, health)
	}
}
