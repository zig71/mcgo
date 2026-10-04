package world

import (
	"bytes"
	"path/filepath"
	"testing"

	"mcgo/internal/mcproto"
)

// bannerPatterns builds the component value a client sends for a banner with
// the given layers: a TAG_List of unnamed compounds, each a pattern and a
// colour, which is BannerPatternLayers' codec — and therefore the block
// entity's patterns field too.
func bannerPatterns(layers [][2]string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x09) // TAG_List
	w.WriteByte(0x0A) // of TAG_Compound
	w.WriteInt(int32(len(layers)))
	for _, l := range layers {
		w.WriteByte(0x0A) // the element compound, unnamed inside a list
		writeNBTString(&w, "pattern", l[0])
		writeNBTString(&w, "color", l[1])
		w.WriteByte(0x00) // TAG_End of the layer
	}
	return w.Bytes()
}

// The block entity NBT has to carry the layers under "patterns", and omit the
// field when there are none — a plain banner's block entity is only its id.
func TestBannerNBT(t *testing.T) {
	value := bannerPatterns([][2]string{{"minecraft:base", "red"}, {"minecraft:border", "white"}})
	nbt := BannerNBT(value)
	if !bytes.Contains(nbt, []byte("patterns")) {
		t.Fatalf("the banner NBT has no patterns field: %x", nbt)
	}
	if !bytes.Contains(nbt, []byte("minecraft:base")) || !bytes.Contains(nbt, []byte("white")) {
		t.Errorf("the layers did not survive: %x", nbt)
	}

	// The field is the list itself, byte for byte, right after the name.
	i := bytes.Index(nbt, []byte("patterns"))
	list := nbt[i+len("patterns"):]
	if !bytes.HasPrefix(list, value[1:]) {
		t.Errorf("the patterns field is not the component's list\n got %x\nwant %x", list[:12], value[1:13])
	}

	plain := BannerNBT(bannerPatterns(nil))
	if bytes.Contains(plain, []byte("patterns")) {
		t.Errorf("a banner with no layers still carries a patterns field: %x", plain)
	}
	if !bytes.Contains(plain, []byte("minecraft:banner")) {
		t.Errorf("the banner NBT does not name its block entity: %x", plain)
	}
}

// A banner placed with layers has to appear in its chunk's block entity list
// with those layers, because that list is the only place the client reads them
// from when it first sees the chunk.
func TestChunkBlockEntitiesCarryBannerPatterns(t *testing.T) {
	const bannerState, bannerType = 42, 21
	cfg := FlatConfig{
		Air: 0, Bedrock: 1, Dirt: 2, Grass: 3,
		MinY: -64, Height: 384, SurfaceY: 64,
		SignBlockEntityType: 8,
		BlockEntityState:    map[int32]BlockEntityType{bannerState: {ID: bannerType, Name: "minecraft:banner"}},
	}
	w := NewWorld(cfg, NewEdits(), NewSigns(), NewBanners())
	w.SetBlock(1, 65, 1, bannerState)
	w.Banners().Put(1, 65, 1, bannerPatterns([][2]string{{"minecraft:base", "blue"}}))

	bes := w.chunkBlockEntities(0, 0)
	if len(bes) != 1 {
		t.Fatalf("%d block entities, want 1", len(bes))
	}
	// DyeColor's codec is the bare name, so the layer reads "blue", not
	// "minecraft:blue": match the length-prefixed value to be sure.
	if !bytes.Contains(bes[0].NBT, []byte("\x00\x04blue")) {
		t.Errorf("the chunk's block entity has no layers: %x", bes[0].NBT)
	}
	if !bytes.Contains(bes[0].NBT, []byte("minecraft:base")) {
		t.Errorf("the chunk's block entity has no pattern: %x", bes[0].NBT)
	}
}

// Banners survive a save and load, patterns and all.
func TestBannersPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "banners.json")
	patterns := bannerPatterns([][2]string{{"minecraft:base", "lime"}, {"minecraft:creeper", "black"}})

	b := NewBanners()
	b.Put(3, 70, -5, patterns)
	if err := b.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	fresh := NewBanners()
	if err := fresh.Load(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := fresh.Get(3, 70, -5)
	if !ok {
		t.Fatal("the banner did not come back")
	}
	if !bytes.Equal(got, patterns) {
		t.Errorf("the layers changed across a save\n got %x\nwant %x", got, patterns)
	}

	fresh.Delete(3, 70, -5)
	if _, ok := fresh.Get(3, 70, -5); ok {
		t.Error("a broken banner is still stored")
	}
}
