package server

import (
	"encoding/json"

	"mcgo/internal/mcproto"
)

type statusVersion struct {
	Name     string `json:"name"`
	Protocol int32  `json:"protocol"`
}

type statusPlayers struct {
	Max    int   `json:"max"`
	Online int   `json:"online"`
	Sample []any `json:"sample"`
}

type statusDescription struct {
	Text string `json:"text"`
}

type statusResponse struct {
	Version     statusVersion     `json:"version"`
	Players     statusPlayers     `json:"players"`
	Description statusDescription `json:"description"`
}

// handleStatus implements the Server List Ping exchange. It advertises the
// newest supported protocol so version-mismatched clients see a useful entry.
func (s *Server) handleStatus(c *mcproto.Conn) {
	req, err := c.ReadPacket()
	if err != nil || req.ID != 0 {
		return
	}

	resp := statusResponse{
		Version:     statusVersion{Name: s.newest.VersionName, Protocol: s.newest.Protocol},
		Players:     statusPlayers{Max: s.cfg.MaxPlayers, Online: 0, Sample: []any{}},
		Description: statusDescription{Text: s.cfg.MOTD},
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return
	}
	var w mcproto.Writer
	w.WriteString(string(body))
	if err := c.WritePacket(0x00, w.Bytes()); err != nil {
		return
	}

	ping, err := c.ReadPacket()
	if err != nil || ping.ID != 1 {
		return
	}
	_ = c.WritePacket(1, ping.Data)
}
