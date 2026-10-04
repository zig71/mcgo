package server

import (
	"log"
	"math"
	"math/rand/v2"

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
// is that, rounded — the projectile is a point and the entity as wide as one
// block.
const projectileReach = 0.7

// entityHeight is how tall an entity is taken to be for a projectile's sake.
// Two blocks covers every mob this server spawns and the player's own body.
const entityHeight = 2.0

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

// arrowGravity is an arrow's gravity, which is heavier than a thrown thing's
// (ThrowableProjectile's 0.03 against AbstractArrow's 0.05). The drag is the
// same 0.99.
const arrowGravity = 0.05

// arrowBaseDamage is AbstractArrow's base damage for a plain arrow: the shot's
// speed times this is what it does to what it hits.
const arrowBaseDamage = 2.0

// bowPower is BowItem.getPowerForTime: how far a draw of this many ticks has
// come, from nothing to one. The curve is the game's own, which is why a
// half-drawn bow is much weaker than half strength.
func bowPower(ticks int64) float32 {
	pow := float32(ticks) / 20
	pow = (pow*pow + 2*pow) / 3
	if pow > 1 {
		pow = 1
	}
	return pow
}

// releaseBow fires the bow that use_item started drawing. Vanilla's numbers:
// the shot leaves at three times the charge, the spread is a gaussian of
// 0.0172275 per axis (Projectile.shoot's uncertainty), and a draw under a tenth
// of the way does not fire at all.
//
// What is missing from the whole picture: an arrow that hits a block vanishes
// rather than sticking, it cannot be picked up again, it never crits, and it
// cannot hit a player — this server's projectiles look for mobs and items, not
// sessions.
func (s *session) releaseBow() {
	draw, drawing := s.bowDraw, s.bowDrawing
	s.bowDrawing = false
	s.bowDraw = 0
	// The bow has to still be in hand: switching to something else mid-draw
	// cancels the shot.
	if !drawing || s.t.ItemName[s.mainHandItem().ID] != "minecraft:bow" {
		return
	}
	pow := bowPower(s.srv.Age() - draw)
	if pow < 0.1 {
		return
	}
	typeID, ok := s.t.EntityIDs["minecraft:arrow"]
	if !ok {
		return
	}
	if !s.takeArrow() {
		return
	}
	yaw, pitch := float64(s.yaw)*math.Pi/180, float64(s.pitch)*math.Pi/180
	dx := -math.Sin(yaw) * math.Cos(pitch)
	dy := -math.Sin(pitch)
	dz := math.Cos(yaw) * math.Cos(pitch)
	spread := 0.0172275
	dx += rand.NormFloat64() * spread
	dy += rand.NormFloat64() * spread
	dz += rand.NormFloat64() * spread
	if l := math.Sqrt(dx*dx + dy*dy + dz*dz); l > 1e-6 {
		dx, dy, dz = dx/l, dy/l, dz/l
	}
	speed := float64(pow) * 3.0

	e := s.srv.spawnEntity(kindProjectile, typeID, s.posX, s.posY+eyeHeight, s.posZ)
	e.Owner = s.entityID
	e.Arrow = true
	e.VX, e.VY, e.VZ = dx*speed, dy*speed, dz*speed
	e.Yaw, e.LastYaw = s.yaw, s.yaw
	if id, ok := s.t.SoundEventIDs["minecraft:entity.arrow.shoot"]; ok {
		s.srv.playSound(s.t, id, soundSourcePlayers, float32(s.posX), float32(s.posY), float32(s.posZ), 1, 1)
	}
}

// takeArrow spends one arrow from the inventory, or reports that the shot can
// go ahead without one because the player is in creative. Vanilla takes the
// first arrow it finds of any kind; with no item data here, that is a plain
// arrow and then a tipped one.
func (s *session) takeArrow() bool {
	if s.gameMode == gameModeCreative {
		return true
	}
	for _, want := range []string{"minecraft:arrow", "minecraft:tipped_arrow"} {
		id, ok := s.t.ItemIDs[want]
		if !ok {
			continue
		}
		for i := range s.inv {
			if s.inv[i].ID != id || s.inv[i].Count <= 0 {
				continue
			}
			s.inv[i].Count--
			if s.inv[i].Count <= 0 {
				s.inv[i] = world.Item{}
			}
			s.sendSlot(invWindowSlot(i), s.inv[i])
			return true
		}
	}
	return false
}

// invWindowSlot is where an inventory slot sits in the player's window: the
// hotbar is 36-44 and the main inventory 9-35.
func invWindowSlot(i int) int32 {
	if i >= 27 {
		return int32(36 + i - 27)
	}
	return int32(9 + i)
}

// arrowHit is what an arrow does to the mob it stopped in: it hurts it by the
// shot's speed times the arrow's base damage. The hurt goes through the mob's
// own path, so it flashes, makes its hurt sound and is knocked back the way a
// punch does. The arrow is spent doing it — it does not stick in anything, and
// nothing can pick it up again.
func (s *Server) arrowHit(t proto.Tables, e *Entity, other *Entity, x, y, z float64) {
	if other.Kind == kindMob {
		speed := math.Sqrt(e.VX*e.VX + e.VY*e.VY + e.VZ*e.VZ)
		s.hurtMob(t, other, float32(math.Ceil(speed*arrowBaseDamage)), s.sessionByEntityID(e.Owner))
	}
	if id, ok := t.SoundEventIDs["minecraft:entity.arrow.hit"]; ok {
		s.playSound(t, id, soundSourcePlayers, float32(x), float32(y), float32(z), 1, 1)
	}
	s.removeEntity(e.ID)
}

// stepProjectile is one tick of a thrown thing: gravity, drag, then a swept move
// that stops at the first block or entity in the way.
func (s *Server) stepProjectile(t proto.Tables, w *world.World, e *Entity) {
	gravity := projectileGravity
	if e.Arrow {
		gravity = arrowGravity
	}
	e.VY -= gravity
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
			if e.Arrow {
				s.arrowHit(t, e, other, nx, ny, nz)
				return
			}
			s.burstProjectile(t, e, other.X, other.Y, other.Z)
			return
		}
	}
	e.Age++
	s.moveEntityAt(e, x+dx, y+dy, z+dz)
}

// projectileHit is the first entity the projectile's position is inside, leaving
// out itself and — for the first ticks — whoever threw it.
//
// Horizontally this is a distance from the entity's centre, which is the box it
// really is, rounded. Vertically it is a range from the feet up: without that a
// level shot at eye height passed clean *over* every mob, because an entity was
// being treated as a point at its feet rather than as something two blocks tall.
func (s *Server) projectileHit(t proto.Tables, x, y, z float64, self *Entity, hitOwner bool) *Entity {
	for _, other := range s.entities() {
		if other.ID == self.ID {
			continue
		}
		if !hitOwner && other.ID == self.Owner {
			continue
		}
		if math.Abs(other.X-x) > projectileReach || math.Abs(other.Z-z) > projectileReach {
			continue
		}
		if y < other.Y || y > other.Y+entityHeight {
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
