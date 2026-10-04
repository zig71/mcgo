package world

import (
	"bytes"
	"fmt"
	"sync"

	"mcgo/internal/nbt"
)

// RegionWorld serves chunks out of a saved world's region files, with the live
// block edits overlaid. A chunk the world has not generated reports false so
// the caller can fall back to generated terrain: most of a real world is
// ungenerated, and serving those as void would drop a player through the floor.
//
// Known gap: block entities placed by players (signs, chests) live in this
// server's own stores, not in the region files, so they do not appear in
// converted chunks yet. Block edits do.
type RegionWorld struct {
	dir  string
	ctx  ChunkContext
	mu   sync.Mutex
	regs map[[2]int32]*Region // nil value: no such region file
}

// OpenRegionWorld serves dir, which must be the directory containing region/.
func OpenRegionWorld(dir string, ctx ChunkContext) *RegionWorld {
	return &RegionWorld{dir: dir, ctx: ctx, regs: make(map[[2]int32]*Region, 8)}
}

// Payload converts one saved chunk into a level_chunk_with_light body. The
// second result is false when the world has no chunk there.
func (r *RegionWorld) Payload(cx, cz int32, edits map[[3]int32]int32, extra []ExtraBE) ([]byte, bool) {
	raw := r.raw(cx, cz)
	if raw == nil {
		return nil, false
	}
	ctx := r.ctx
	ctx.Edits = chunkEdits(cx, cz, ctx, edits)
	ctx.ExtraBEs = extra
	body, err := ConvertChunk(raw, cx, cz, ctx)
	if err != nil {
		return nil, false
	}
	return body, true
}

// Tops returns the highest solid block per column, read from the chunk's own
// heightmap. Light is recomputed from these after an edit, so a cave is not lit
// as though the ground were solid: the flat builder's constant surface is only
// correct for flat terrain.
func (r *RegionWorld) Tops(cx, cz int32) ([256]int32, bool) {
	var tops [256]int32
	raw := r.raw(cx, cz)
	if raw == nil {
		return tops, false
	}
	_, root, err := nbt.Read(bytes.NewReader(raw))
	if err != nil {
		return tops, false
	}
	hm, ok := root.Get("Heightmaps")
	if !ok {
		return tops, false
	}
	col, ok := hm.Get("MOTION_BLOCKING")
	if !ok || len(col.Longs) == 0 {
		return tops, false
	}
	// Seven 9-bit heights per long, each stored as (y - minY + 1).
	for i := range tops {
		li := i / 7
		if li >= len(col.Longs) {
			break
		}
		v := uint64(col.Longs[li]) >> (uint(i%7) * 9) & 0x1FF
		if v == 0 {
			tops[i] = int32(r.ctx.MinY) - 1 // all air: lit from the bottom up
			continue
		}
		tops[i] = int32(v) + int32(r.ctx.MinY) - 1
	}
	return tops, true
}

// raw returns the chunk's NBT, or nil if the region file or the chunk is
// absent. Region files stay open: one world uses a handful of them.
func (r *RegionWorld) raw(cx, cz int32) []byte {
	rc := [2]int32{cx >> 5, cz >> 5}
	r.mu.Lock()
	reg, cached := r.regs[rc]
	if !cached {
		if f, err := OpenRegion(RegionPath(r.dir, cx, cz)); err == nil {
			reg = f
		}
		r.regs[rc] = reg
	}
	r.mu.Unlock()

	if reg == nil {
		return nil
	}
	chunk, ok, err := reg.Chunk(cx, cz)
	if err != nil || !ok {
		return nil
	}
	return chunk
}

// chunkEdits turns the server's world-space edits into the chunk-local indices
// the packed data uses, or nil when this chunk has none.
func chunkEdits(cx, cz int32, ctx ChunkContext, edits map[[3]int32]int32) map[int]int32 {
	var out map[int]int32
	minY := int32(ctx.MinY)
	for p, id := range edits {
		if p[0]>>4 != cx || p[2]>>4 != cz {
			continue
		}
		ly := int(p[1] - minY)
		if ly < 0 || ly >= ctx.Sections*16 {
			continue
		}
		if out == nil {
			out = make(map[int]int32, 8)
		}
		out[(ly<<8)|(int(p[2]&15)<<4)|int(p[0]&15)] = id
	}
	return out
}

// Verify reports whether the chunk converts and how many block entities it
// carries, for logging at startup. A world the server cannot read should say so
// once, not per chunk.
func (r *RegionWorld) Verify(cx, cz int32) error {
	raw := r.raw(cx, cz)
	if raw == nil {
		return fmt.Errorf("no chunk (%d,%d) in %s", cx, cz, r.dir)
	}
	if _, ok := r.Tops(cx, cz); !ok {
		return fmt.Errorf("chunk (%d,%d) has no heightmap", cx, cz)
	}
	return nil
}
