package server

import (
	"log"
	"math"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Thrown projectiles: a snowball, an egg, an ender pearl.
//
// The physics is ThrowableProjectile's: gravity 0.03 a tick, drag 0.99 on all
// three axes, and a burst the moment it touches a block or an entity. A snowball
// or an egg bursts with entity event 3, which is the puff of particles and its
// sound in one packet — vanilla's own way of doing it, and the only particles
// this server sends at all.
//
// The ender pearl is the one that does something else: it teleports whoever
// threw it to where it landed, at the cost of five damage in survival.

// throwables maps a throwable item to the entity it becomes and the sound the
// throw makes.
var throwables = map[string]struct {
	entity string
	sound  string
}{
	"minecraft:snowball":    {"minecraft:snowball", "minecraft:entity.snowball.throw"},
	"minecraft:egg":         {"minecraft:egg", "minecraft:entity.egg.throw"},
	"minecraft:ender_pearl": {"minecraft:ender_pearl", "minecraft:entity.ender_pearl.throw"},
}

// entityEventBurst is the entity event a snowball or an egg ends with.
const entityEventBurst = 3

// throwSpeed is how fast a thrown thing leaves the hand: vanilla shoots it along
// the look vector at 1.5 blocks a tick.
const throwSpeed = 1.5

// projectileGravity and projectileDrag are ThrowableProjectile's own numbers,
// which is why a thrown thing falls slower than a mob walks off a ledge.
const (
	projectileGravity = 0.03
	projectileDrag    = 0.99
)

// projectileStep is how far a projectile travels between collision checks. A
// snowball crosses a block and a half in one tick, so a single check at the end
// of the tick would let it through a wall.
const projectileStep = 0.5

// eyeHeight is where a thrown thing leaves from: the thrower's eyes.
const eyeHeight = 1.62

// projectileReach is how close a projectile has to pass to an entity's centre to
// count as hitting it. The real test is the two bounding boxes overlapping; this
// is that, rounded — the projectile is a point and the entity a small cube.
const projectileReach = 0.7

// throwItem throws what is in hand, and reports whether the item was something
// throwable at all, so the caller can go on to what else using it means.
func (s *session) throwItem(itemID int32) bool {
	name, ok := s.t.ItemName[itemID]
	if !ok {
		return false
	}
	throw, ok := throwables[name]
	if !ok {
		return false
	}
	typeID, ok := s.t.EntityIDs[throw.entity]
	if !ok {
		log.Printf("throwable %q holds unknown entity %q", name, throw.entity)
		return true
	}
	// Along the look vector: yaw 0 faces south, which is +Z, and a pitch of -90
	// is straight down.
	yaw, pitch := float64(s.yaw)*math.Pi/180, float64(s.pitch)*math.Pi/180
	e := s.srv.spawnEntity(kindProjectile, typeID, s.posX, s.posY+eyeHeight, s.posZ)
	e.Owner = s.entityID
	e.VX = -math.Sin(yaw) * math.Cos(pitch) * throwSpeed
	e.VY = -math.Sin(pitch) * throwSpeed
	e.VZ = math.Cos(yaw) * math.Cos(pitch) * throwSpeed
	e.Yaw, e.LastYaw = s.yaw, s.yaw
	// The throw is spent: a snowball is gone, an egg is gone, a pearl is gone.
	s.consumeHeldItem(itemID, 0)
	if id, ok := s.t.SoundEventIDs[throw.sound]; ok {
		s.srv.playSound(s.t, id, soundSourceNeutral, float32(s.posX), float32(s.posY), float32(s.posZ), 0.5, 1)
	}
	return true
}

// stepProjectile is one tick of a thrown thing: gravity, drag, then a swept move
// that stops at the first block or entity in the way.
func (s *Server) stepProjectile(t proto.Tables, w *world.World, e *Entity) {
	e.VY -= projectileGravity
	e.VX *= projectileDrag
	e.VY *= projectileDrag
	e.VZ *= projectileDrag

	x, y, z := e.X, e.Y, e.Z
	dx, dy, dz := e.VX, e.VY, e.VZ
	steps := int(math.Ceil(math.Max(math.Abs(dx), math.Max(math.Abs(dy), math.Abs(dz))) / projectileStep))
	if steps < 1 {
		steps = 1
	}
	// The thrower cannot be hit by its own throw for its first two ticks, which
	// is vanilla's rule and what stops a pearl thrown standing still from
	// teleporting you on the spot.
	hitOwner := e.Age >= 2
	// Sweeping from the tick's own start, not from the last step: stepping from
	// a moving origin would follow the same curve but at the wrong scale.
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		nx, ny, nz := x+dx*f, y+dy*f, z+dz*f
		if ny < float64(w.MinY()) {
			// Out of the world: nothing left to hit.
			s.removeEntity(e.ID)
			return
		}
		if s.solidAt(t, w, nx, ny, nz) {
			s.burstProjectile(t, e, nx, ny, nz)
			return
		}
		if other := s.projectileHit(t, nx, ny, nz, e, hitOwner); other != nil {
			s.burstProjectile(t, e, other.X, other.Y, other.Z)
			return
		}
	}
	e.Age++
	s.moveEntityAt(e, x+dx, y+dy, z+dz)
}

// projectileHit is the first entity the projectile's position is inside, leaving
// out itself and — for the first ticks — whoever threw it.
func (s *Server) projectileHit(t proto.Tables, x, y, z float64, self *Entity, hitOwner bool) *Entity {
	for _, other := range s.entities() {
		if other.ID == self.ID {
			continue
		}
		if !hitOwner && other.ID == self.Owner {
			continue
		}
		if math.Abs(other.X-x) > projectileReach || math.Abs(other.Y+0.5-y) > projectileReach || math.Abs(other.Z-z) > projectileReach {
			continue
		}
		return other
	}
	return nil
}

// burstProjectile ends one where it stopped. A snowball or an egg bursts, which
// the client draws from the entity event; a pearl takes its thrower with it
// instead, to the place the pearl reached.
func (s *Server) burstProjectile(t proto.Tables, e *Entity, x, y, z float64) {
	name := proto.EntityTypeName(t, e.Type)
	if name == "minecraft:ender_pearl" {
		if owner := s.sessionByEntityID(e.Owner); owner != nil {
			owner.teleportTo(x, y, z)
			// The pearl's own damage type, which is what the hurt sound and the
			// death message are drawn from. Creative players do not take it.
			if owner.gameMode == gameModeSurvival {
				owner.hurtPlayer(t, 5, "minecraft:ender_pearl", nil)
			}
		}
	} else {
		s.broadcastEntityEvent(e.ID, e.X, e.Y, e.Z, entityEventBurst)
	}
	s.removeEntity(e.ID)
}

// sessionByEntityID is the session whose player is that entity, which is what a
// pearl needs to find whoever threw it.
func (s *Server) sessionByEntityID(id int32) *session {
	for _, sess := range s.sessionList() {
		if sess.entityID == id {
			return sess
		}
	}
	return nil
}

// moveEntityAt tells everyone watching where an entity went. It is the packet a
// mob's step and a projectile's flight both send, and it carries a head turn
// when the entity has one.
func (s *Server) moveEntityAt(e *Entity, nx, ny, nz float64) {
	turned := e.Yaw != e.LastYaw
	if nx == e.X && ny == e.Y && nz == e.Z && !turned {
		return
	}
	dx := int16((nx - e.X) * 4096)
	dy := int16((ny - e.Y) * 4096)
	dz := int16((nz - e.Z) * 4096)
	e.X, e.Y, e.Z = nx, ny, nz
	e.LastYaw = e.Yaw
	body := moveEntityBody(e.ID, dx, dy, dz, e.Yaw, 0)
	var head []byte
	if turned {
		var h mcproto.Writer
		h.WriteVarInt(e.ID)
		h.WriteByte(angleByte(e.Yaw))
		head = h.Bytes()
	}
	s.viewersAt(e.X, e.Y, e.Z, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundMoveEntityPosRot, body)
		if head != nil && sess.t.PlayClientboundRotateHead != 0 {
			_ = sess.sendQuiet(sess.t.PlayClientboundRotateHead, head)
		}
	})
}
