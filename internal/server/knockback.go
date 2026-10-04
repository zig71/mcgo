package server

import (
	"math"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto"
)

// Velocity on the wire, and the knockback that uses it.
//
// Add Entity writes its velocity with the low-precision Vec3 codec and this
// server only ever wrote the zero case of it — an entity spawned standing
// still. Anything that moves an entity after it exists needs the rest of the
// codec, so it is here rather than inline in a packet body: projectiles and
// vehicles will want it too.

// writeLpVec encodes a velocity the way Vec3.LP_STREAM_CODEC does: a single zero
// byte for "nothing", otherwise a scale and three 15-bit directions packed into
// a byte and a 32-bit int, with a VarInt carrying the scale's high bits when the
// scale is not a multiple of four.
func writeLpVec(w *mcproto.Writer, x, y, z float64) {
	x, y, z = sanitizeLpVec(x), sanitizeLpVec(y), sanitizeLpVec(z)
	longest := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if longest < 3.051944088384301e-5 {
		w.WriteByte(0)
		return
	}
	scale := int64(math.Ceil(longest))
	partial := scale&3 != scale
	markers := scale
	if partial {
		markers = scale&3 | 4
	}
	buffer := markers |
		packLpVec(x/float64(scale))<<3 |
		packLpVec(y/float64(scale))<<18 |
		packLpVec(z/float64(scale))<<33
	w.WriteByte(byte(buffer))
	w.WriteByte(byte(buffer >> 8))
	w.WriteInt(int32(buffer >> 16))
	if partial {
		w.WriteVarInt(int32(scale >> 2))
	}
}

// sanitizeLpVec is LpVec3.sanitize: a NaN becomes zero, and the value is
// clamped to the range the encoding can carry.
func sanitizeLpVec(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Min(math.Max(v, -1.7179869183e10), 1.7179869183e10)
}

// packLpVec quantises one axis into 15 bits, -1..1.
func packLpVec(v float64) int64 { return int64(math.Round((v*0.5 + 0.5) * 32766.0)) }

// knockback gives an entity a velocity and tells the clients watching it, which
// is how vanilla moves a mob that has been hit. The server's own simulation uses
// the same velocity, so the mob walks back the way it was knocked.
func (s *Server) knockback(t proto.Tables, e *Entity, vx, vy, vz float64) {
	if t.PlayClientboundSetEntityMotion == 0 {
		return
	}
	e.VX, e.VY, e.VZ = vx, vy, vz
	var w mcproto.Writer
	w.WriteVarInt(e.ID)
	writeLpVec(&w, vx, vy, vz)
	body := w.Bytes()
	s.viewersAt(e.X, e.Y, e.Z, func(sess *session) {
		_ = sess.sendQuiet(sess.t.PlayClientboundSetEntityMotion, body)
	})
}
