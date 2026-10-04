package server

import (
	"math"
	"math/rand/v2"

	"mcgo/internal/proto"
	"mcgo/internal/world"
)

// Mob behaviour: enough of it that the world is not full of statues.
//
// Vanilla gives every mob a list of goals and a pathfinder. This is a random
// walk with a wall check and a look-at-the-player reflex, which is the part a
// player actually notices.

const (
	// mobWalkSpeed is how far a mob moves in one tick. Vanilla reads this from
	// each mob's movement attribute.
	mobWalkSpeed = 0.05
	// mobLookRange is how close a player has to come before a mob stops and
	// stares at them.
	mobLookRange = 8.0
)

// driveMob decides where a mob is going this tick and pushes it that way. It
// runs before the shared step applies gravity and the move.
func (s *Server) driveMob(t proto.Tables, w *world.World, e *Entity) {
	// A player nearby wins over whatever the mob was doing: it turns to face
	// them and stands still.
	if px, pz, ok := s.nearestPlayer(e.X, e.Z, e.Y); ok {
		e.Yaw = lookYaw(e.X, e.Z, px, pz)
		e.Walking = false
		e.VX, e.VZ = 0, 0
		return
	}

	if e.WanderTicks <= 0 {
		s.decideMob(e)
	}
	e.WanderTicks--

	if !e.Walking {
		e.VX, e.VZ = 0, 0
		return
	}

	rad := float64(e.Yaw) * math.Pi / 180
	e.VX, e.VZ = -math.Sin(rad)*mobWalkSpeed, math.Cos(rad)*mobWalkSpeed

	// A block in the way, and the mob turns rather than pushing into it. The
	// check is two ticks ahead, so it stops before its body is inside. The
	// turn is a half circle plus a jitter, so a mob in a corner does not
	// bounce between two directions forever.
	if s.solidAt(t, w, e.X+e.VX*2, e.Y, e.Z+e.VZ*2) {
		e.Yaw += 180 + rand.Float32()*90 - 45
		e.WanderTicks = int32(40 + rand.IntN(60))
		e.VX, e.VZ = 0, 0
	}
}

// decideMob picks what a mob does next: stand for a moment, or walk in some
// direction for a moment.
func (s *Server) decideMob(e *Entity) {
	if rand.IntN(3) == 0 {
		e.Walking = false
		e.WanderTicks = int32(40 + rand.IntN(60)) // two to five seconds
		return
	}
	e.Yaw = rand.Float32() * 360
	e.Walking = true
	e.WanderTicks = int32(40 + rand.IntN(80))
}

// nearestPlayer is the closest player within reach, as a point on the ground.
func (s *Server) nearestPlayer(x, z, y float64) (float64, float64, bool) {
	best := mobLookRange * mobLookRange
	var bx, bz float64
	found := false
	for _, sess := range s.sessionList() {
		d := sq(sess.posX-x) + sq(sess.posZ-z)
		if d > best || math.Abs(sess.posY-y) > 4 {
			continue
		}
		best, bx, bz, found = d, sess.posX, sess.posZ, true
	}
	return bx, bz, found
}

// lookYaw is the yaw that faces a point, in the client's convention: zero is
// south, and it grows clockwise.
func lookYaw(x, z, tx, tz float64) float32 {
	return float32(math.Atan2(tz-z, tx-x)*180/math.Pi) - 90
}
