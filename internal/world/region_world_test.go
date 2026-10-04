package world

import (
	"fmt"
	"testing"

	"mcgo/internal/proto/v776"
)

// TestRegionWorldServesSavedChunks drives the provider the way the server does:
// a chunk the world has comes back converted and passes the same strict decode
// as the direct converter, a chunk it lacks reports false so the caller can
// generate terrain, and the tops come from the chunk's own heightmap.
func TestRegionWorldServesSavedChunks(t *testing.T) {
	dir := worldDir(t)
	ctx := loadCtx(t)
	ctx.Biomes = biomesFromWorld(t, dir)
	rw := OpenRegionWorld(dir, ctx)

	cx, cz, raw := firstPopulatedChunk(t, dir)
	if raw == nil {
		t.Fatal("no populated chunk found")
	}

	body, ok := rw.Payload(cx, cz, nil, nil)
	if !ok {
		t.Fatalf("provider reports no chunk (%d,%d) that the region file has", cx, cz)
	}
	if err := compareChunk(body, raw, ctx); err != nil {
		t.Errorf("provider chunk (%d,%d): %v", cx, cz, err)
	}

	// Far outside anything generated: the caller must be able to fall back to
	// generated terrain rather than dropping the player into void.
	if _, ok := rw.Payload(cx+400, cz+400, nil, nil); ok {
		t.Error("provider claimed a chunk that does not exist")
	}

	tops, ok := rw.Tops(cx, cz)
	if !ok {
		t.Fatal("no tops for a chunk that has a heightmap")
	}
	// Cross-check against the terrain itself: a column's light top cannot sit
	// above the topmost block in the converted chunk. A bad heightmap decode
	// (wrong bit offset) would push it far higher.
	topmost := map[int]int32{}
	for s := range ctx.Sections {
		blocks, err := decodeSection(body, s, ctx)
		if err != nil {
			t.Fatal(err)
		}
		base := int32(ctx.MinY + s*16)
		for i, id := range blocks {
			if id == 0 {
				continue
			}
			y := base + int32(i>>8)
			if y > topmost[i&0xFF] {
				topmost[i&0xFF] = y
			}
		}
	}
	flatTops := 0
	for col := range tops {
		if tops[col] == 64 {
			flatTops++
		}
		hi, present := topmost[col]
		if !present {
			continue // all air: lit from the bottom
		}
		if tops[col] > hi {
			t.Errorf("column %d: top %d above the topmost block %d", col, tops[col], hi)
		}
	}
	if flatTops == 256 {
		t.Error("every column tops out at the flat builder's surface: the heightmap was not used")
	}
}

// TestRegionWorldAppliesEdits checks a recorded block change reaches the wire at
// the index the packed data uses.
func TestRegionWorldAppliesEdits(t *testing.T) {
	dir := worldDir(t)
	ctx := loadCtx(t)
	ctx.Biomes = biomesFromWorld(t, dir)
	rw := OpenRegionWorld(dir, ctx)
	cx, cz, _ := firstPopulatedChunk(t, dir)

	// Edit one block in the bottom section, at local (1,2,3).
	const lx, ly, lz = 1, 2, 3
	idx := (ly << 8) | (lz << 4) | lx
	pos := [3]int32{cx*16 + lx, int32(ctx.MinY + ly), cz*16 + lz}

	plain, ok := rw.Payload(cx, cz, nil, nil)
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	base, err := decodeSection(plain, 0, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if base[idx] == v776.BlockStone {
		t.Skip("that block is already stone, so the edit would prove nothing")
	}

	edited, ok := rw.Payload(cx, cz, map[[3]int32]int32{pos: v776.BlockStone}, nil)
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	got, err := decodeSection(edited, 0, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[idx] != v776.BlockStone {
		t.Errorf("edited block at index %d is %d, want %d", idx, got[idx], v776.BlockStone)
	}
	// Neighbouring entries must be untouched: a wrong index would corrupt the
	// column while still passing the check above.
	if idx > 0 && got[idx-1] != base[idx-1] {
		t.Errorf("neighbour at %d changed from %d to %d", idx-1, base[idx-1], got[idx-1])
	}
}

// TestRegionWorldCarriesServerSigns covers the gap that made a converted chunk
// look right while losing player work: a sign placed in the world lives in this
// server's own store, not in the region file, so it has to be sent alongside
// the file's block entities.
func TestRegionWorldCarriesServerSigns(t *testing.T) {
	dir := worldDir(t)
	ctx := loadCtx(t)
	ctx.Biomes = biomesFromWorld(t, dir)
	rw := OpenRegionWorld(dir, ctx)
	cx, cz, _ := firstPopulatedChunk(t, dir)

	plain, ok := rw.Payload(cx, cz, nil, nil)
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	fileBEs, err := wireBlockEntityCount(plain, ctx)
	if err != nil {
		t.Fatal(err)
	}

	sign := ExtraBE{
		X: cx*16 + 4, Y: 70, Z: cz*16 + 5,
		Type: v776.SignBlockEntityTypeID,
		NBT:  SignNBT(SignText{}),
	}
	body, ok := rw.Payload(cx, cz, nil, []ExtraBE{sign})
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	got, err := wireBlockEntityCount(body, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != fileBEs+1 {
		t.Errorf("chunk carries %d block entities, want %d", got, fileBEs+1)
	}

	// A sign in another chunk must not be attached to this one.
	body, ok = rw.Payload(cx, cz, nil, []ExtraBE{{X: sign.X + 32, Y: 70, Z: sign.Z, NBT: sign.NBT}})
	if !ok {
		t.Fatal("provider has no chunk here")
	}
	if got, err = wireBlockEntityCount(body, ctx); err != nil {
		t.Fatal(err)
	}
	if got != fileBEs {
		t.Errorf("a sign from another chunk leaked in: %d block entities, want %d", got, fileBEs)
	}
}

// firstPopulatedChunk returns a chunk with terrain, since an untouched chunk is
// all air and would exercise nothing.
func firstPopulatedChunk(t *testing.T, dir string) (int32, int32, []byte) {
	t.Helper()
	for _, rc := range [][2]int32{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}} {
		reg, err := OpenRegion(RegionPath(dir, rc[0]*32, rc[1]*32))
		if err != nil {
			continue
		}
		for cx := rc[0] * 32; cx < rc[0]*32+32; cx++ {
			for cz := rc[1] * 32; cz < rc[1]*32+32; cz++ {
				raw, ok, err := reg.Chunk(cx, cz)
				if err != nil {
					reg.Close()
					t.Fatal(err)
				}
				if ok && richness(raw) > 4 {
					reg.Close()
					return cx, cz, raw
				}
			}
		}
		reg.Close()
	}
	return 0, 0, nil
}

// biomesFromWorld collects the biome names a world uses and assigns them ids in
// sorted order. The ids only need to be self-consistent here: the ids a client
// sees come from the registry data the server sends.
func biomesFromWorld(t *testing.T, dir string) map[string]int32 {
	t.Helper()
	var cands []chunkCand
	for _, rc := range [][2]int32{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}} {
		reg, err := OpenRegion(RegionPath(dir, rc[0]*32, rc[1]*32))
		if err != nil {
			continue
		}
		for cx := rc[0] * 32; cx < rc[0]*32+32; cx++ {
			for cz := rc[1] * 32; cz < rc[1]*32+32; cz++ {
				if raw, ok, err := reg.Chunk(cx, cz); err == nil && ok {
					cands = append(cands, chunkCand{cx, cz, raw, 0})
				}
			}
		}
		reg.Close()
	}
	return biomeMap(cands)
}

// afterSections walks a converted body's chunk coordinates, heightmaps and
// section buffer, leaving the reader at the block-entity count.
func afterSections(body []byte, ctx ChunkContext) (*byteReader, error) {
	r := &byteReader{b: body}
	if err := r.skip(8); err != nil { // chunk x and z
		return nil, err
	}
	hmCount, err := r.varint()
	if err != nil {
		return nil, err
	}
	for range hmCount {
		if _, err := r.varint(); err != nil {
			return nil, err
		}
		longs, err := r.varint()
		if err != nil {
			return nil, err
		}
		if err := r.skip(int(longs) * 8); err != nil {
			return nil, err
		}
	}
	cdLen, err := r.varint()
	if err != nil {
		return nil, err
	}
	if cdLen <= 0 || r.remaining() < int(cdLen) {
		return nil, fmt.Errorf("section buffer length %d with %d bytes left", cdLen, r.remaining())
	}
	sec := &byteReader{b: body[r.i : r.i+int(cdLen)]}
	r.i += int(cdLen)
	r.sec = sec
	return r, nil
}

// wireBlockEntityCount reports how many block entities the body carries.
func wireBlockEntityCount(body []byte, ctx ChunkContext) (int32, error) {
	r, err := afterSections(body, ctx)
	if err != nil {
		return 0, err
	}
	return r.varint()
}

// decodeSection decodes one section's block states from a converted body.
func decodeSection(body []byte, want int, ctx ChunkContext) ([]int32, error) {
	r, err := afterSections(body, ctx)
	if err != nil {
		return nil, err
	}
	sec := r.sec
	for i := range ctx.Sections {
		if _, err := sec.u16(); err != nil {
			return nil, err
		}
		if _, err := sec.u16(); err != nil {
			return nil, err
		}
		blocks, err := sec.paletted(4096, 8)
		if err != nil {
			return nil, fmt.Errorf("section %d: %w", i, err)
		}
		if _, err := sec.paletted(64, 3); err != nil {
			return nil, fmt.Errorf("section %d biomes: %w", i, err)
		}
		if i == want {
			return blocks, nil
		}
	}
	return nil, fmt.Errorf("section %d not in the buffer", want)
}
