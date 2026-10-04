package server

import (
	"bytes"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v772"
	"mcgo/internal/proto/v776"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// bannerPatternBytes builds the banner_patterns component value the client
// sends: a TAG_List of unnamed compounds, each a pattern and a dye colour.
func bannerPatternBytes(layers [][2]string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x09) // TAG_List
	w.WriteByte(0x0A) // of TAG_Compound
	w.WriteInt(int32(len(layers)))
	for _, l := range layers {
		w.WriteByte(0x0A)
		for _, f := range [2]string{l[0], l[1]} {
			w.WriteByte(0x08)
			w.WriteUShort(uint16(len(f)))
			w.WriteBytes([]byte(f))
		}
		w.WriteByte(0x00)
	}
	return w.Bytes()
}

// A banner placed from a stack in creative keeps its pattern layers: they are
// recorded for the block entity, and the chunk that carries it then has them.
func TestPlacedBannerKeepsItsPatterns(t *testing.T) {
	s := placer(0, 0)
	s.heldSlot = 0
	value := bannerPatternBytes([][2]string{{"minecraft:base", "red"}, {"minecraft:skull", "black"}})
	s.inv[27] = world.Item{
		ID:    itemOf(t, "minecraft:white_banner"),
		Count: 1,
		Components: []world.Component{{
			Type:  s.t.BannerPatternsComponent,
			Value: value,
		}},
	}

	s.rememberBannerPatterns(1, 65, 1)
	got, ok := s.world.Banners().Get(1, 65, 1)
	if !ok {
		t.Fatal("the banner's layers were not recorded")
	}
	if !bytes.Equal(got, value) {
		t.Errorf("the layers changed on the way in\n got %x\nwant %x", got, value)
	}

	// A plain banner placed where a patterned one was takes its place, so no
	// layers are left behind for it.
	s.inv[27] = world.Item{ID: itemOf(t, "minecraft:white_banner"), Count: 1}
	s.rememberBannerPatterns(1, 65, 1)
	if _, ok := s.world.Banners().Get(1, 65, 1); ok {
		t.Error("a plain banner kept the layers of the one before it")
	}
}

// The component ID has to be the one the client uses, or the lookup finds
// nothing and every banner comes out plain.
func TestBannerPatternsComponentID(t *testing.T) {
	for _, id := range []int32{v772.Tables.BannerPatternsComponent, v776.Tables.BannerPatternsComponent, v777.Tables.BannerPatternsComponent} {
		if id == 0 {
			t.Error("a version's tables have no minecraft:banner_patterns component id")
		}
	}
}
