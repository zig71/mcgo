package server

import (
	"testing"
	"time"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// waitFor waits until a packet id has been seen. The test session's reader
// runs in its own goroutine, so a log has to be given a moment: checking
// immediately is a race, and checking for absence needs the arrival of
// whatever was expected first.
func waitFor(t *testing.T, l *packetLog, id int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l.has(id) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("packet %d never arrived: %v", id, l.seen())
}

// An entity's packets go to the players who can see it, not to the whole
// server.
func TestEntityPacketsFollowViewers(t *testing.T) {
	srv := fluidServer()
	near, _, nearLog := pipedSession(t, srv)
	sessionRegisterForTest(srv, near)
	far, _, farLog := pipedSession(t, srv)
	far.entityID = 12
	far.posX, far.posY, far.posZ = 400, 65, 400
	sessionRegisterForTest(srv, far)

	e := srv.spawnEntity(kindMob, v777.Tables.EntityIDs["minecraft:zombie"], 2, 65, 0.5)
	waitFor(t, nearLog, srv.newest.PlayClientboundAddEntity)
	if farLog.has(srv.newest.PlayClientboundAddEntity) {
		t.Errorf("the player 400 blocks away was told about the mob: %v", farLog.seen())
	}

	// A move, a turn, and a despawn all stay local.
	e.Yaw = 90
	e.Walking = true
	srv.stepEntity(srv.newest, srv.worldFor(srv.newest), e)
	waitFor(t, nearLog, srv.newest.PlayClientboundMoveEntityPosRot)
	if farLog.has(srv.newest.PlayClientboundMoveEntityPosRot) {
		t.Errorf("the move was sent 400 blocks away: %v", farLog.seen())
	}

	srv.removeEntity(e.ID)
	waitFor(t, nearLog, srv.newest.PlayClientboundRemoveEntities)
	if farLog.has(srv.newest.PlayClientboundRemoveEntities) {
		t.Errorf("the despawn was sent 400 blocks away: %v", farLog.seen())
	}
}

// A player joining an active world is told about the entities already there,
// and only the ones near them.
func TestJoiningPlayerSeesExistingEntities(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	zombie := tab.EntityIDs["minecraft:zombie"]
	srv.spawnEntity(kindMob, zombie, 3, 65, 0.5)
	srv.spawnEntity(kindMob, zombie, 400, 65, 400)
	srv.spawnItemEntity(tab, itemStack(t, "minecraft:stone", 2), 4, 65, 0.5)

	sess, got, _ := pipedSession(t, srv)
	srv.sendVisibleEntities(sess)

	deadline := time.After(300 * time.Millisecond)
	added := 0
	for {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundAddEntity {
				added++
			}
		case <-deadline:
			// Two mobs and one item exist; only the two nearby ones count.
			if added != 2 {
				t.Fatalf("%d entities were announced to the joining player, want 2", added)
			}
			return
		}
	}
}

// itemStack is a stack with an item id looked up by name.
func itemStack(t *testing.T, name string, count int32) world.Item {
	t.Helper()
	id, ok := v777.Tables.ItemIDs[name]
	if !ok {
		t.Fatalf("%s is not an item in the registry", name)
	}
	return world.Item{ID: id, Count: count}
}

// An entity well below a player is still in view: the crater drops of an
// explosion sit 30-odd blocks down, and a client that stopped hearing about
// them would show an empty hole.
func TestEntitiesBelowThePlayerAreVisible(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	sess, got, _ := pipedSession(t, srv)
	sess.posX, sess.posY, sess.posZ = 403.5, 65, 400.5
	sessionRegisterForTest(srv, sess)

	if !sess.seesPos(402.5, 28.5, 400.5) {
		t.Fatal("a drop 36 blocks below the player is out of view")
	}

	srv.spawnItemEntity(tab, itemStack(t, "minecraft:stone", 1), 402.5, 28.5, 400.5)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundAddEntity {
				return
			}
		case <-deadline:
			t.Fatal("the drop below the player was never announced")
		}
	}
}
