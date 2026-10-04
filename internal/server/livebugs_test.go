package server

import (
	"bytes"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// A death message is a System Chat packet, which is the text and then whether
// it is an action-bar overlay. The flag was missing, so the client could not
// decode the packet and dropped the connection the first time a player died.
func TestDeathMessageIsDecodable(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	s.invulnerable = false
	sessionRegisterForTest(srv, s)

	s.hurtPlayer(v777.Tables, 100, "minecraft:explosion", nil)
	if !s.dead {
		t.Fatal("the player took 100 damage and survived")
	}

	// The message is the player's name and how they died.
	wantText := deathMessage(s.name, "minecraft:explosion")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID != s.t.PlayClientboundSystemChat {
				continue
			}
			if !bytes.HasPrefix(p.Data, nbtText(wantText)) {
				t.Fatalf("the death message is not the expected text: %x", p.Data)
			}
			if last := p.Data[len(p.Data)-1]; last != 0 {
				t.Errorf("the overlay flag is %d, want 0", last)
			}
			// The flag is one byte past the text: nothing else may follow.
			if len(p.Data) != len(nbtText(wantText))+1 {
				t.Errorf("the packet has %d trailing bytes", len(p.Data)-len(nbtText(wantText))-1)
			}
			return
		case <-deadline:
			t.Fatal("no system chat after the death")
		}
	}
}

// A creative player is invulnerable, which the server tells the client in the
// abilities packet: it must not then hurt them, fall or otherwise.
func TestCreativePlayersAreNotHurt(t *testing.T) {
	srv := fluidServer()
	s, got, log := pipedSession(t, srv) // pipedSession marks the player creative
	sessionRegisterForTest(srv, s)

	s.falling = true
	s.fallFrom = 100
	s.onGround = true
	s.posY = 65
	s.trackFall()

	if s.health != maxHealth {
		t.Errorf("a creative player fell 35 blocks and lost %v health", maxHealth-s.health)
	}
	s.hurtPlayer(v777.Tables, 20, "minecraft:explosion", nil)
	if s.health != maxHealth || s.dead {
		t.Errorf("a creative player was hurt: health %v, dead %v", s.health, s.dead)
	}
	time.Sleep(50 * time.Millisecond)
	if log.has(s.t.PlayClientboundHurtAnimation) {
		t.Error("a creative player was sent the hurt animation")
	}
	if log.has(s.t.PlayClientboundDamageEvent) {
		t.Error("a creative player was sent a damage event")
	}
	select {
	case p := <-got:
		t.Errorf("a creative player was sent packet %d", p.ID)
	default:
	}
}

// An explosion is one packet with the burst and the bang, not one effect per
// block: the per-block events made the blast look like it ate the ground a
// block at a time.
func TestExplosionSendsOneEffect(t *testing.T) {
	srv, w := mobServer(t)
	tab := v777.Tables
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for i := int32(0); i < 20; i++ {
		w.SetBlock(2+i%5, 65, 2+i/5, stone)
	}

	sess, got, log := pipedSession(t, srv)
	sess.posX, sess.posY, sess.posZ = 4, 65, 4
	sessionRegisterForTest(srv, sess)

	srv.explode(tab, w, 4.5, 65, 4.5, tntPower)

	deadline := time.After(2 * time.Second)
	var explode *mcproto.Packet
	for explode == nil {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundExplode {
				explode = &p
			}
		case <-deadline:
			t.Fatalf("no explode packet: %v", log.seen())
		}
	}

	// An empty weighted list, and no per-block break events at all.
	_ = log
	var want mcproto.Writer
	want.WriteDouble(4.5)
	want.WriteDouble(65)
	want.WriteDouble(4.5)
	want.WriteFloat(float32(tntPower)) // radius
	want.WriteInt(0)                   // block count
	want.WriteBool(false)              // no knockback
	want.WriteVarInt(tab.ParticleTypeIDs["minecraft:explosion_emitter"])
	want.WriteVarInt(tab.SoundEventIDs["minecraft:entity.generic.explode"] + 1) // a holder: id plus one
	want.WriteVarInt(0)                                                         // no debris
	want.WriteBool(true)                                                        // play the sound
	if !bytes.Equal(explode.Data, want.Bytes()) {
		t.Errorf("explode body\n got %x\nwant %x", explode.Data, want.Bytes())
	}
}
