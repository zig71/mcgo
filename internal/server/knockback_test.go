package server

import (
	"math"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// A hit knocks a mob back. The velocity goes out with the low-precision Vec3
// codec, which this server previously only ever wrote in its zero form — the
// Add Entity packet — so nothing could move an entity after it had spawned.
func TestHittingAMobKnocksItBack(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5
	srv.register(s)

	// Directly east of the player, so the knockback is +x and +y.
	e := srv.spawnEntity(kindMob, tab.EntityIDs["minecraft:zombie"], 1.5, 65, 0.5)
	srv.hurtMob(tab, e, 1, s)

	body := firstPacketWith(t, got, tab.PlayClientboundSetEntityMotion)
	r := mcproto.NewReader(body)
	id, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("entity id: %v", err)
	}
	if id != e.ID {
		t.Errorf("the motion is for entity %d, want %d", id, e.ID)
	}
	// The packed velocity is a byte, a byte and a 32-bit int. 0.4 has scale 1,
	// which is not partial, so no scale VarInt follows.
	if rest := r.Remaining(); rest != 6 {
		t.Errorf("the motion is %d bytes, want 6 (a byte, a byte and an int)", rest)
	}
	if first, err := r.ReadByte(); err != nil || first == 0 {
		t.Errorf("the motion starts with %d (err %v), want a non-zero packed byte", first, err)
	}
	// And the simulation agrees: away, and upward.
	if math.Abs(e.VX-0.4) > 1e-9 || math.Abs(e.VY-0.4) > 1e-9 {
		t.Errorf("velocity is (%.3f, %.3f), want (0.4, 0.4)", e.VX, e.VY)
	}
	if math.Abs(e.VZ) > 1e-9 {
		t.Errorf("velocity z is %.3f, want none", e.VZ)
	}
}
