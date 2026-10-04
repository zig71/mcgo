package nbt

import (
	"bytes"
	"testing"
)

// TestRoundTrip writes a compound with one field of every interesting type and
// reads it back. The writer exists so block entities can be re-serialised on
// their way to the wire, so a silent encoding bug here would corrupt them.
func TestRoundTrip(t *testing.T) {
	src := Tag{Type: Compound, Comp: map[string]Tag{
		"name":    {Type: String, Str: "minecraft:sign"},
		"count":   {Type: Int, Num: -7},
		"flag":    {Type: Byte, Num: 1},
		"wide":    {Type: Long, Num: 1 << 40},
		"ratio":   {Type: Double, Flt: 0.5},
		"back":    {Type: String, Str: "TEEST"},
		"empty":   {Type: String, Str: ""},
		"data":    {Type: LongArray, Longs: []int64{1, -1, 42}},
		"indices": {Type: IntArray, Ints: []int32{3, 4, 5}},
		"raw":     {Type: ByteArray, Bytes: []byte{0, 1, 2, 255}},
		"lines": {Type: List, List: []Tag{
			{Type: String, Str: `{"text":"a"}`},
			{Type: String, Str: ""},
		}},
		"nested": {Type: Compound, Comp: map[string]Tag{
			"color": {Type: String, Str: "black"},
		}},
		// The shape chests and spawners use: a list of compounds, and a list of
		// lists, neither of which the original round-trip covered.
		"potentials": {Type: List, List: []Tag{
			{Type: Compound, Comp: map[string]Tag{
				"id":   {Type: String, Str: "minecraft:pig"},
				"data": {Type: Compound, Comp: map[string]Tag{"Health": {Type: Short, Num: 8}}},
			}},
			{Type: Compound, Comp: map[string]Tag{
				"id": {Type: String, Str: "minecraft:cow"},
			}},
		}},
		"nestedlists": {Type: List, List: []Tag{
			{Type: List, List: []Tag{{Type: Int, Num: 1}, {Type: Int, Num: 2}}},
		}},
		"emptylist": {Type: List},
	}}

	var buf bytes.Buffer
	if err := Write(&buf, "root", src); err != nil {
		t.Fatal(err)
	}
	encoded := buf.Bytes()

	name, got, err := Read(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "root" {
		t.Errorf("name = %q, want root", name)
	}
	if v, _ := got.Get("count"); v.Int() != -7 {
		t.Errorf("count = %d, want -7", v.Int())
	}
	if v, _ := got.Get("wide"); v.Int() != 1<<40 {
		t.Errorf("wide = %d, want %d", v.Int(), int64(1)<<40)
	}
	if v, _ := got.Get("ratio"); v.Flt != 0.5 {
		t.Errorf("ratio = %v, want 0.5", v.Flt)
	}
	if v, _ := got.Get("raw"); len(v.Bytes) != 4 || v.Bytes[3] != 255 {
		t.Errorf("raw = %v", v.Bytes)
	}
	if v, _ := got.Get("data"); len(v.Longs) != 3 || v.Longs[1] != -1 {
		t.Errorf("data = %v", v.Longs)
	}
	if v, _ := got.Get("lines"); len(v.List) != 2 || v.List[1].Str != "" {
		t.Errorf("lines = %v", v.List)
	}
	if v, _ := got.Get("nested"); v.Comp["color"].Str != "black" {
		t.Errorf("nested = %v", v.Comp)
	}

	// Re-encoding must be byte-identical: the writer is deterministic, which
	// is what makes it safe to compare or cache output.
	var again bytes.Buffer
	if err := Write(&again, "root", got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again.Bytes()) {
		t.Error("re-encoding differs from the original")
	}
}

// TestWithout checks the block-entity path: x, y and z are dropped because the
// packet carries the position separately.
func TestWithout(t *testing.T) {
	comp := map[string]Tag{
		"id": {Type: String, Str: "minecraft:sign"},
		"x":  {Type: Int, Num: 1},
		"y":  {Type: Int, Num: 65},
		"z":  {Type: Int, Num: 10},
	}
	got := Without(comp, "x", "y", "z")
	if len(got) != 1 {
		t.Fatalf("kept %d fields, want 1", len(got))
	}
	if got["id"].Str != "minecraft:sign" {
		t.Errorf("id = %q", got["id"].Str)
	}
}
