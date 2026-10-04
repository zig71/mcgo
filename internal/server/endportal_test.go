package server

import (
	"testing"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// An eye of ender fills an end portal frame. The eye is part of the frame's
// state rather than a block entity — the client draws it from the block update —
// and the eye is spent in survival. Nothing could trigger the frame before, so
// an eye of ender did nothing at all.
func TestEyeOfEnderFillsAnEndPortalFrame(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	tab := v777.Tables
	frame, ok := srv.blockState(tab, "minecraft:end_portal_frame", nil)
	if !ok {
		t.Fatal("no end portal frame block state")
	}
	s.world.SetBlock(4, 65, 4, frame)
	if _, props, _ := srv.blockStateName(tab, s.world.Block(4, 65, 4)); props["eye"] != "false" {
		t.Fatalf("the frame starts with eye=%q, want false", props["eye"])
	}

	eye, ok := tab.ItemIDs["minecraft:ender_eye"]
	if !ok {
		t.Fatal("no eye of ender item")
	}
	s.inv[27] = world.Item{ID: eye, Count: 1}
	s.heldSlot = 0
	s.gameMode = gameModeSurvival

	if !s.useEndPortalFrame("minecraft:ender_eye", 4, 65, 4, eye, 21) {
		t.Fatal("the eye was not used on the frame")
	}
	if _, props, ok := srv.blockStateName(tab, s.world.Block(4, 65, 4)); !ok || props["eye"] != "true" {
		t.Errorf("the frame is %v, want eye=true", props)
	}
	if s.inv[27].Count != 0 {
		t.Errorf("the eye was not spent: %d left", s.inv[27].Count)
	}

	// A frame that already has its eye is not filled twice.
	s.inv[27] = world.Item{ID: eye, Count: 1}
	if s.useEndPortalFrame("minecraft:ender_eye", 4, 65, 4, eye, 22) {
		t.Error("an eye was used on a frame that already has one")
	}
	if s.inv[27].Count != 1 {
		t.Error("the second eye was spent anyway")
	}
}
