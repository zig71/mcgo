package server

import (
	"strings"
	"testing"

	"mcgo/internal/proto/v777"
)

// hasEntityTypeAt reports whether an entity of that type stands at (x, z).
func hasEntityTypeAt(srv *Server, typeID int32, x, z float64) bool {
	for _, e := range srv.entities() {
		if e.Type == typeID && e.X == x && e.Z == z {
			return true
		}
	}
	return false
}

// The command tree the client is sent is vanilla's own and already offers
// summon, so the server has to answer it: picking it from the completion list
// should give an entity, not "Unknown command".
func TestSummonSpawnsTheEntity(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	tab := v777.Tables

	if got := s.runCommand("summon zombie 10 65 10"); got != "Summoned zombie" {
		t.Errorf("reply = %q", got)
	}
	if !hasEntityTypeAt(srv, tab.EntityIDs["minecraft:zombie"], 10, 10) {
		t.Errorf("no zombie at 10,65,10 (%d entities)", len(srv.entities()))
	}
	// Without coordinates it lands on the player, and the namespace is
	// optional, both as in vanilla.
	if got := s.runCommand("summon minecraft:cow"); got != "Summoned cow" {
		t.Errorf("reply = %q", got)
	}
	if !hasEntityTypeAt(srv, tab.EntityIDs["minecraft:cow"], 0.5, 0.5) {
		t.Errorf("the cow did not land on the player (%d entities)", len(srv.entities()))
	}
	// TNT keeps its own behaviour: primed, with a fuse, not a mob.
	s.runCommand("summon tnt 1 65 1")
	primed := false
	for _, e := range srv.entities() {
		if e.Kind == kindTNT && e.Fuse > 0 {
			primed = true
		}
	}
	if !primed {
		t.Error("the summoned TNT is not a primed entity")
	}
}

// An entity name the version does not have is a reply, not a crash, and a bare
// /summon says how it is used.
func TestSummonRejectsBadArguments(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)

	if got := s.runCommand("summon not_a_mob"); got != "Unknown entity: not_a_mob" {
		t.Errorf("reply = %q", got)
	}
	if got := s.runCommand("summon"); !strings.HasPrefix(got, "Usage:") {
		t.Errorf("reply = %q", got)
	}
	if got := s.runCommand("summon zombie here 65 1"); got != "Not a number: here" {
		t.Errorf("reply = %q", got)
	}
}
