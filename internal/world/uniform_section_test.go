package world

import (
	"testing"

	"mcgo/internal/proto/v776"
)

// A uniform section is one the saved chunk holds a single block in: all air
// above the terrain, all stone below it, and most of both. The converter used to
// return early for those and drop every edit in them, which is why a block that
// was broken came back on the next chunk resend while a block that was placed
// did not — placing usually lands in a section that is not uniform.
func TestRegionWorldEditsUniformSections(t *testing.T) {
	dir := worldDir(t)
	ctx := loadCtx(t)
	ctx.Biomes = biomesFromWorld(t, dir)
	rw := OpenRegionWorld(dir, ctx)
	cx, cz, _ := firstPopulatedChunk(t, dir)

	plain, ok := rw.Payload(cx, cz, nil, nil)
	if !ok {
		t.Fatal("provider has no chunk here")
	}

	// Find a section whose saved blocks are all the same.
	section := -1
	var uniformState int32
	for s := range ctx.Sections {
		vals, err := decodeSection(plain, s, ctx)
		if err != nil {
			continue
		}
		same := true
		for _, v := range vals[1:] {
			if v != vals[0] {
				same = false
				break
			}
		}
		if same {
			section, uniformState = s, vals[0]
			break
		}
	}
	if section < 0 {
		t.Skip("this chunk has no uniform section")
	}

	const lx, ly, lz = 1, 2, 3
	idx := (ly << 8) | (lz << 4) | lx
	pos := [3]int32{cx*16 + lx, int32(ctx.MinY + section*16 + ly), cz*16 + lz}
	want := int32(v776.BlockDirt)
	if uniformState == want {
		want = int32(v776.BlockStone)
	}

	edited, ok := rw.Payload(cx, cz, map[[3]int32]int32{pos: want}, nil)
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	got, err := decodeSection(edited, section, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[idx] != want {
		t.Errorf("an edit in a uniform section reads back as %d, want %d", got[idx], want)
	}
}
