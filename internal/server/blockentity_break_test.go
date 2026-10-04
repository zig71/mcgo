package server

import (
	"testing"

	"mcgo/internal/proto/v777"
)

// Breaking a sign or a banner takes its contents with it. A sign's text and a
// banner's layers live in this server's own stores rather than in the world
// file, so an entry that outlives its block is content coming back after the
// block is gone — and a sign placed there later inherits the old text.
func TestBreakingABannerForgetsItsPatterns(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	tab := v777.Tables
	banner, ok := srv.blockState(tab, "minecraft:white_banner", nil)
	if !ok {
		t.Fatal("no banner block state")
	}
	s.world.SetBlock(4, 65, 4, banner)
	// Any component value will do: the store's contents are bytes.
	s.world.Banners().Put(4, 65, 4, []byte{0x09, 0x00, 0x00, 0x00, 0x00})
	if s.world.Banners().Count() != 1 {
		t.Fatal("the banner was not stored")
	}

	s.handlePlayerAction(actionPacket(3, 4, 65, 4, 1, 5))

	if _, ok := s.world.Banners().Get(4, 65, 4); ok {
		t.Error("the banner's patterns outlived the block")
	}
	if s.world.Banners().Count() != 0 {
		t.Errorf("the store still holds %d banners", s.world.Banners().Count())
	}
	if s.world.Block(4, 65, 4) != tab.BlockAir {
		t.Error("the banner was not broken")
	}
}
