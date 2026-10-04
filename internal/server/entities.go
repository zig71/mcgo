package server

import (
	"encoding/binary"
	"log"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Entity kinds. The kind decides the physics and what the entity does on each
// tick; Type is the registry id, which is what the client draws.
const (
	kindMob = iota
	kindTNT
	kindItem
	kindProjectile
)

// Entity is anything that is not a player: a mob, primed TNT, a dropped item.
type Entity struct {
	ID   int32
	uuid [16]byte
	Type int32
	Kind int

	X, Y, Z    float64
	VX, VY, VZ float64
	OnGround   bool

	// Yaw is which way it faces, and LastYaw what the client was last told.
	// WanderTicks counts down to a mob's next decision.
	Yaw         float32
	LastYaw     float32
	WanderTicks int32
	Walking     bool

	// Health and HurtTicks are a mob's condition. Health 0 on a mob that has
	// never been hit means "not filled in yet": mobHealthFor supplies the
	// starting value. HurtTicks is the immunity after a hit.
	Health    float32
	HurtTicks int32

	// AmbientTicks counts down to a mob's next noise of its own, and
	// StepDistance is how far it has walked since its last footstep.
	AmbientTicks int32
	StepDistance float64
	// Fuse counts down on primed TNT.
	Fuse int32
	// Owner is the entity that threw a projectile, which is what its first two
	// ticks ignore and what an ender pearl teleports.
	Owner int32
	// Arrow marks a projectile that is an arrow: heavier, and it hurts what it
	// hits rather than bursting.
	Arrow bool
	// Item is the stack a dropped item carries, and Age counts its ticks for
	// despawning.
	Item world.Item
	Age  int32
}

// Metadata index of a dropped item's stack on an ItemEntity, and the serializer
// it goes through. Entity has eight accessors, so an entity that extends it
// directly starts at eight, and ITEM_STACK is the eighth serializer registered.
const (
	metaItemStack       = 8
	serializerItemStack = 7
)

// itemDespawnTicks is how long a dropped item lasts before it vanishes.
const itemDespawnTicks = 6000

// maxExplosionDrops caps how many items one explosion leaves behind.
const maxExplosionDrops = 64

// tntFuse is how long primed TNT burns before it goes off, in ticks.
const tntFuse = 80

// tntPower is primed TNT's explosion radius, in blocks.
const tntPower = 4.0

// levelEventBreak is the level event that plays a block's break sound and
// particles. Its data is the block state that was there.
const levelEventBreak = 2001

// entities is a snapshot of the entity store: the tick loop mutates it while
// packets are being built from it.
func (s *Server) entities() []*Entity {
	s.entMu.Lock()
	defer s.entMu.Unlock()
	out := make([]*Entity, 0, len(s.ents))
	for _, e := range s.ents {
		out = append(out, e)
	}
	return out
}

// entityByID is one entity, or nil.
func (s *Server) entityByID(id int32) *Entity {
	s.entMu.Lock()
	defer s.entMu.Unlock()
	return s.ents[id]
}

// spawnEntity adds an entity and tells everyone about it.
func (s *Server) spawnEntity(kind int, typeID int32, x, y, z float64) *Entity {
	e := &Entity{
		ID: s.entitySeq.Add(1), Type: typeID, Kind: kind, X: x, Y: y, Z: z,
		// Staggered, so a pen of mobs does not all speak on the same tick.
		AmbientTicks: int32(ambientMinTicks + rand.IntN(ambientMaxTicks-ambientMinTicks)),
	}
	// A uuid the client only needs to be unique; the id is already unique.
	binary.BigEndian.PutUint32(e.uuid[0:4], uint32(e.ID))
	binary.BigEndian.PutUint32(e.uuid[4:8], uint32(e.Type))
	e.uuid[6] = (e.uuid[6] & 0x0f) | 0x40
	e.uuid[8] = (e.uuid[8] & 0x3f) | 0x80

	s.entMu.Lock()
	if s.ents == nil {
		s.ents = make(map[int32]*Entity)
	}
	s.ents[e.ID] = e
	s.entMu.Unlock()

	body := addEntityBody(e.ID, e.uuid, e.Type, x, y, z, 0, 0)
	s.viewersAt(x, y, z, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundAddEntity, body)
	})
	log.Printf("spawned entity %d (type %d) at (%.1f,%.1f,%.1f)", e.ID, e.Type, x, y, z)
	return e
}

// removeEntity despawns an entity everywhere.
func (s *Server) removeEntity(id int32) {
	s.entMu.Lock()
	e, ok := s.ents[id]
	if !ok {
		s.entMu.Unlock()
		return
	}
	delete(s.ents, id)
	s.entMu.Unlock()

	var w mcproto.Writer
	w.WriteVarInt(1) // one entity
	w.WriteVarInt(id)
	body := w.Bytes()
	// The entity is gone from the store, so its position has to come from the
	// copy taken above: only the players who could see it are told.
	s.viewersAt(e.X, e.Y, e.Z, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundRemoveEntities, body)
	})
}

// tickEntities advances every entity by one tick.
func (s *Server) tickEntities() {
	list := s.entities()
	if len(list) == 0 {
		return
	}
	t := s.newest
	w := s.worldFor(t)
	for _, e := range list {
		s.stepEntity(t, w, e)
	}
}

// spawnEggEntity is the mob a spawn egg holds: minecraft:zombie_spawn_egg is
// minecraft:zombie. The namespace stays on, which is what the entity registry
// is keyed by.
func spawnEggEntity(itemName string) (string, bool) {
	const suffix = "_spawn_egg"
	if !strings.HasSuffix(itemName, suffix) {
		return "", false
	}
	return strings.TrimSuffix(itemName, suffix), true
}

// blockName is the name of the block at a position, or "" when the state is
// not in the table.
func (s *session) blockName(x, y, z int32) string {
	name, _, ok := s.srv.blockStateName(s.t, s.world.Block(x, y, z))
	if !ok {
		return ""
	}
	return name
}

// solidAt reports whether a block is solid, which is what stops a fall.
func (s *Server) solidAt(t proto.Tables, w *world.World, x, y, z float64) bool {
	return w.Opaque(int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z)))
}

// levelEvent tells clients about something that happened in the world: here,
// a block breaking, which is its sound and particles.
//
// The body is the event as an int, the position, the event's data (for a block
// break, the block state that was there) and whether it is a global event —
// which is what decides whether clients far away play it too.
func (s *Server) levelEvent(t proto.Tables, x, y, z int32, event int32, data int32) {
	var w mcproto.Writer
	w.WriteInt(event)
	w.WriteLong(packPosition(int(x), int(y), int(z)))
	w.WriteInt(data)
	w.WriteBool(false) // not global: this is an event at one place
	body := w.Bytes()
	s.viewersAt(float64(x), float64(y), float64(z), func(sess *session) {
		if sess.t.PlayClientboundLevelEvent == 0 {
			return
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundLevelEvent, body)
	})
}

// stepEntity is one tick of an entity: gravity, landing, the move that is sent
// to clients, and whatever the entity does with its time.
func (s *Server) stepEntity(t proto.Tables, w *world.World, e *Entity) {
	// A thrown thing moves on its own numbers and stops at what it hits, so it
	// has its own pass rather than sharing the mob path's drag and landing.
	if e.Kind == kindProjectile {
		s.stepProjectile(t, w, e)
		return
	}
	switch e.Kind {
	case kindMob:
		e.VY -= 0.08
		s.driveMob(t, w, e)
	default:
		e.VY -= 0.04
	}
	// Water and lava carry an entity along their current. Without this a stream
	// moves the blocks but not whatever is floating in it, which is what makes
	// a dropped item sit still in the middle of a waterfall.
	if cx, cy, cz := s.fluidCurrent(t, w, e); cx != 0 || cy != 0 || cz != 0 {
		e.VX += cx
		e.VY += cy
		e.VZ += cz
	}
	e.VX *= 0.98
	e.VZ *= 0.98

	nx, ny, nz := e.X+e.VX, e.Y+e.VY, e.Z+e.VZ
	if e.VY < 0 && s.solidAt(t, w, nx, ny, nz) {
		// Land on top of the block below.
		ny = math.Floor(ny) + 1
		e.VY = 0
		e.OnGround = true
	} else {
		e.OnGround = false
	}

	if e.Kind == kindMob {
		// A footstep every block and a half, taken from the block under the
		// mob's feet. This has to be measured before the position moves on: a
		// player's own steps are the client's business, but a mob walking in
		// silence is the thing everyone notices.
		e.StepDistance += math.Hypot(nx-e.X, nz-e.Z)
		if e.StepDistance >= 1.5 {
			e.StepDistance = 0
			fx, fz := int32(math.Floor(e.X)), int32(math.Floor(e.Z))
			fy := int32(math.Floor(e.Y - 1))
			if name, _, ok := s.blockStateName(t, w.Block(fx, fy, fz)); ok {
				s.playBlockSound(t, fx, fy, fz, name, "step")
			}
		}
	}

	s.moveEntityAt(e, nx, ny, nz)

	if e.Kind == kindMob {
		e.AmbientTicks--
		if e.AmbientTicks <= 0 {
			e.AmbientTicks = int32(ambientMinTicks + rand.IntN(ambientMaxTicks-ambientMinTicks))
			s.playMobSoundFrom(t, proto.EntityTypeName(t, e.Type), "ambient", e.ID, e.X, e.Y, e.Z, 1, 1)
		}
	}
	if e.HurtTicks > 0 {
		e.HurtTicks--
	}
	switch e.Kind {
	case kindTNT:
		e.Fuse--
		if e.Fuse <= 0 {
			s.removeEntity(e.ID)
			s.explode(t, w, e.X, e.Y, e.Z, tntPower)
		}
	case kindItem:
		e.Age++
		if e.Age > itemDespawnTicks {
			s.removeEntity(e.ID)
			return
		}
		if sess, picked, took := s.pickupItem(e); took {
			s.removeEntity(e.ID)
			// The client shows the item flying into the player, and the slot
			// contents have changed. The amount is how many went in, which is
			// not what is left on the entity: that is now nothing.
			var w mcproto.Writer
			w.WriteVarInt(e.ID)
			w.WriteVarInt(sess.entityID)
			w.WriteVarInt(picked)
			body := w.Bytes()
			s.viewersAt(e.X, e.Y, e.Z, func(o *session) {
				_ = o.sendQuiet(o.t.PlayClientboundTakeItemEntity, body)
			})
			if err := sess.sendInventory(); err != nil {
				return
			}
		}
	}
}

// sendVisibleEntities tells a player about the entities already around them.
// Without it, a player who joins an active world sees none of them until each
// one happens to move.
func (s *Server) sendVisibleEntities(sess *session) {
	for _, e := range s.entities() {
		if !sess.seesPos(e.X, e.Y, e.Z) {
			continue
		}
		_ = sess.sendQuiet(sess.t.PlayClientboundAddEntity,
			addEntityBody(e.ID, e.uuid, e.Type, e.X, e.Y, e.Z, e.Yaw, 0))
		if e.Kind != kindItem {
			continue
		}
		// A dropped item is its stack, and the client cannot draw it without.
		_ = sess.sendQuiet(sess.t.PlayClientboundSetEntityData,
			sess.entityMetadataBody(e.ID, metaItemStack, serializerItemStack,
				func(w *mcproto.Writer) { sess.writeSlot(w, e.Item) }))
	}
}

// spawnItemEntity drops an item stack, telling clients what it is.
//
// The stack travels as entity metadata: without it the client has an entity to
// draw and nothing to draw in it.
func (s *Server) spawnItemEntity(t proto.Tables, stack world.Item, x, y, z float64) *Entity {
	if stack.Empty() {
		return nil
	}
	// Merge into a dropped item already nearby, the way vanilla does.
	for _, e := range s.entities() {
		if e.Kind != kindItem || !e.Item.Same(stack) {
			continue
		}
		dx, dy, dz := e.X-x, e.Y-y, e.Z-z
		if dx*dx+dy*dy+dz*dz > 1 {
			continue
		}
		space := 64 - e.Item.Count
		if space <= 0 {
			continue
		}
		taken := min(space, stack.Count)
		e.Item.Count += taken
		stack.Count -= taken
		if stack.Count <= 0 {
			return e
		}
	}

	typeID, ok := t.EntityIDs["minecraft:item"]
	if !ok {
		return nil
	}
	e := s.spawnEntity(kindItem, typeID, x, y, z)
	e.Item = stack
	s.sendItemMetadata(e)
	return e
}

// sendExplode is the one packet an explosion sends: where it was, the particle
// burst, and the sound. The blocks themselves were already removed one by one,
// so the position list inside it is empty.
func (s *Server) sendExplode(t proto.Tables, x, y, z, radius float64) {
	particle, ok := t.ParticleTypeIDs["minecraft:explosion_emitter"]
	if !ok {
		particle = t.ParticleTypeIDs["minecraft:explosion"]
	}
	sound, ok := t.SoundEventIDs["minecraft:entity.generic.explode"]
	if !ok || t.PlayClientboundExplode == 0 {
		return
	}

	// The layout, field by field: the centre, the radius, how many blocks were
	// destroyed, the knockback an entity takes, the particle, the sound, the
	// debris list, and whether to play the sound at all. Leaving out the radius
	// and the block count, as this did at first, is a packet the client cannot
	// decode.
	var w mcproto.Writer
	w.WriteDouble(x)
	w.WriteDouble(y)
	w.WriteDouble(z)
	w.WriteFloat(float32(radius))
	w.WriteInt(0)      // block count: the blocks were sent one by one already
	w.WriteBool(false) // no knockback for the player
	w.WriteVarInt(particle)
	// A sound is a *holder* here, and the holder codec writes the registry id
	// plus one so that zero can mean an inline sound; the damage type's codec
	// writes the bare id. Writing the bare id plays the wrong sound.
	w.WriteVarInt(sound + 1)
	w.WriteVarInt(0)  // no weighted debris list
	w.WriteBool(true) // play the sound
	body := w.Bytes()

	s.viewersAt(x, y, z, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundExplode, body)
	})
}

// sendItemMetadata tells clients which stack a dropped item is.
func (s *Server) sendItemMetadata(e *Entity) {
	s.viewersAt(e.X, e.Y, e.Z, func(sess *session) {
		sess.sendQuiet(sess.t.PlayClientboundSetEntityData, sess.entityMetadataBody(e.ID, metaItemStack, serializerItemStack,
			func(w *mcproto.Writer) { sess.writeSlot(w, e.Item) }))
	})
}

// pickupItem gives a dropped item to the nearest player who can hold it. It
// returns that session, how much was taken, and whether it went anywhere.
//
// Vanilla has the item's box touch the player's; a distance test is the
// approximation here.
func (s *Server) pickupItem(e *Entity) (*session, int32, bool) {
	for _, sess := range s.sessionList() {
		dx, dy, dz := sess.posX-e.X, sess.posY-e.Y, sess.posZ-e.Z
		if dx*dx+dy*dy+dz*dz > 2.25 { // within a block and a half
			continue
		}
		if !sess.hasRoomFor(e.Item) {
			continue
		}
		left := e.Item
		if !sess.takeItem(&left) {
			continue
		}
		picked := e.Item.Count - left.Count
		e.Item = left
		return sess, picked, true
	}
	return nil, 0, false
}

// hasRoomFor reports whether a stack would fit in the player's inventory.
func (s *session) hasRoomFor(it world.Item) bool {
	for _, slot := range s.inv {
		if slot.Empty() || (slot.Same(it) && slot.Count < 64) {
			return true
		}
	}
	return false
}

// takeItem adds what fits of a stack to the player's inventory, filling
// matching stacks first and then empty slots, as Inventory.add does.
func (s *session) takeItem(it *world.Item) bool {
	if it.Empty() {
		return false
	}
	taken := it.Count
	for i := range s.inv {
		slot := s.inv[i]
		if slot.Empty() || !slot.Same(*it) || slot.Count >= 64 {
			continue
		}
		n := min(64-slot.Count, it.Count)
		slot.Count += n
		it.Count -= n
		s.inv[i] = slot
		if it.Count <= 0 {
			break
		}
	}
	for i := range s.inv {
		if it.Count <= 0 {
			break
		}
		if !s.inv[i].Empty() {
			continue
		}
		n := min(64, it.Count)
		s.inv[i] = world.Item{ID: it.ID, Count: n, Components: it.Components}
		it.Count -= n
	}
	return it.Count < taken
}

// explode removes the blocks in a blast, tells clients, leaves some of it
// behind as items, and hurts whatever is standing in it.
func (s *Server) explode(t proto.Tables, w *world.World, x, y, z float64, power float64) {
	started := time.Now()
	r := int32(math.Ceil(power))
	cx, cy, cz := int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z))
	removed, dropped := 0, 0

	// The blast changes the world first, without sending anything. A blast
	// touches hundreds of blocks, and the light update that setWorldBlock sends
	// for each one — and the further ones that SectionsNear turns up — was
	// thousands of packets for a single explosion. The client re-meshes as each
	// one arrives, which is what made the blast look like it was eating the
	// ground row by row.
	type blockUpdate struct{ x, y, z int32 }
	var updates []blockUpdate
	changed := map[[2]int32]map[int]bool{}
	var order [][2]int32

	mark := func(chunk [2]int32, secs []int) {
		if changed[chunk] == nil {
			changed[chunk] = map[int]bool{}
			order = append(order, chunk)
		}
		for _, sec := range secs {
			changed[chunk][sec] = true
		}
	}

	for dx := -r; dx <= r; dx++ {
		for dy := -r; dy <= r; dy++ {
			for dz := -r; dz <= r; dz++ {
				if float64(dx*dx+dy*dy+dz*dz) > power*power {
					continue
				}
				bx, by, bz := cx+dx, cy+dy, cz+dz
				state := w.Block(bx, by, bz)
				if state == t.BlockAir {
					continue
				}
				chunkX, chunkZ, secs := w.SetBlock(bx, by, bz, t.BlockAir)
				w.RelightBlock(bx, by, bz)
				mark([2]int32{chunkX, chunkZ}, secs)
				updates = append(updates, blockUpdate{bx, by, bz})
				s.containers.Remove(bx, by, bz)
				// An explosion drops most of what it breaks. Capped, because a
				// large blast would otherwise spawn hundreds of entities at
				// once and every one of them is a packet.
				if tileDrops, _ := s.rule("doTileDrops"); dropped < maxExplosionDrops && tileDrops {
					if name, _, ok := s.blockStateName(t, state); ok {
						if item, ok := t.BlockItems[name]; ok {
							dropped++
							s.spawnItemEntity(t, world.Item{ID: item, Count: 1},
								float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5)
						}
					}
				}
				removed++
			}
		}
	}

	// The blocks, one packet each, which is what vanilla sends too.
	for _, u := range updates {
		var wu mcproto.Writer
		wu.WriteLong(packPosition(int(u.x), int(u.y), int(u.z)))
		wu.WriteVarInt(t.BlockAir)
		body := wu.Bytes()
		s.viewersAt(float64(u.x), float64(u.y), float64(u.z), func(sess *session) {
			_ = sess.sendQuiet(sess.t.PlayClientboundBlockUpdate, body)
		})
	}

	// Then the light, once per chunk the blast touched, plus the sections the
	// light spread through: one call for the whole blast rather than one per
	// block.
	sections := func(chunk [2]int32, secs map[int]bool) []int {
		out := make([]int, 0, len(secs))
		for sec := range secs {
			out = append(out, sec)
		}
		return out
	}
	for _, chunk := range order {
		body := w.LightUpdate(chunk[0], chunk[1], sections(chunk, changed[chunk]))
		s.viewersAt(x, y, z, func(sess *session) {
			if sess.t.PlayClientboundLightUpdate != 0 {
				_ = sess.sendQuiet(sess.t.PlayClientboundLightUpdate, body)
			}
		})
	}
	for chunk, secs := range w.Light().SectionsNear(cx, cy, cz, r+world.MaxLight, w.MinY()) {
		body := w.LightUpdate(chunk[0], chunk[1], secs)
		s.viewersAt(x, y, z, func(sess *session) {
			if sess.t.PlayClientboundLightUpdate != 0 {
				_ = sess.sendQuiet(sess.t.PlayClientboundLightUpdate, body)
			}
		})
	}

	s.sendExplode(t, x, y, z, power)
	s.damageInBlast(t, x, y, z, power)
	log.Printf("explosion at (%.1f,%.1f,%.1f) removed %d blocks, dropped %d items, %d light updates in %v",
		x, y, z, removed, dropped, len(order), time.Since(started).Round(time.Millisecond))
}
