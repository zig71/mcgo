package world

import (
	"sort"
	"strings"

	"mcgo/internal/mcproto"
)

// FlatConfig carries the version-specific IDs the flat builder needs. Keeping
// them here (rather than importing a protocol package) keeps world
// version-agnostic.
type FlatConfig struct {
	Air, Bedrock, Dirt, Grass, Stone int32
	Biome                            int32
	MinY, Height, SurfaceY           int
	SignBlockEntityType              int32 // minecraft:sign in block_entity_type
	// BlockEntityState maps a block state that has a block entity to that
	// entity's registry id and name, so a streamed chunk can list one for every
	// such block. A block entity the client never hears about is a block it
	// draws nothing for: an invisible chest.
	BlockEntityState map[int32]BlockEntityType
	// Emission is the hand-written fallback light table, and LightOpaque the
	// states that stop light. The generated LightEmission and LightDampening
	// tables below take precedence; these two are what a version with no dump
	// falls back to.
	Emission    map[int32]int32
	LightOpaque map[int32]bool
	// LightDampening is the generated per-state table: one byte per state id, the
	// cost of a light step through it — BlockBehaviour.getLightDampening, with
	// LightEngine's floor of one applied by the engine. Empty when a version has
	// no table, and then opacity and the fluid rules decide.
	LightDampening []byte
	// LightEmission is the generated per-state emission table. The hand-written
	// Emission map above stays as the fallback for a version with no dump.
	LightEmission []byte
}

// BlockEntityType names a block entity: the registry id a chunk lists, and the
// name its NBT carries.
type BlockEntityType struct {
	ID   int32
	Name string
}

// Flat holds the canonical superflat chunk split into parts, so a single
// section can be re-encoded when a block changes without rebuilding the rest.
type Flat struct {
	cfg      FlatConfig
	head     []byte   // heightmaps
	sections [][]byte // encoded sections, bottom to top
	light    []byte   // light data for an unmodified chunk
}

func NewFlat(cfg FlatConfig) *Flat {
	f := &Flat{cfg: cfg}
	sections := cfg.Height / 16

	var head mcproto.Writer
	head.WriteVarInt(2) // world_surface, motion_blocking
	for _, t := range []int32{1, 4} {
		head.WriteVarInt(t)
		writeHeightmap(&head, cfg)
	}
	f.head = head.Bytes()

	f.sections = make([][]byte, sections)
	for s := range sections {
		var w mcproto.Writer
		encodeSection(&w, cfg, baseBlocks(cfg, s))
		f.sections[s] = w.Bytes()
	}

	tops := baseTops(cfg)
	var light mcproto.Writer
	// The base world has no light sources in it, so the block-light half of
	// this is empty; every real chunk gets its own light written.
	writeLight(&light, cfg, tops, allSections(sections), 0, 0, nil)
	f.light = light.Bytes()
	return f
}

// World is the mutable view over the flat terrain: the canonical chunk plus a
// sparse map of block changes, so memory grows with edits rather than with the
// volume of the world.
type World struct {
	flat    *Flat
	e       *Edits
	signs   *Signs
	region  *RegionWorld
	light   *BlockLight
	banners *Banners
}

func NewWorld(cfg FlatConfig, e *Edits, signs *Signs, banners *Banners) *World {
	return &World{flat: NewFlat(cfg), e: e, signs: signs, banners: banners, light: NewBlockLight()}
}

// Light is the block light this world holds.
func (w *World) Light() *BlockLight { return w.light }

// Banners is the pattern layers of the banners placed in this world.
func (w *World) Banners() *Banners { return w.banners }

// Emission reports the light a block gives off, implementing LightEnv. The
// table is the game's own, per state: a lit campfire emits 15 and an unlit one
// none, which a table keyed by block cannot say.
func (w *World) Emission(x, y, z int32) byte {
	state := w.Block(x, y, z)
	if n := int(state); n >= 0 && n < len(w.flat.cfg.LightEmission) {
		return w.flat.cfg.LightEmission[n]
	}
	if w.flat.cfg.Emission == nil {
		return 0
	}
	return byte(w.flat.cfg.Emission[state])
}

// Dampening reports what a step through a block costs, implementing LightEnv.
// A state that stops light costs the maximum, which is how a solid block makes
// light stop; anything else costs what the table says, or nothing.
func (w *World) Dampening(x, y, z int32) byte {
	state := w.Block(x, y, z)
	// The generated table is the more precise answer, so it wins: water is in
	// the opaque table as well (it does dim light), and reading that first made
	// this report 15 for water.
	//
	// 0xff is this table's "no data": a state the table could not be filled in
	// for — 1.21.8's is projected from 26.3 by block and properties, and 183 of
	// its blocks have no counterpart there. Those keep the rules below rather
	// than a guess.
	if n := int(state); n >= 0 && n < len(w.flat.cfg.LightDampening) {
		if d := w.flat.cfg.LightDampening[n]; d != lightNoData {
			return d
		}
	}
	if w.Opaque(x, y, z) {
		return 15
	}
	return 0
}

// lightNoData is what a projected dampening table writes for a state it could
// not fill in. No dampening can be 255, so it cannot collide with a real answer.
const lightNoData = 0xFF

// Opaque reports whether a block stops light, implementing LightEnv. Anything
// the table does not name is treated as opaque, since most blocks are.
func (w *World) Opaque(x, y, z int32) bool {
	state := w.Block(x, y, z)
	if w.flat.cfg.LightOpaque == nil {
		return state != w.flat.cfg.Air
	}
	if opaque, ok := w.flat.cfg.LightOpaque[state]; ok {
		return opaque
	}
	return state != w.flat.cfg.Air
}

// RelightBlock re-lights around a block that has just changed. A block that
// emits light lights its surroundings; one that blocked light may have been
// shadowing them.
func (w *World) RelightBlock(x, y, z int32) {
	w.light.SetEmission(w, x, y, z)
}

// UseRegion makes this world serve a saved world's chunks wherever it has one,
// falling back to the flat builder elsewhere — including for every chunk, when
// rw is nil.
func (w *World) UseRegion(rw *RegionWorld) { w.region = rw }

// Sections reports how many sections a chunk column has.
func (w *World) Sections() int { return len(w.flat.sections) }

// SetBlock records a block change and reports the chunk and the sections whose
// sky light may have changed (nil when the column height is unaffected).
func (w *World) SetBlock(x, y, z, id int32) (int32, int32, []int) {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()

	before := w.columnTopLocked(x, z)
	w.e.m[[3]int32{x, y, z}] = id
	w.e.dirty = true
	after := w.columnTopLocked(x, z)
	if before == after {
		return x >> 4, z >> 4, nil
	}
	lo := w.sectionOf(min(before, after))
	hi := w.sectionOf(max(before, after))
	secs := make([]int, 0, hi-lo+1)
	for s := lo; s <= hi; s++ {
		secs = append(secs, s)
	}
	return x >> 4, z >> 4, secs
}

// Block returns the current block state at a position, edits included.
func (w *World) Block(x, y, z int32) int32 {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	return w.blockLocked(x, y, z)
}

func (w *World) sectionOf(y int32) int {
	s := (int(y) - w.flat.cfg.MinY) / 16
	if s < 0 {
		return 0
	}
	if s >= len(w.flat.sections) {
		return len(w.flat.sections) - 1
	}
	return s
}

// chunkBlockEntities lists the block entities for a chunk: signs, which carry
// text, and any other block entity a player has placed. Every one of them has
// to be sent with the chunk, or the client renders nothing there.
func (w *World) chunkBlockEntities(cx, cz int32) []ExtraBE {
	cfg := w.flat.cfg
	var out []ExtraBE
	for pos, text := range w.signs.InChunk(cx, cz) {
		// Only when a sign is still standing there: an entry that outlived its
		// block would be sent with the chunk, and the client would be told about
		// a block entity at a position that holds something else.
		if !w.isSignLocked(pos[0], pos[1], pos[2]) {
			continue
		}
		out = append(out, ExtraBE{
			X: pos[0], Y: pos[1], Z: pos[2],
			Type: cfg.SignBlockEntityType,
			NBT:  SignNBT(text),
		})
	}
	for p, id := range w.e.m {
		if p[0]>>4 != cx || p[2]>>4 != cz {
			continue
		}
		bet, ok := cfg.BlockEntityState[id]
		if !ok || bet.ID == cfg.SignBlockEntityType {
			continue // signs went out above, with their text
		}
		if strings.HasSuffix(bet.Name, "banner") {
			// A banner carries its pattern layers, which came from the item
			// that placed it; with none it is just its id, as vanilla leaves it.
			if patterns, ok := w.banners.Get(p[0], p[1], p[2]); ok {
				out = append(out, ExtraBE{X: p[0], Y: p[1], Z: p[2], Type: bet.ID, NBT: BannerNBT(patterns)})
				continue
			}
		}
		out = append(out, ExtraBE{X: p[0], Y: p[1], Z: p[2], Type: bet.ID, NBT: minimalBlockEntity(bet.Name)})
	}
	return out
}

// EditsInChunk reports how many recorded block changes fall inside a chunk,
// which is what the debug log uses to say whether a chunk being sent carries
// player work.
func (w *World) EditsInChunk(cx, cz int32) int {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()
	n := 0
	for p := range w.e.m {
		if p[0]>>4 == cx && p[2]>>4 == cz {
			n++
		}
	}
	return n
}

// isSignLocked reports whether the block at a position is a sign, which is the
// state whose block-entity type the sign store is for. The caller holds the
// edits lock.
func (w *World) isSignLocked(x, y, z int32) bool {
	bet, ok := w.flat.cfg.BlockEntityState[w.blockLocked(x, y, z)]
	if !ok {
		return false
	}
	// The block entity type is minecraft:sign for a standing sign and
	// minecraft:hanging_sign for a hanging one, so both spellings count.
	return bet.Name == "minecraft:sign" || strings.HasSuffix(bet.Name, "_sign")
}

// minimalBlockEntity is the NBT a block entity needs in order to exist: its id.
// A chest and the rest carry nothing else worth synthesising, and the packet
// gives the position.
func minimalBlockEntity(name string) []byte {
	var w mcproto.Writer
	w.WriteByte(0x0A) // TAG_Compound
	writeNBTString(&w, "id", name)
	w.WriteByte(0x00) // TAG_End
	return w.Bytes()
}

// Payload returns the level_chunk_with_light packet body for chunk (cx, cz),
// re-encoding any section that has edits and recomputing light if it does.
//
// The light is written with the edits lock RELEASED, and that matters: the
// light engine reads block states through its environment callbacks, which take
// the edits lock, while relighting holds the light lock. Doing the light here,
// with the edits lock held, is the other half of a deadlock with RelightBlock —
// one encoders and one relighter is enough to stop a server dead.
func (w *World) Payload(cx, cz int32) []byte {
	body, tops, dirty, region := w.payloadLocked(cx, cz)
	if region {
		return body
	}

	var out mcproto.Writer
	out.WriteBytes(body)
	if dirty {
		writeLight(&out, w.flat.cfg, tops, allSections(len(w.flat.sections)), cx, cz, w.light)
	} else {
		out.WriteBytes(w.flat.light)
	}
	return out.Bytes()
}

// payloadLocked builds everything but the light for one chunk, reporting the
// column tops the light needs and whether the light has to be recomputed at
// all. It is the part that has to run under the edits lock.
func (w *World) payloadLocked(cx, cz int32) (body []byte, tops [256]int32, dirty bool, region bool) {
	w.e.mu.Lock()
	defer w.e.mu.Unlock()

	if w.region != nil {
		// What players have placed lives in this server's own store, so the
		// region file cannot know about it.
		if b, ok := w.region.Payload(cx, cz, w.e.m, w.chunkBlockEntities(cx, cz)); ok {
			return b, [256]int32{}, false, true
		}
	}

	f := w.flat
	var out mcproto.Writer
	out.WriteInt(cx)
	out.WriteInt(cz)
	out.WriteBytes(f.head)

	var cd mcproto.Writer
	for s := range f.sections {
		if sec := w.reencode(cx, cz, s); sec != nil {
			cd.WriteBytes(sec)
			dirty = true
		} else {
			cd.WriteBytes(f.sections[s])
		}
	}
	out.WriteVarInt(int32(cd.Len()))
	out.WriteBytes(cd.Bytes())
	// Block entities. Signs carry text the palette cannot express, and every
	// other block entity has to be listed too: the client draws nothing at a
	// position whose block entity it never heard about.
	bes := w.chunkBlockEntities(cx, cz)
	out.WriteVarInt(int32(len(bes)))
	for _, e := range bes {
		out.WriteByte(byte((e.X&15)<<4 | (e.Z & 15))) // packed XZ
		out.WriteUShort(uint16(e.Y))                  // Y
		out.WriteVarInt(e.Type)
		// The NBT field is vanilla's optional tag: a single tag-type byte where
		// 0 means "absent", then the payload with no name. There is NO boolean
		// present flag — writing one makes the client read TAG_Byte and then a
		// one-byte payload.
		out.WriteBytes(e.NBT)
	}
	return out.Bytes(), w.chunkTopsLocked(cx, cz), dirty, false
}

// LightUpdate returns a light_update packet body for the given sections. Like
// Payload, the light is written after the edits lock is released.
func (w *World) LightUpdate(cx, cz int32, secs []int) []byte {
	w.e.mu.Lock()
	tops := w.chunkTopsLocked(cx, cz)
	w.e.mu.Unlock()

	var out mcproto.Writer
	out.WriteVarInt(cx)
	out.WriteVarInt(cz)
	writeLight(&out, w.flat.cfg, tops, secs, cx, cz, w.light)
	return out.Bytes()
}

// reencode rebuilds one section if any recorded edit falls inside it, else nil.
func (w *World) reencode(cx, cz int32, s int) []byte {
	base := w.flat.cfg.MinY + s*16
	var hits [][4]int32
	for p, id := range w.e.m {
		if p[0]>>4 != cx || p[2]>>4 != cz {
			continue
		}
		if int(p[1]) < base || int(p[1]) >= base+16 {
			continue
		}
		hits = append(hits, [4]int32{p[0], p[1], p[2], id})
	}
	if len(hits) == 0 {
		return nil
	}
	blocks := baseBlocks(w.flat.cfg, s)
	for _, h := range hits {
		lx := int(h[0] & 15)
		ly := int(h[1]) - base
		lz := int(h[2] & 15)
		blocks[ly*256+lz*16+lx] = h[3]
	}
	var wr mcproto.Writer
	encodeSection(&wr, w.flat.cfg, blocks)
	return wr.Bytes()
}

// ---- light ----

// chunkTopsLocked returns the highest opaque block per column of a chunk.
func (w *World) chunkTopsLocked(cx, cz int32) [256]int32 {
	if w.region != nil {
		if tops, ok := w.region.Tops(cx, cz); ok {
			return tops
		}
	}
	var tops [256]int32
	for x := range 16 {
		for z := range 16 {
			tops[x*16+z] = w.columnTopLocked(cx*16+int32(x), cz*16+int32(z))
		}
	}
	return tops
}

func (w *World) columnTopLocked(x, z int32) int32 {
	cfg := w.flat.cfg
	top := int32(-1)
	for y := int32(cfg.SurfaceY - 3); y <= int32(cfg.SurfaceY); y++ {
		if w.blockLocked(x, y, z) != cfg.Air {
			top = y
		}
	}
	for p, id := range w.e.m {
		if p[0] == x && p[2] == z && id != cfg.Air && p[1] > top {
			top = p[1]
		}
	}
	return top
}

func (w *World) blockLocked(x, y, z int32) int32 {
	if id, ok := w.e.m[[3]int32{x, y, z}]; ok {
		return id
	}
	// This has to mirror baseBlocks exactly: a lookup that disagrees with the
	// chunks the server sends means the server and its clients see different
	// worlds, which is how a dug hole appears to be void.
	cfg := w.flat.cfg
	switch {
	case y > int32(cfg.SurfaceY) || y < int32(cfg.MinY):
		return cfg.Air
	case y == int32(cfg.SurfaceY):
		return cfg.Grass
	case y >= int32(cfg.SurfaceY-3):
		return cfg.Dirt
	case y == int32(cfg.MinY):
		return cfg.Bedrock
	default:
		return cfg.Stone
	}
}

func baseTops(cfg FlatConfig) [256]int32 {
	var tops [256]int32
	for i := range tops {
		tops[i] = int32(cfg.SurfaceY)
	}
	return tops
}

func allSections(n int) []int {
	secs := make([]int, n)
	for i := range secs {
		secs[i] = i
	}
	return secs
}

// writeLight emits the four masks and the sky-light arrays for the given
// sections. Masks cover (sections + 2) entries: bit 0 is the section below the
// world, bit i+1 is section i. Sections with no light are declared empty, which
// keeps the common case cheap.
// writeLight writes the sky and block light for a set of sections.
//
// A section with nothing in it is named in the *empty* mask instead of being
// written, which is why the two masks above the arrays have to agree with the
// arrays that follow: a mismatch makes the client read light that is not there.
func writeLight(out *mcproto.Writer, cfg FlatConfig, tops [256]int32, secs []int, cx, cz int32, blocks *BlockLight) {
	var skyLit, skyEmpty, blockLit, blockEmpty uint64
	var skyArrays, blockArrays [][]byte
	for _, s := range secs {
		arr := skyLightArray(cfg, tops, s)
		if allZero(arr) {
			skyEmpty |= 1 << uint(s+1)
		} else {
			skyLit |= 1 << uint(s+1)
			skyArrays = append(skyArrays, arr)
		}
		var blockArr []byte
		if blocks != nil {
			blockArr = blocks.Section(cx, cz, s, cfg.MinY)
		}
		if allZero(blockArr) {
			blockEmpty |= 1 << uint(s+1)
		} else {
			blockLit |= 1 << uint(s+1)
			blockArrays = append(blockArrays, blockArr)
		}
	}
	writeBitSet(out, skyLit)
	writeBitSet(out, blockLit)
	writeBitSet(out, skyEmpty)
	writeBitSet(out, blockEmpty)
	out.WriteVarInt(int32(len(skyArrays)))
	for _, a := range skyArrays {
		out.WriteVarInt(2048)
		out.WriteBytes(a)
	}
	out.WriteVarInt(int32(len(blockArrays)))
	for _, a := range blockArrays {
		out.WriteVarInt(2048)
		out.WriteBytes(a)
	}
}

// skyLightArray packs one section's sky light: 15 above the column top, 0 at or
// below it. Layout matches vanilla's DataLayer: index (y<<8)|(z<<4)|x, low
// nibble first.
func skyLightArray(cfg FlatConfig, tops [256]int32, s int) []byte {
	base := int32(cfg.MinY + s*16)
	arr := make([]byte, 2048)
	for x := range 16 {
		for z := range 16 {
			top := tops[x*16+z]
			for y := range 16 {
				// Everything at or above the topmost block is lit. Vanilla
				// decides this from each block's light-blocking shape; without
				// that table, lighting the top block as well is what keeps a
				// sign, banner or anvil from rendering black, and it never
				// makes a block darker than vanilla.
				if base+int32(y) < top {
					continue
				}
				i := (y << 8) | (z << 4) | x
				if i&1 == 0 {
					arr[i>>1] |= 15
				} else {
					arr[i>>1] |= 15 << 4
				}
			}
		}
	}
	return arr
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// ---- encoding ----

func baseBlocks(cfg FlatConfig, s int) []int32 {
	base := cfg.MinY + s*16
	blocks := make([]int32, 4096)
	for i := range blocks {
		blocks[i] = cfg.Air
	}
	put := func(y int, id int32) {
		if y < base || y >= base+16 {
			return
		}
		ly := y - base
		for z := range 16 {
			for x := range 16 {
				blocks[ly*256+z*16+x] = id
			}
		}
	}
	// A deep world: bedrock at the bottom, stone through the middle, dirt under
	// the grass. The shallow slab this used to be meant that a hole — dug, or
	// blown by TNT — fell straight through into the void.
	put(cfg.SurfaceY, cfg.Grass)
	put(cfg.SurfaceY-1, cfg.Dirt)
	put(cfg.SurfaceY-2, cfg.Dirt)
	put(cfg.SurfaceY-3, cfg.Dirt)
	for y := cfg.MinY + 1; y < cfg.SurfaceY-3; y++ {
		put(y, cfg.Stone)
	}
	put(cfg.MinY, cfg.Bedrock)
	return blocks
}

func encodeSection(w *mcproto.Writer, cfg FlatConfig, blocks []int32) {
	nonEmpty := 0
	for _, b := range blocks {
		if b != cfg.Air {
			nonEmpty++
		}
	}
	w.WriteUShort(uint16(nonEmpty))
	w.WriteUShort(0) // fluid count
	if nonEmpty == 0 {
		w.WriteByte(0) // bitsPerEntry 0 = single value
		w.WriteVarInt(cfg.Air)
	} else {
		writePaletted(w, blocks, 4)
	}
	w.WriteByte(0) // biome container: single value
	w.WriteVarInt(cfg.Biome)
}

// writeBitSet writes a BitSet the way vanilla does: a VarInt byte length
// followed by the minimal little-endian byte representation, matching
// BitSet.toByteArray()/BitSet.valueOf().
func writeBitSet(w *mcproto.Writer, mask uint64) {
	n := 0
	for i := 63; i >= 0; i-- {
		if mask&(1<<uint(i)) != 0 {
			n = i/8 + 1
			break
		}
	}
	w.WriteVarInt(int32(n))
	for i := range n {
		w.WriteByte(byte(mask >> (8 * uint(i))))
	}
}

// writeHeightmap emits a 9-bit-per-column heightmap packed 7 values per long.
func writeHeightmap(w *mcproto.Writer, cfg FlatConfig) {
	const cols = 256
	val := uint64(cfg.SurfaceY - cfg.MinY + 1)
	longs := make([]int64, (cols+6)/7)
	for i := range cols {
		longs[i/7] |= int64(val << (uint(i%7) * 9))
	}
	w.WriteVarInt(int32(len(longs)))
	for _, l := range longs {
		w.WriteLong(l)
	}
}

// writePaletted emits a paletted container, choosing the width from the
// palette size: 4-8 bits indirect, or 15 bits direct once a section holds more
// distinct states than an indirect palette can index. A fixed 4 bits silently
// truncates indices past 15, so a section with 17 block types renders wrong.
func writePaletted(w *mcproto.Writer, values []int32, minBits int) {
	seen := make(map[int32]bool, 8)
	var palette []int32
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			palette = append(palette, v)
		}
	}
	sort.Slice(palette, func(i, j int) bool { return palette[i] < palette[j] })

	bits := minBits
	for (1 << uint(bits)) < len(palette) {
		bits++
	}
	direct := bits > 8 // beyond an 8-bit palette, switch to global ids
	if direct {
		bits = 15
	}

	index := make(map[int32]uint64, len(palette))
	for i, v := range palette {
		index[v] = uint64(i)
	}

	w.WriteByte(byte(bits))
	if !direct {
		w.WriteVarInt(int32(len(palette)))
		for _, v := range palette {
			w.WriteVarInt(v)
		}
	}

	// SimpleBitStorage holds valuesPerLong = 64/bits values per long, rounding
	// down: a 5-bit width fits 12 values and wastes 4 bits rather than letting
	// an entry straddle the boundary. The long count is therefore
	// ceil(entries / valuesPerLong) — 342, not 320, for 4096 five-bit entries.
	perLong := 64 / bits
	longs := make([]int64, (len(values)+perLong-1)/perLong)
	for i, v := range values {
		idx := uint64(v) // direct entries are global block state ids
		if !direct {
			idx = index[v]
		}
		longs[i/perLong] |= int64(idx << (uint(i%perLong) * uint(bits)))
	}
	// As of 1.21.5 the data array is NOT length-prefixed: the client derives
	// the long count from bits-per-entry and the entry count.
	for _, l := range longs {
		w.WriteLong(l)
	}
}

// MinY is the lowest block the world holds, which light section indexes need.
func (w *World) MinY() int { return w.flat.cfg.MinY }
