package server

import (
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// The chat session update and the chat ack are the two serverbound packets of
// the signed-chat protocol. This server does not do signed chat: it sends
// disguised chat, which carries no signature, so there is nothing to verify and
// nothing to acknowledge. The packets are tolerated rather than acted on, and
// this pins that: a client that sends them is not disconnected, and a player
// who keeps talking afterwards is fine.
func TestSignedChatPacketsAreTolerated(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	s, _, log := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	// Chat ack: one varint.
	var ack mcproto.Writer
	ack.WriteVarInt(0)
	s.handlePlay(&mcproto.Packet{ID: tab.PlayServerboundChatAck, Data: ack.Bytes()})

	// Chat session update: a session uuid and a profile public key of an
	// expiry, a key and a signature.
	var session mcproto.Writer
	session.WriteBytes(make([]byte, 16)) // session id
	session.WriteLong(0)                 // key expiry
	session.WriteVarInt(0)               // key bytes: empty
	session.WriteVarInt(0)               // signature: empty
	s.handlePlay(&mcproto.Packet{ID: tab.PlayServerboundChatSessionUpdate, Data: session.Bytes()})

	if !s.ignored[tab.PlayServerboundChatAck] || !s.ignored[tab.PlayServerboundChatSessionUpdate] {
		t.Errorf("the chat packets were not recognised as tolerated: %v", s.ignored)
	}

	// The session still works: a chat message goes out to everyone.
	s.handlePlay(&mcproto.Packet{ID: tab.PlayServerboundChat, Data: chatMessageBody(t, "hello")})
	waitFor(t, log, tab.PlayClientboundDisguisedChat)
}

// chatMessageBody is a serverbound chat message: the text, then the timestamp,
// the salt and the signature tail, none of which this server reads.
func chatMessageBody(t *testing.T, msg string) []byte {
	t.Helper()
	var w mcproto.Writer
	w.WriteString(msg)
	w.WriteLong(0)                // timestamp
	w.WriteLong(0)                // salt
	w.WriteVarInt(0)              // no signature
	w.WriteVarInt(0)              // no message count
	w.WriteBytes(make([]byte, 3)) // acknowledged bitset
	return w.Bytes()
}
