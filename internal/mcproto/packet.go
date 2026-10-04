package mcproto

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"io"
	"net"
	"sync"
)

const (
	// MaxPacketLen matches the vanilla client's 2 MiB packet ceiling.
	MaxPacketLen = 1 << 21
	// NoCompression disables the compression layer.
	NoCompression = -1
)

// Packet is one framed protocol packet: a VarInt id plus the raw payload that
// follows it.
type Packet struct {
	ID   int32
	Data []byte
}

// Conn wraps a TCP connection with Minecraft packet framing. It is not safe
// for concurrent use; drive reads and writes from a single goroutine.
type Conn struct {
	nc          net.Conn
	r           *bufio.Reader
	w           *bufio.Writer
	mu          sync.Mutex
	compression int
}

func NewConn(nc net.Conn) *Conn {
	return &Conn{
		nc:          nc,
		r:           bufio.NewReaderSize(nc, 4096),
		w:           bufio.NewWriterSize(nc, 4096),
		compression: NoCompression,
	}
}

// SetCompression sets the threshold at which payloads are zlib-compressed.
// Values below 0 disable compression.
func (c *Conn) SetCompression(threshold int) { c.compression = threshold }

func (c *Conn) ReadPacket() (*Packet, error) {
	length, err := ReadVarInt(c.r)
	if err != nil {
		return nil, err
	}
	if length < 0 || int(length) > MaxPacketLen {
		return nil, errPacketTooLarge
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, err
	}

	payload := body
	if c.compression >= 0 {
		r := NewReader(body)
		dataLen, err := r.ReadVarInt()
		if err != nil {
			return nil, err
		}
		if dataLen == 0 {
			payload = body[r.pos:]
		} else {
			if int(dataLen) != r.Remaining() {
				return nil, errPacketTooLarge
			}
			zr, err := zlib.NewReader(bytes.NewReader(body[r.pos:]))
			if err != nil {
				return nil, err
			}
			payload, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				return nil, err
			}
		}
	}

	r := NewReader(payload)
	id, err := r.ReadVarInt()
	if err != nil {
		return nil, err
	}
	return &Packet{ID: id, Data: payload[r.pos:]}, nil
}

func (c *Conn) WritePacket(id int32, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var inner Writer
	inner.WriteVarInt(id)
	inner.WriteBytes(data)
	payload := inner.Bytes()

	var out Writer
	if c.compression >= 0 {
		if len(payload) >= c.compression {
			out.WriteVarInt(int32(len(payload)))
			zw := zlib.NewWriter(&out.Buffer)
			zw.Write(payload)
			zw.Close()
		} else {
			out.WriteVarInt(0)
			out.WriteBytes(payload)
		}
	} else {
		out.WriteBytes(payload)
	}

	var frame Writer
	frame.WriteVarInt(int32(out.Len()))
	frame.WriteBytes(out.Bytes())
	if _, err := c.w.Write(frame.Bytes()); err != nil {
		return err
	}
	return c.w.Flush()
}
