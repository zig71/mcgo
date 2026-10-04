package server

import (
	"os"
	"testing"

	"mcgo/internal/proto/v776"
)

// TestRegionWorldWiring checks the server builds a usable saved-world provider
// from a protocol version's own tables.
//
// This is the failure that looks like success: if the block-state blob or the
// biome registry disagree with what the converter expects, every conversion
// fails, Payload reports false, and the server quietly serves flat terrain
// instead of the saved world.
func TestRegionWorldWiring(t *testing.T) {
	dir := os.Getenv("MCGO_WORLD")
	if dir == "" {
		t.Skip("set MCGO_WORLD to a world directory containing region/")
	}
	s := &Server{cfg: Config{RegionDir: dir}}
	rw := s.regionWorld(v776.Tables)
	if rw == nil {
		t.Fatal("regionWorld returned nil for a configured directory")
	}
	for cx := int32(0); cx < 8; cx++ {
		for cz := int32(0); cz < 8; cz++ {
			body, ok := rw.Payload(cx, cz, nil, nil)
			if !ok {
				continue
			}
			if len(body) == 0 {
				t.Fatalf("chunk (%d,%d) converted to nothing", cx, cz)
			}
			t.Logf("chunk (%d,%d) served through the server's wiring: %d bytes", cx, cz, len(body))
			return
		}
	}
	t.Fatal("no chunk converted: the table or biome wiring is wrong")
}

// TestRegionWorldOffByDefault checks the flat world stays the default: an empty
// directory must not open anything.
func TestRegionWorldOffByDefault(t *testing.T) {
	s := &Server{cfg: DefaultConfig()}
	if rw := s.regionWorld(v776.Tables); rw != nil {
		t.Error("a saved world was opened without one being configured")
	}
}
