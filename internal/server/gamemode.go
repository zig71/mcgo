package server

import (
	"mcgo/internal/mcproto"
)

// Game modes. A mode decides two things on this server: whether the player can
// be hurt, and what the client is told about flying and instant breaking.

const (
	gameModeSurvival = 0
	gameModeCreative = 1

	// Ability flags, from the abilities packet.
	abilityInvulnerable = 0x01
	abilityFlying       = 0x02
	abilityAllowFlying  = 0x04
	abilityCreative     = 0x08
)

// sendAbilities tells the client what its game mode allows.
func (s *session) sendAbilities() error {
	var w mcproto.Writer
	if s.gameMode == gameModeCreative {
		w.WriteByte(abilityInvulnerable | abilityAllowFlying | abilityCreative)
	} else {
		w.WriteByte(0) // survival: hurtable, no flight, no instant break
	}
	w.WriteFloat(0.05) // flying speed
	w.WriteFloat(0.1)  // walking speed / fov modifier
	return s.send(s.t.PlayClientboundPlayerAbilities, w.Bytes())
}

// setGameMode changes a player's mode and tells them, so the client's HUD,
// flight and death screen agree with what the server will do. The game event
// is what vanilla sends for this; without it the client's own idea of the mode
// drifts from the server's.
func (s *session) setGameMode(mode int32) error {
	if mode != gameModeCreative {
		mode = gameModeSurvival
	}
	s.gameMode = mode
	s.invulnerable = mode == gameModeCreative

	if err := s.sendAbilities(); err != nil {
		return err
	}
	var w mcproto.Writer
	w.WriteByte(3) // game event: change game mode
	w.WriteFloat(float32(mode))
	return s.send(s.t.PlayClientboundGameEvent, w.Bytes())
}
