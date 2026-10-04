package server

import (
	"bytes"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// nlString encodes a network-NBT string tag: the bare form a plain custom name
// takes on the wire.
func nlString(s string) []byte {
	out := []byte{0x08, byte(len(s) >> 8), byte(len(s))}
	return append(out, s...)
}

// nlCompoundText encodes {"text": s} as network NBT.
func nlCompoundText(s string) []byte {
	out := []byte{0x0A, 0x08, 0x00, 0x04}
	out = append(out, "text"...)
	out = append(out, byte(len(s)>>8), byte(len(s)))
	out = append(out, s...)
	return append(out, 0x00)
}

// creativePatch builds the delimited component patch a creative-mode slot
// carries: counts, then type/length/value for each set component, then a type
// for each removed one.
func creativePatch(added []world.Component, removed []int32) []byte {
	out := []byte{byte(len(added)), byte(len(removed))}
	for _, c := range added {
		out = append(out, byte(c.Type), byte(len(c.Value)))
		out = append(out, c.Value...)
	}
	for _, typ := range removed {
		out = append(out, byte(typ))
	}
	return out
}

func TestCreativeComponentsReadsValues(t *testing.T) {
	const customName = 6
	raw := creativePatch([]world.Component{{Type: customName, Value: nlString("Excalibur")}}, nil)

	got, err := creativeComponents(mcproto.NewReader(raw))
	if err != nil {
		t.Fatalf("creativeComponents: %v", err)
	}
	if len(got) != 1 || got[0].Type != customName || !bytes.Equal(got[0].Value, nlString("Excalibur")) {
		t.Fatalf("components = %+v", got)
	}
	if name := textComponentName(got[0].Value); name != "Excalibur" {
		t.Errorf("name = %q", name)
	}
}

// An unrecognised component is kept verbatim rather than dropped: that is what
// lets a book's pages or banner patterns survive without an encoder for them.
func TestCreativeComponentsKeepUnknownOnes(t *testing.T) {
	const unknown = 77
	raw := creativePatch([]world.Component{
		{Type: unknown, Value: []byte{1, 2, 3, 4}},
		{Type: 6, Value: nlCompoundText("Sparkle")},
	}, []int32{9})

	got, err := creativeComponents(mcproto.NewReader(raw))
	if err != nil {
		t.Fatalf("creativeComponents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d components, want 3", len(got))
	}
	if got[0].Type != unknown || !bytes.Equal(got[0].Value, []byte{1, 2, 3, 4}) {
		t.Errorf("unknown component was not kept: %+v", got[0])
	}
	if name := textComponentName(got[1].Value); name != "Sparkle" {
		t.Errorf("compound name = %q", name)
	}
	if !got[2].Removed || got[2].Type != 9 {
		t.Errorf("removal was not recorded: %+v", got[2])
	}
}

func TestCreativeComponentsRejectsNonsense(t *testing.T) {
	// A patch claiming 300 components is not something a client sends, and
	// believing it would allocate on demand.
	var raw []byte
	raw = append(raw, 0xff, 0x02) // 383, a two-byte VarInt
	raw = append(raw, 0x00)
	if _, err := creativeComponents(mcproto.NewReader(raw)); err == nil {
		t.Error("an absurd component count was accepted")
	}
}

// What the client sends (delimited) must be what we send back (not delimited),
// and a removal must survive the round trip.
func TestComponentRoundTrip(t *testing.T) {
	s := &session{t: v777.Tables}
	raw := creativePatch(
		[]world.Component{{Type: 6, Value: nlString("Named")}},
		[]int32{12},
	)
	comps, err := creativeComponents(mcproto.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	it := world.Item{ID: 5, Count: 1, Components: comps}

	var w mcproto.Writer
	s.writeSlot(&w, it)

	want := []byte{
		0x01, 0x05, // count, item id
		0x01, 0x01, // one component set, one removed
		0x06, // component type 6 (custom_name)
	}
	want = append(want, nlString("Named")...)
	want = append(want, 0x0c) // the removal, as a bare type id

	if got := w.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("slot\n got %x\nwant %x", got, want)
	}
}

// Items with different components must not stack together.
func TestComponentsKeepItemsApart(t *testing.T) {
	plain := world.Item{ID: 5, Count: 1}
	named := world.Item{ID: 5, Count: 1}
	setItemName(v777.Tables, &named, "Excalibur")

	if plain.Same(named) {
		t.Error("a named item was treated as the same as a plain one")
	}
	other := world.Item{ID: 5, Count: 1}
	setItemName(v777.Tables, &other, "Other")
	if named.Same(other) {
		t.Error("two differently named items were treated as the same")
	}
	if !plain.Same(world.Item{ID: 5, Count: 64}) {
		t.Error("the same item with a different count is still the same item")
	}
}

// Setting a component twice replaces it rather than adding a second entry, and
// removing it takes the entry away entirely.
func TestSetAndRemoveComponent(t *testing.T) {
	it := world.Item{ID: 5, Count: 1}
	it.SetComponent(6, []byte{1})
	it.SetComponent(6, []byte{2})
	if len(it.Components) != 1 {
		t.Fatalf("setting twice left %d components", len(it.Components))
	}
	if v, _ := it.Component(6); !bytes.Equal(v, []byte{2}) {
		t.Errorf("value = %x, want 02", v)
	}
	it.RemoveComponent(6)
	if _, ok := it.Component(6); ok || len(it.Components) != 0 {
		t.Errorf("removal left %+v", it.Components)
	}
}
