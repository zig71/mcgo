package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// A boss bar arrives as: its id, the ADD operation, the name component, the
// progress, the colour, the overlay, and the flags byte.
func TestBossBarAddBody(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if reply := s.runCommand("bossbar add raid Raid white progress"); !strings.Contains(reply, "raid") {
		t.Errorf("adding replied %q", reply)
	}
	waitFor(t, log, tab.PlayClientboundBossEvent)

	var p mcproto.Packet
	select {
	case p = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no boss bar packet")
	}

	var want mcproto.Writer
	id := barID("raid")
	want.WriteBytes(id[:])
	want.WriteVarInt(bossAdd)
	want.WriteBytes(nbtText("Raid"))
	want.WriteFloat(0)
	want.WriteVarInt(bossColors["white"])
	want.WriteVarInt(bossOverlays["progress"])
	want.WriteByte(0)
	if !bytes.Equal(p.Data, want.Bytes()) {
		t.Errorf("boss bar add\n got %x\nwant %x", p.Data, want.Bytes())
	}
}

// Progress moves the bar; a joining player is sent the bars that exist.
func TestBossBarProgressAndJoin(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	s.runCommand("bossbar add raid Raid red notched_10")
	s.runCommand("bossbar set raid value 0.5")

	deadline := time.After(2 * time.Second)
	sawProgress := false
	for !sawProgress {
		select {
		case p := <-got:
			r := mcproto.NewReader(p.Data)
			if _, err := r.ReadBytes(16); err != nil {
				t.Fatalf("id: %v", err)
			}
			op, err := r.ReadVarInt()
			if err != nil {
				t.Fatalf("operation: %v", err)
			}
			if op != bossProgress {
				continue
			}
			progress, err := r.ReadFloat()
			if err != nil {
				t.Fatalf("progress: %v", err)
			}
			if progress != 0.5 {
				t.Errorf("progress is %v, want 0.5", progress)
			}
			if r.Remaining() != 0 {
				t.Errorf("%d trailing bytes", r.Remaining())
			}
			sawProgress = true
		case <-deadline:
			t.Fatal("no progress update")
		}
	}

	// A player joining now is told about the bar.
	joiner, joinerGot, joinerLog := pipedSession(t, srv)
	joiner.entityID = 30
	srv.sendBossBars(joiner)
	waitFor(t, joinerLog, tab.PlayClientboundBossEvent)
	select {
	case p := <-joinerGot:
		raidID := barID("raid")
		if !bytes.HasPrefix(p.Data, raidID[:]) {
			t.Errorf("the joining player got a different bar: %x", p.Data[:20])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the joining player was told about nothing")
	}

	// And removing it takes it away everywhere.
	if reply := s.runCommand("bossbar remove raid"); !strings.Contains(reply, "Removed") {
		t.Errorf("removing replied %q", reply)
	}
	if reply := s.runCommand("bossbar list"); !strings.Contains(reply, "No boss bars") {
		t.Errorf("the list replied %q", reply)
	}
	if reply := s.runCommand("bossbar add x X nope progress"); !strings.Contains(reply, "Unknown colour") {
		t.Errorf("a bad colour replied %q", reply)
	}
	if reply := s.runCommand("bossbar set x value nonsense"); !strings.Contains(reply, "Not a number") {
		t.Errorf("a bad value replied %q", reply)
	}
}
