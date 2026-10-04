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

// A bow draws for twenty ticks to full charge and fires at three blocks a tick,
// on BowItem's curve: the charge is ((t/20)^2 + 2t/20) / 3, so twenty ticks is
// one and three blocks a tick, and a draw under a tenth of the way does not fire
// at all.
func TestDrawingABow(t *testing.T) {
	srv, s, _, tab := thrownServer(t, "minecraft:bow", 1)
	arrow, ok := tab.ItemIDs["minecraft:arrow"]
	if !ok {
		t.Fatal("no arrow item")
	}
	s.inv[28] = world.Item{ID: arrow, Count: 5}

	// Two ticks in: below a tenth, so no shot and no arrow spent.
	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	if !s.bowDrawing {
		t.Fatal("the draw did not start")
	}
	srv.age.Add(2)
	s.handlePlayerAction(actionPacket(5, 0, 65, 0, 1, 9))
	if e := thrown(srv); e != nil {
		t.Errorf("a two-tick draw fired an arrow at (%v,%v,%v)", e.X, e.Y, e.Z)
	}
	if s.inv[28].Count != 5 {
		t.Errorf("the abandoned draw spent an arrow: %d left", s.inv[28].Count)
	}

	// A full draw: three blocks a tick, down the look vector, one arrow gone.
	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	srv.age.Add(20)
	s.handlePlayerAction(actionPacket(5, 0, 65, 0, 1, 10))
	e := thrown(srv)
	if e == nil || !e.Arrow {
		t.Fatal("a full draw fired no arrow")
	}
	if got := proto.EntityTypeName(tab, e.Type); got != "minecraft:arrow" {
		t.Errorf("the fired entity is %q, want an arrow", got)
	}
	speed := math.Sqrt(e.VX*e.VX + e.VY*e.VY + e.VZ*e.VZ)
	if math.Abs(speed-3) > 0.2 {
		t.Errorf("the shot leaves at %v, want 3", speed)
	}
	if e.VZ < 0.9*speed {
		t.Errorf("the shot went somewhere else: (%v,%v,%v)", e.VX, e.VY, e.VZ)
	}
	if s.inv[28].Count != 4 {
		t.Errorf("the stack holds %d after the shot, want 4", s.inv[28].Count)
	}
}

// A bow with nothing to shoot does not fire; a bow in creative does.
func TestABowNeedsAnArrow(t *testing.T) {
	srv, s, _, _ := thrownServer(t, "minecraft:bow", 1)
	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	srv.age.Add(20)
	s.handlePlayerAction(actionPacket(5, 0, 65, 0, 1, 9))
	if e := thrown(srv); e != nil {
		t.Error("a bow with no arrows fired one anyway")
	}

	if err := s.setGameMode(gameModeCreative); err != nil {
		t.Fatalf("changing mode: %v", err)
	}
	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	srv.age.Add(20)
	s.handlePlayerAction(actionPacket(5, 0, 65, 0, 1, 10))
	if thrown(srv) == nil {
		t.Error("creative fired nothing")
	}
}

// An arrow spends itself in the mob it hits and hurts it by the shot's speed
// times the arrow's base damage. A level shot at eye height reaches a mob
// standing on the same ground, which is why an entity is a box and not a point:
// the first version of this test had the arrow sail over the zombie's head.
func TestAnArrowHurtsWhatItHits(t *testing.T) {
	srv, s, _, tab := thrownServer(t, "minecraft:bow", 1)
	arrow, ok := tab.ItemIDs["minecraft:arrow"]
	if !ok {
		t.Fatal("no arrow item")
	}
	s.inv[28] = world.Item{ID: arrow, Count: 1}
	zombie := srv.spawnEntity(kindMob, tab.EntityIDs["minecraft:zombie"], 0.5, 65, 3.5)
	full := mobHealthFor(tab, zombie.Type)

	s.handleUseItem(mcproto.NewReader(useItemPacket()))
	srv.age.Add(20)
	s.handlePlayerAction(actionPacket(5, 0, 65, 0, 1, 9))
	if thrown(srv) == nil {
		t.Fatal("the shot did not fire")
	}
	for range 5 {
		srv.tickEntities()
	}

	if zombie.Health >= full {
		t.Errorf("the arrow left the zombie at %v, want less than %v", zombie.Health, full)
	}
	if e := thrown(srv); e != nil {
		t.Errorf("the arrow is still flying at (%v,%v,%v)", e.X, e.Y, e.Z)
	}
}
