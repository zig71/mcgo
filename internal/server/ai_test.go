package server

import (
	"bytes"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

func mobServer(t *testing.T) (*Server, *world.World) {
	t.Helper()
	srv := fluidServer()
	return srv, srv.worldFor(v777.Tables)
}

// A walking mob moves, and the client is told which way it is facing.
func TestMobWalks(t *testing.T) {
	srv, w := mobServer(t)
	zombie := v777.Tables.EntityIDs["minecraft:zombie"]
	e := srv.spawnEntity(kindMob, zombie, 0.5, 65, 0.5)
	e.Walking = true
	e.WanderTicks = 200 // do not change its mind mid-test
	e.Yaw = 0           // south, so +Z

	startZ := e.Z
	for i := 0; i < 20; i++ {
		srv.stepEntity(v777.Tables, w, e)
	}
	if e.Z <= startZ {
		t.Errorf("a mob walking south went from z=%v to z=%v", startZ, e.Z)
	}
	if !e.OnGround {
		t.Error("the mob is not on the ground while walking it")
	}
	if e.LastYaw != e.Yaw {
		t.Errorf("the client was last told yaw %v, the mob faces %v", e.LastYaw, e.Yaw)
	}
}

// A mob that is not walking stays put.
func TestMobStandsStill(t *testing.T) {
	srv, w := mobServer(t)
	e := srv.spawnEntity(kindMob, v777.Tables.EntityIDs["minecraft:cow"], 0.5, 65, 0.5)
	e.Walking = false
	e.WanderTicks = 200
	x, z := e.X, e.Z

	for i := 0; i < 20; i++ {
		srv.stepEntity(v777.Tables, w, e)
	}
	if e.X != x || e.Z != z {
		t.Errorf("a standing mob moved from (%v,%v) to (%v,%v)", x, z, e.X, e.Z)
	}
}

// A block in the way turns the mob rather than letting it walk into it.
func TestMobTurnsAtABlock(t *testing.T) {
	srv, w := mobServer(t)
	stone, ok := srv.blockState(v777.Tables, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	e := srv.spawnEntity(kindMob, v777.Tables.EntityIDs["minecraft:zombie"], 0.5, 65, 0.5)
	e.Walking = true
	e.WanderTicks = 200
	e.Yaw = 0 // south, +Z
	w.SetBlock(0, 65, 1, stone)

	turned := false
	for i := 0; i < 60; i++ {
		srv.stepEntity(v777.Tables, w, e)
		if e.Z >= 1 {
			t.Fatalf("the mob walked into the block at z=1: it is at z=%v", e.Z)
		}
		if e.Yaw != 0 {
			turned = true
		}
	}
	if !turned {
		t.Error("the mob never turned away from the wall")
	}
}

// A player nearby is enough to stop a mob and turn it to face them.
func TestMobLooksAtAPlayer(t *testing.T) {
	srv, w := mobServer(t)
	e := srv.spawnEntity(kindMob, v777.Tables.EntityIDs["minecraft:zombie"], 0.5, 65, 0.5)
	e.Walking = true
	e.WanderTicks = 200
	e.Yaw = 180 // walking north, away from the player below

	sess, _, _ := pipedSession(t, srv)
	sess.posX, sess.posY, sess.posZ = 4.5, 65, 0.5 // due east
	sessionRegisterForTest(srv, sess)

	srv.stepEntity(v777.Tables, w, e)

	// Due east is yaw -90.
	if e.Yaw < -91 || e.Yaw > -89 {
		t.Errorf("the mob faces yaw %v, want about -90 (east)", e.Yaw)
	}
	if e.Walking {
		t.Error("the mob kept walking while looking at the player")
	}
	if e.VX != 0 || e.VZ != 0 {
		t.Errorf("the mob is still moving: v=(%v,%v)", e.VX, e.VZ)
	}
}

// A mob far from everyone gets on with its own business.
func TestMobDecidesForItself(t *testing.T) {
	srv, _ := mobServer(t)
	e := srv.spawnEntity(kindMob, v777.Tables.EntityIDs["minecraft:pig"], 0.5, 65, 0.5)

	walking, standing := 0, 0
	for i := 0; i < 200; i++ {
		srv.decideMob(e)
		if e.WanderTicks <= 0 {
			t.Fatal("a decision set no time before the next one")
		}
		if e.Walking {
			walking++
		} else {
			standing++
		}
	}
	if walking == 0 || standing == 0 {
		t.Errorf("the mob only ever did one thing: walking=%d standing=%d", walking, standing)
	}
}

// The Level Event packet is the event as an int, the position, the data and a
// global flag. Leaving the flag off makes the client fail to decode the packet
// and drop the connection, which is what happened when it was first written.
func TestLevelEventBody(t *testing.T) {
	srv := fluidServer()
	sess, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, sess)

	srv.levelEvent(v777.Tables, 13, 64, -7, levelEventBreak, 4242)

	var p mcproto.Packet
	select {
	case p = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no Level Event packet arrived")
	}
	if p.ID != sess.t.PlayClientboundLevelEvent {
		t.Fatalf("packet id %d, want %d", p.ID, sess.t.PlayClientboundLevelEvent)
	}

	var w mcproto.Writer
	w.WriteInt(levelEventBreak)
	w.WriteLong(packPosition(13, 64, -7))
	w.WriteInt(4242)
	w.WriteBool(false) // not a global event
	want := w.Bytes()
	if !bytes.Equal(p.Data, want) {
		t.Errorf("level event body\n got %x\nwant %x", p.Data, want)
	}
}
