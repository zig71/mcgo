package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// The client asks for its statistics, and gets a map of them: how many there
// are, then each as a kind, a value and a count.
func TestStatisticsOnRequest(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	srv.countStat(s.uuid, statCustom, statDeaths)
	srv.countStat(s.uuid, statMined, "minecraft:stone")

	// The client's request: a client command with action 1.
	var r mcproto.Writer
	r.WriteVarInt(1)
	s.handleClientCommand(mcproto.NewReader(r.Bytes()))
	waitFor(t, log, tab.PlayClientboundAwardStatistics)

	var p mcproto.Packet
	select {
	case p = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no statistics packet")
	}

	read := mcproto.NewReader(p.Data)
	count, err := read.ReadVarInt()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("the packet holds %d statistics, want 3 (deaths, stone mined, play time)", count)
	}
	saw := map[[2]int32]int32{}
	for i := int32(0); i < count; i++ {
		kind, err := read.ReadVarInt()
		if err != nil {
			t.Fatalf("kind %d: %v", i, err)
		}
		value, err := read.ReadVarInt()
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		n, err := read.ReadVarInt()
		if err != nil {
			t.Fatalf("count %d: %v", i, err)
		}
		saw[[2]int32{kind, value}] = n
	}
	if read.Remaining() != 0 {
		t.Errorf("%d trailing bytes", read.Remaining())
	}

	if got := saw[[2]int32{tab.StatTypeIDs[statCustom], tab.CustomStatIDs[statDeaths]}]; got != 1 {
		t.Errorf("deaths are %d, want 1", got)
	}
	if got := saw[[2]int32{tab.StatTypeIDs[statMined], tab.BlockIDs["minecraft:stone"]}]; got != 1 {
		t.Errorf("stone mined is %d, want 1", got)
	}
	if _, ok := saw[[2]int32{tab.StatTypeIDs[statCustom], tab.CustomStatIDs[statPlayTime]}]; !ok {
		t.Error("play time is missing from the statistics")
	}
}

// Breaking a block counts it, and dying counts that.
func TestStatisticsAreCounted(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.invulnerable = false

	stone, ok := srv.blockState(tab, "minecraft:stone", nil)
	if !ok {
		t.Fatal("no stone")
	}
	s.world.SetBlock(1, 64, 1, stone)
	s.posX, s.posY, s.posZ = 1.5, 65, 1.5

	var r mcproto.Writer
	r.WriteVarInt(3)
	r.WriteLong(packPosition(1, 64, 1))
	r.WriteByte(1)
	r.WriteVarInt(7)
	s.handlePlayerAction(mcproto.NewReader(r.Bytes()))

	if got := srv.statValue(s.uuid, statMined, "minecraft:stone"); got != 1 {
		t.Errorf("stone mined is %d, want 1", got)
	}
	if got := srv.statValue(s.uuid, statMined, "minecraft:dirt"); got != 0 {
		t.Errorf("dirt mined is %d, want 0", got)
	}

	s.hurtPlayer(tab, 100, "minecraft:explosion", nil)
	if got := srv.statValue(s.uuid, statCustom, statDeaths); got != 1 {
		t.Errorf("deaths are %d, want 1", got)
	}
}

// Statistics are per player: one player's mining is not on another's screen.
func TestStatisticsArePerPlayer(t *testing.T) {
	srv := fluidServer()
	one, _, _ := pipedSession(t, srv)
	one.uuid[0] = 1
	two, _, _ := pipedSession(t, srv)
	two.uuid[0] = 2
	sessionRegisterForTest(srv, one)
	sessionRegisterForTest(srv, two)

	srv.countStat(one.uuid, statMined, "minecraft:stone")

	if got := srv.statValue(one.uuid, statMined, "minecraft:stone"); got != 1 {
		t.Errorf("the player who mined holds %d, want 1", got)
	}
	if got := srv.statValue(two.uuid, statMined, "minecraft:stone"); got != 0 {
		t.Errorf("the other player holds %d, want 0", got)
	}
}
