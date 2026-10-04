package server

import (
	"bytes"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// The sound packet: the sound, the source channel, the position in eighths of
// a block, the volume, the pitch, and a seed.
func TestSoundEffectBody(t *testing.T) {
	srv := fluidServer()
	sess, got, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, sess)

	sound, ok := srv.newest.SoundEventIDs["minecraft:block.stone.break"]
	if !ok {
		t.Fatal("no stone break sound in the registry")
	}
	srv.playSound(srv.newest, sound, soundSourceBlocks, 10.5, 64, -3.25, 0.25, 1)

	waitFor(t, log, srv.newest.PlayClientboundSoundEffect)
	select {
	case p := <-got:
		// Everything but the seed, which is random so the client does not
		// deduplicate the same sound played twice.
		var want mcproto.Writer
		want.WriteVarInt(sound + 1) // the holder form: id plus one
		want.WriteVarInt(soundSourceBlocks)
		want.WriteInt(int32(10.5 * 8))
		want.WriteInt(int32(64 * 8))
		want.WriteInt(int32(-3.25 * 8))
		want.WriteFloat(0.25)
		want.WriteFloat(1)
		prefix := want.Bytes()
		if !bytes.HasPrefix(p.Data, prefix) {
			t.Fatalf("sound body\n got %x\nwant prefix %x", p.Data, prefix)
		}
		if len(p.Data) != len(prefix)+8 {
			t.Errorf("the sound packet is %d bytes, want %d plus the seed", len(p.Data), len(prefix))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no sound packet")
	}
}

// A block's sound group is guessed from its name.
func TestBlockSoundGroups(t *testing.T) {
	for _, tc := range []struct{ block, group string }{
		{"minecraft:stone", "stone"},
		{"minecraft:oak_planks", "wood"},
		{"minecraft:oak_log", "wood"},
		{"minecraft:glass", "glass"},
		{"minecraft:white_wool", "wool"},
		{"minecraft:sand", "sand"},
		{"minecraft:gravel", "gravel"},
		{"minecraft:grass_block", "grass"},
		{"minecraft:short_grass", "grass"},
		{"minecraft:diamond_block", "stone"},
	} {
		if got := soundGroupOf(tc.block); got != tc.group {
			t.Errorf("%s is in the %s group, want %s", tc.block, got, tc.group)
		}
	}
}

// Breaking a block plays its particles and its sound.
func TestBreakingABlockMakesASound(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	sess, _, log := pipedSession(t, srv)
	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	sess.world.SetBlock(1, 64, 1, stone)
	sess.posX, sess.posY, sess.posZ = 1.5, 65, 1.5
	sessionRegisterForTest(srv, sess)

	// The serverbound action packet: status 3 (stopped digging, which is the one
	// that breaks the block), the
	// position, the face, and a sequence number.
	var r mcproto.Writer
	r.WriteVarInt(3)
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(7)

	sess.handlePlayerAction(mcproto.NewReader(r.Bytes()))

	if got := sess.world.Block(1, 64, 1); got != tab.BlockAir {
		t.Fatalf("the block survived the break: %d", got)
	}
	waitFor(t, log, tab.PlayClientboundSoundEffect)
	if !log.has(tab.PlayClientboundLevelEvent) {
		t.Errorf("no break particles were sent: %v", log.seen())
	}
}

// soundIDOf reads the sound id out of a Sound Effect body, where it is written
// as a holder: the registry id plus one.
func soundIDOf(t *testing.T, p mcproto.Packet) int32 {
	t.Helper()
	r := mcproto.NewReader(p.Data)
	id, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("reading the sound id: %v", err)
	}
	return id - 1
}

// Hitting a mob plays that mob's own hurt sound, and killing it plays its death
// sound: a mob used to take damage and die in silence. Both go out bound to the
// mob, so the client keeps the sound with the entity.
func TestMobHurtAndDeathSounds(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5
	sessionRegisterForTest(srv, s)

	e := srv.spawnEntity(kindMob, tab.EntityIDs["minecraft:zombie"], 1.5, 65, 0.5)
	srv.hurtMob(tab, e, 1, s)

	want := tab.SoundEventIDs["minecraft:entity.zombie.hurt"]
	sound, bound := soundEntityOf(t, waitPacket(t, got, tab.PlayClientboundSoundEntity))
	if sound != want {
		t.Errorf("the hurt sound is %d, want the zombie's %d", sound, want)
	}
	if bound != e.ID {
		t.Errorf("the hurt sound is bound to entity %d, want the zombie's %d", bound, e.ID)
	}

	// Kill it: the death sound, then the mob is gone. The immunity from the
	// first hit has to pass first, or the second one is ignored.
	e.HurtTicks = 0
	srv.hurtMob(tab, e, 100, s)
	wantDeath := tab.SoundEventIDs["minecraft:entity.zombie.death"]
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID != tab.PlayClientboundSoundEntity {
				continue
			}
			if sound, bound := soundEntityOf(t, p); sound == wantDeath && bound == e.ID {
				return
			}
		case <-deadline:
			t.Fatalf("no death sound for the zombie; wanted %d", wantDeath)
		}
	}
}

// soundEntityOf reads a sound_entity packet: the sound's registry id and the
// entity it is bound to.
func soundEntityOf(t *testing.T, p mcproto.Packet) (int32, int32) {
	t.Helper()
	r := mcproto.NewReader(p.Data)
	sound, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("sound id: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // the sound source
		t.Fatalf("source: %v", err)
	}
	id, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("entity id: %v", err)
	}
	return sound - 1, id
}

// A mob that has no sound of its own falls back to the generic one. Vanilla
// has generic hurt and death sounds but no generic ambient, so a sound that has
// no generic counterpart plays nothing at all.
func TestMobSoundFallsBack(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	// A mob this server has never heard of: the group does not exist, so this
	// is the fallback path.
	srv.playMobSound(tab, "minecraft:not_a_real_mob", "hurt", 1, 65, 1, 1, 1)
	want := tab.SoundEventIDs["minecraft:entity.generic.hurt"]
	if gotID := soundIDOf(t, waitPacket(t, got, tab.PlayClientboundSoundEffect)); gotID != want {
		t.Errorf("the fallback sound is %d, want the generic hurt %d", gotID, want)
	}

	// And an action with no generic sound plays nothing, which is what a
	// creeper's missing ambient sound comes down to.
	if _, ok := tab.SoundEventIDs["minecraft:entity.generic.ambient"]; ok {
		t.Skip("this version has a generic ambient sound")
	}
	srv.playMobSound(tab, "minecraft:creeper", "ambient", 1, 65, 1, 1, 1)
	select {
	case p := <-got:
		t.Errorf("a sound was played for something with none: id %d", p.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

// A chest creaks when it opens and closes; a furnace, which has no such sound,
// stays quiet.
func TestContainerSounds(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.posX, s.posY, s.posZ = 0.5, 65, 0.5

	chest, ok := srv.blockState(tab, "minecraft:chest", nil)
	if !ok {
		t.Fatal("no chest in the block table")
	}
	s.world.SetBlock(1, 65, 0, chest)

	if !s.openContainer(1, 65, 0) {
		t.Fatal("the chest did not open")
	}
	wantOpen := tab.SoundEventIDs["minecraft:block.chest.open"]
	foundOpen := false
	deadline := time.After(2 * time.Second)
	for !foundOpen {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundSoundEffect && soundIDOf(t, p) == wantOpen {
				foundOpen = true
			}
		case <-deadline:
			t.Fatalf("no chest opening sound; wanted %d", wantOpen)
		}
	}

	s.handleContainerClose(mcproto.NewReader([]byte{0x00}))
	wantClose := tab.SoundEventIDs["minecraft:block.chest.close"]
	deadline = time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundSoundEffect && soundIDOf(t, p) == wantClose {
				return
			}
		case <-deadline:
			t.Fatalf("no chest closing sound; wanted %d", wantClose)
		}
	}
}

// A mob walking makes a footstep from the block under it: without this a mob
// crosses a room in silence, which is the one thing a player notices.
func TestMobFootsteps(t *testing.T) {
	srv, w := mobServer(t)
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	s.posX, s.posY, s.posZ = 40, 65, 40 // far away, so no look-at-player
	sessionRegisterForTest(srv, s)

	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	for x := int32(0); x <= 4; x++ {
		for z := int32(-1); z <= 1; z++ {
			w.SetBlock(x, 64, z, stone)
		}
	}
	e := srv.spawnEntity(kindMob, tab.EntityIDs["minecraft:cow"], 0.5, 65, 0.5)
	e.Walking = true
	e.WanderTicks = 500
	e.Yaw = 0              // south, +Z
	e.AmbientTicks = 10000 // so the only sound is a step

	want := tab.SoundEventIDs["minecraft:block.stone.step"]
	if want == 0 {
		t.Fatal("no stone step sound in the registry")
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID == tab.PlayClientboundSoundEffect && soundIDOf(t, p) == want {
				return
			}
		case <-deadline:
			t.Fatal("the walking mob made no footstep")
		}
	}
}
