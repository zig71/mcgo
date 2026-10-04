package server

import (
	"strings"
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// A dummy objective, a score in it, and the sidebar showing it: the three
// packets a sidebar is made of.
func TestScoreboardSidebar(t *testing.T) {
	srv := fluidServer()
	s, got, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if reply := s.runCommand("scoreboard objectives add points dummy Points"); !strings.Contains(reply, "points") {
		t.Errorf("adding an objective replied %q", reply)
	}
	waitFor(t, log, s.t.PlayClientboundSetObjective)

	if !srv.setScore("Solo", "points", 42) {
		t.Fatal("the score was not set")
	}
	waitFor(t, log, s.t.PlayClientboundSetScore)

	// The sidebar display goes out with the slot the client expects.
	if reply := s.runCommand("scoreboard objectives setdisplay sidebar points"); !strings.Contains(reply, "sidebar") {
		t.Errorf("setdisplay replied %q", reply)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID != s.t.PlayClientboundSetDisplayObjective {
				continue
			}
			r := mcproto.NewReader(p.Data)
			slot, err := r.ReadVarInt()
			if err != nil {
				t.Fatalf("slot: %v", err)
			}
			if slot != sidebarSlot {
				t.Errorf("the objective was displayed in slot %d, want the sidebar %d", slot, sidebarSlot)
			}
			name, err := r.ReadString()
			if err != nil {
				t.Fatalf("objective name: %v", err)
			}
			if name != "points" {
				t.Errorf("the sidebar shows %q, want points", name)
			}
			if r.Remaining() != 0 {
				t.Errorf("%d trailing bytes", r.Remaining())
			}
			return
		case <-deadline:
			t.Fatal("no display objective packet")
		}
	}
}

// An objective the server cannot count is refused rather than showing a number
// that never moves.
func TestScoreboardRefusesUnknownCriteria(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if reply := s.runCommand("scoreboard objectives add x notACriterion"); !strings.Contains(reply, "Unknown criterion") {
		t.Errorf("replied %q", reply)
	}
	if reply := s.runCommand("scoreboard objectives setdisplay sidebar nothing"); !strings.Contains(reply, "Unknown objective") {
		t.Errorf("replied %q", reply)
	}
	if reply := s.runCommand("scoreboard players set Solo points nonsense"); !strings.Contains(reply, "Not a number") {
		t.Errorf("replied %q", reply)
	}
}

// A health objective follows the player's health, so the sidebar is not empty
// the moment it is made.
func TestHealthObjectiveTracksHealth(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.invulnerable = false

	if !srv.addObjective("hp", "health", "Health") {
		t.Fatal("a health objective was refused")
	}
	// The objective starts from what the server knows.
	waitFor(t, log, s.t.PlayClientboundSetScore)

	s.hurtPlayer(tab, 5, "minecraft:fall", nil)
	srv.trackScore("health", s.name, int32(s.health+0.5))

	if got := srv.board.scores["hp"][s.name]; got != 15 {
		t.Errorf("the health score is %d, want 15", got)
	}
}
