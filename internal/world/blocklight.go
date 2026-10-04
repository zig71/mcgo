package world

import "sync"

// LightEnv is what the light engine needs to know about the world: what light a
// block emits, and whether it blocks light at all.
type LightEnv interface {
	// Dampening is how much light a block costs to pass through: 0 for air,
	// 1 for water and leaves, 15 for anything solid. Vanilla's
	// BlockState.getLightDampening, with LightEngine.getOpacity's floor of one
	// applied by the caller.
	Dampening(x, y, z int32) byte
	// Emission is the light a block emits, 0 for most blocks.
	Emission(x, y, z int32) byte
	// Opaque reports whether a block stops light. Light does not enter these,
	// and it is not emitted from inside them.
	Opaque(x, y, z int32) bool
}

// BlockLight is the light blocks emit, keyed by position.
//
// Only emitted light lives here; sky light is computed per chunk column. The
// map holds lit positions rather than every position, because a world with a
// few torches in it has very few of them.
type BlockLight struct {
	mu sync.Mutex
	m  map[[3]int32]byte
}

// MaxLight is the brightest a block can be. Light dims by one per block, so
// anything within MaxLight-1 of a change may have had its light altered.
const MaxLight = 15

func NewBlockLight() *BlockLight {
	return &BlockLight{m: make(map[[3]int32]byte)}
}

// Level is the light at a position, 0 for dark.
func (b *BlockLight) Level(x, y, z int32) byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.m[key3(x, y, z)]
}

// Count reports how many positions hold light, which is what tells a chunk
// whether it has any block light to send.
func (b *BlockLight) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.m)
}

// inChunk reports whether a position lies in a chunk.
func inChunk(x, z, cx, cz int32) bool {
	return x>>4 == cx && z>>4 == cz
}

// Section returns one section's block light as the 2048 nibble bytes the
// protocol wants, or nil when that section is dark.
func (b *BlockLight) Section(cx, cz int32, section int, minY int) []byte {
	baseY := int32(minY + section*16)
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []byte
	for pos, level := range b.m {
		if !inChunk(pos[0], pos[2], cx, cz) {
			continue
		}
		y := pos[1] - baseY
		if y < 0 || y > 15 {
			continue
		}
		if out == nil {
			out = make([]byte, 2048)
		}
		i := (y << 8) | ((pos[2] & 15) << 4) | (pos[0] & 15)
		if i&1 == 0 {
			out[i>>1] |= level & 0x0F
		} else {
			out[i>>1] |= (level & 0x0F) << 4
		}
	}
	return out
}

// lightDirs are the six directions, in the order the entry bitmask uses:
// +x, -x, +y, -y, +z, -z. DirIndex pairs 0 with 1, 2 with 3 and 4 with 5.
var lightDirs = [6][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

// allDirs is every direction, the mask a fresh entry carries.
const allDirs = 0x3F

func dirBit(i int) uint8    { return 1 << uint(i) }
func exceptDir(i int) uint8 { return allDirs &^ dirBit(i) }
func oppositeDir(i int) int { return i ^ 1 }

// lightEntry is vanilla's QueueEntry: a position, the level the entry carries,
// and which of the six directions it may still propagate in.
//
// A decrease carries the level the position HAD before the light was taken
// away; an increase carries the level the position should reach. Holding the
// directions is what keeps a removal from walking back into the cell it came
// from, and it is the part the first attempt at this port left out — a queue of
// (position, level) pairs, which re-lit the hole from the wrong side and left a
// gradient standing where the light had been.
type lightEntry struct {
	x, y, z int32
	level   byte
	dirs    uint8
	// emission marks an increase that sets the position's level itself, an
	// emitter coming up. It is the only increase that may raise a level
	// without a neighbour having fed it.
	emission bool
}

// SetEmission re-lights after the block at a position changed: vanilla's
// BlockLightEngine.checkNode, then LightEngine's two passes.
//
// Every decrease runs before any increase. A decrease may enqueue increases (a
// neighbour brighter than the cell being cleared is fed from elsewhere, so it
// re-lights the hole), and an increase only ever enqueues increases, which is
// what keeps the passes from interleaving.
func (b *BlockLight) SetEmission(env LightEnv, x, y, z int32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := &lightPass{b: b, env: env}

	key := key3(x, y, z)
	old := b.m[key]
	emit := env.Emission(x, y, z)
	if emit < old {
		// The light that was here is gone: take it away and walk outwards.
		delete(b.m, key)
		p.dec = append(p.dec, lightEntry{x, y, z, old, allDirs, false})
	} else {
		// Nothing is being removed, but the cell may have opened up. A level-1
		// decrease in every direction is vanilla's PULL_LIGHT_IN_ENTRY: no
		// neighbour is dimmer, so each lit one is asked to re-propagate back
		// into this cell — which is how light flows into the space a broken
		// block left behind.
		p.dec = append(p.dec, lightEntry{x, y, z, 1, allDirs, false})
	}
	// An emitter lights its neighbours whether or not it blocks light itself:
	// glowstone is solid and still lights the room. Requiring the source to be
	// see-through left every solid light source dark.
	if emit > 0 {
		p.inc = append(p.inc, lightEntry{x, y, z, emit, allDirs, true})
	}

	for len(p.dec) > 0 {
		e := p.dec[0]
		p.dec = p.dec[1:]
		p.decrease(e)
	}
	for len(p.inc) > 0 {
		e := p.inc[0]
		p.inc = p.inc[1:]
		p.increase(e)
	}
}

// lightPass is the two passes over one change, and what they are working on.
type lightPass struct {
	b   *BlockLight
	env LightEnv
	dec []lightEntry
	inc []lightEntry
}

// decrease is BlockLightEngine.propagateDecrease.
func (p *lightPass) decrease(e lightEntry) {
	for i, d := range lightDirs {
		if e.dirs&dirBit(i) == 0 {
			continue
		}
		nx, ny, nz := e.x+d[0], e.y+d[1], e.z+d[2]
		nkey := key3(nx, ny, nz)
		to := p.b.m[nkey]
		if to == 0 {
			continue
		}
		if to <= e.level-1 {
			// Dim enough that only the cell being cleared could have lit it,
			// so it goes too — and it walks on, in every direction but the one
			// back here.
			nEmit := p.env.Emission(nx, ny, nz)
			delete(p.b.m, nkey)
			if nEmit < to {
				p.dec = append(p.dec, lightEntry{nx, ny, nz, to, exceptDir(oppositeDir(i)), false})
			}
			if nEmit > 0 {
				p.inc = append(p.inc, lightEntry{nx, ny, nz, nEmit, allDirs, true})
			}
			continue
		}
		// Brighter than this cell could have fed: something else lights it, so
		// instead of clearing it, ask it to re-light the hole — back the way it
		// was reached and nowhere else.
		p.inc = append(p.inc, lightEntry{nx, ny, nz, to, dirBit(oppositeDir(i)), false})
	}
}

// increase is BlockLightEngine.propagateIncrease. Block shapes are not modelled
// anywhere in this engine — dampening and opacity are the whole of it — so
// vanilla's shape-occlusion test has no counterpart here.
func (p *lightPass) increase(e lightEntry) {
	key := key3(e.x, e.y, e.z)
	level := p.b.m[key]
	if e.emission && level < e.level {
		p.b.m[key] = e.level
		level = e.level
	}
	// The gate from propagateIncreases: an entry propagates only when the
	// position has reached the level it promised. If it has gone higher since,
	// that brighter light is on its own way already.
	if level != e.level {
		return
	}
	for i, d := range lightDirs {
		if e.dirs&dirBit(i) == 0 {
			continue
		}
		nx, ny, nz := e.x+d[0], e.y+d[1], e.z+d[2]
		nkey := key3(nx, ny, nz)
		to := p.b.m[nkey]
		// Vanilla's step, from LightEngine.getOpacity: max(1, dampening).
		// Through air that is one, through water or leaves two, and a solid
		// block's 15 means it cannot pass at all.
		step := p.env.Dampening(nx, ny, nz)
		if step < 1 {
			step = 1
		}
		if int(e.level) <= int(step) {
			continue
		}
		next := e.level - step
		if next <= to {
			continue
		}
		p.b.m[nkey] = next
		if next > 1 {
			p.inc = append(p.inc, lightEntry{nx, ny, nz, next, exceptDir(oppositeDir(i)), false})
		}
	}
}

func key3(x, y, z int32) [3]int32 { return [3]int32{x, y, z} }

// SectionsNear lists, per chunk, which sections hold block light close enough
// to a position to have changed with it. A light update only has to carry
// those, rather than every section of every chunk the light reaches.
func (b *BlockLight) SectionsNear(x, y, z int32, radius int32, minY int) map[[2]int32][]int {
	out := make(map[[2]int32][]int)
	b.mu.Lock()
	defer b.mu.Unlock()
	for pos := range b.m {
		dx, dy, dz := pos[0]-x, pos[1]-y, pos[2]-z
		if dx < -radius || dx > radius || dy < -radius || dy > radius || dz < -radius || dz > radius {
			continue
		}
		cx, cz := pos[0]>>4, pos[2]>>4
		section := int((pos[1] - int32(minY)) / 16)
		key := [2]int32{cx, cz}
		if !contains(out[key], section) {
			out[key] = append(out[key], section)
		}
	}
	return out
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
