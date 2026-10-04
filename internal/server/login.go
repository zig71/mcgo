package server

import (
	"crypto/md5"
	"fmt"
	"log"

	"mcgo/internal/mcproto"
)

// uuidString formats a UUID in the dashed form used as a persistence key.
func uuidString(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// handleLogin runs the Login state for the session's protocol.
func (s *session) handleLogin(protocol int32) {
	hello, err := s.c.ReadPacket()
	if err != nil || hello.ID != s.t.LoginServerboundHello {
		return
	}
	r := mcproto.NewReader(hello.Data)
	name, err := r.ReadString()
	if err != nil || name == "" {
		return
	}
	// Offline mode: ignore the client-supplied UUID and derive the stable
	// Java offline UUID from the name, as vanilla does.
	s.name = name
	s.uuid = offlineUUID(name)
	s.uuidKey = uuidString(s.uuid)

	var w mcproto.Writer
	w.WriteBytes(s.uuid[:])
	w.WriteString(name)
	w.WriteVarInt(0) // profile properties
	if s.t.LoginSuccessHasSessionID {
		w.WriteBytes(s.uuid[:]) // session id (26.2+)
	}
	if err := s.c.WritePacket(s.t.LoginClientboundLoginFinished, w.Bytes()); err != nil {
		return
	}

	ack, err := s.c.ReadPacket()
	if err != nil || ack.ID != s.t.LoginServerboundLoginAcknowledged {
		return
	}

	log.Printf("login ok: %q uuid=%x protocol=%d -> configuration", name, s.uuid, protocol)
	s.enterConfiguration()
}

// offlineUUID reproduces Java's UUID.nameUUIDFromBytes("OfflinePlayer:"+name):
// MD5, version 3, RFC 4122 variant.
func offlineUUID(name string) [16]byte {
	h := md5.Sum([]byte("OfflinePlayer:" + name))
	h[6] = (h[6] & 0x0f) | 0x30
	h[8] = (h[8] & 0x3f) | 0x80
	return h
}
