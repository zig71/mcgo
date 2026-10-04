package server

import (
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// A mob's own noise goes out bound to the mob (sound_entity), so the client
// keeps it with the entity: a creeper that walks away takes its hiss with it,
// which a positional sound does not do. The tail of the packet is the positional
// one's, with the entity id where the coordinates were.
func TestMobNoiseFollowsTheMob(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	srv.register(s)
	tab := v777.Tables
	if tab.PlayClientboundSoundEntity == 0 {
		t.Fatal("26.3 has no sound_entity packet")
	}
	// A cow, because it has an ambient sound: a creeper does not, which the
	// fallback test next door documents.
	srv.playMobSoundFrom(tab, "minecraft:cow", "ambient", 77, 0, 65, 0, 1, 1)

	r := mcproto.NewReader(firstPacketWith(t, got, tab.PlayClientboundSoundEntity))
	sound, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("sound: %v", err)
	}
	want := tab.SoundEventIDs["minecraft:entity.cow.ambient"] + 1
	if sound != want {
		t.Errorf("the sound is %d, want the cow's ambient %d (registry id plus one)", sound, want)
	}
	source, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if source != soundSourceHostile {
		t.Errorf("the source is %d, want %d (hostile)", source, soundSourceHostile)
	}
	id, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("entity id: %v", err)
	}
	if id != 77 {
		t.Errorf("the sound is bound to entity %d, want 77", id)
	}
	if _, err := r.ReadFloat(); err != nil {
		t.Fatalf("volume: %v", err)
	}
	if _, err := r.ReadFloat(); err != nil {
		t.Fatalf("pitch: %v", err)
	}
	if _, err := r.ReadLong(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if r.Remaining() != 0 {
		t.Errorf("%d trailing bytes in the sound packet", r.Remaining())
	}
}
