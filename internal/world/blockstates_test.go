package world

import (
	"os"
	"testing"
)

// TestBlockStateLookup checks the generated table against values that are
// stable across versions: air is 0, stone 1, and grass_block 8.
func TestBlockStateLookup(t *testing.T) {
	blob, err := os.ReadFile("../proto/v777/blockstates.bin")
	if err != nil {
		t.Fatal(err)
	}
	table, err := LoadBlockStates(blob)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		props map[string]string
		want  int32
	}{
		{"minecraft:air", nil, 0},
		{"minecraft:stone", nil, 1},
		{"minecraft:grass_block", map[string]string{"snowy": "false"}, 9},
		// A partial palette entry: only the non-default property is named.
		{"minecraft:grass_block", nil, 9},
	}
	for _, c := range cases {
		got, ok := table.Lookup(c.name, c.props)
		if !ok {
			t.Errorf("Lookup(%s, %v) not found", c.name, c.props)
			continue
		}
		if got != c.want {
			t.Errorf("Lookup(%s, %v) = %d, want %d", c.name, c.props, got, c.want)
		}
	}

	if _, ok := table.Lookup("minecraft:not_a_block", nil); ok {
		t.Error("unknown block resolved")
	}
}
