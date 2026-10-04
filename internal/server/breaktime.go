package server

import "mcgo/internal/proto"

// fnv32 is a cheap hash of a packet body for the debug log: it only has to
// differ when the bytes differ, so a chunk resent unchanged is recognisable.
func fnv32(b []byte) uint32 {
	h := uint32(2166136261)
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

// unbreakable reports whether a block state's hardness says it can never be
// broken: bedrock, barriers, the end portal frame, and the rest. It is the one
// use of the hardness table that does not need a tool.
func unbreakable(t proto.Tables, state int32) bool {
	return hardness(t, state) < 0
}
