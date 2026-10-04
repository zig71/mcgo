// mcdump connects to a server, walks login + configuration, and prints every
// packet it receives so two servers can be compared.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"
)

const coreVersion = "26.3"

var cbNames = map[string]map[int32]string{
	"login": {0: "disconnect", 1: "hello", 2: "login_finished", 3: "login_compression", 4: "custom_query", 5: "cookie_request"},
	"config": {
		0: "cookie_request", 1: "custom_payload", 2: "disconnect", 3: "finish_configuration",
		4: "keep_alive", 5: "ping", 6: "reset_chat", 7: "registry_data", 8: "resource_pack_pop",
		9: "resource_pack_push", 10: "post_effects", 11: "store_cookie", 12: "transfer",
		13: "update_enabled_features", 14: "update_tags", 15: "select_known_packs",
		16: "custom_report_details", 17: "server_links", 18: "clear_dialog", 19: "show_dialog",
		20: "code_of_conduct",
	},
}

func writeVarInt(b *bytes.Buffer, v int32) {
	u := uint32(v)
	for {
		if u&^0x7F == 0 {
			b.WriteByte(byte(u))
			return
		}
		b.WriteByte(byte(u&0x7F | 0x80))
		u >>= 7
	}
}

func readVarInt(r io.ByteReader) (int32, error) {
	var res uint32
	for i := range 5 {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		res |= uint32(c&0x7F) << (7 * uint(i))
		if c&0x80 == 0 {
			return int32(res), nil
		}
	}
	return 0, fmt.Errorf("varint too big")
}

func writeString(b *bytes.Buffer, s string) {
	writeVarInt(b, int32(len(s)))
	b.WriteString(s)
}

func readString(r *bytes.Reader) (string, error) {
	n, err := readVarInt(r)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func frame(id int32, body []byte) []byte {
	var inner bytes.Buffer
	writeVarInt(&inner, id)
	inner.Write(body)
	var out bytes.Buffer
	writeVarInt(&out, int32(inner.Len()))
	out.Write(inner.Bytes())
	return out.Bytes()
}

func readPacket(br *bufio.Reader) (int32, []byte, error) {
	n, err := readVarInt(br)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return 0, nil, err
	}
	r := bytes.NewReader(body)
	id, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	rest, _ := io.ReadAll(r)
	return id, rest, nil
}

func main() {
	addr := os.Args[1]
	port := 25565
	if len(os.Args) > 2 {
		port, _ = strconv.Atoi(os.Args[2])
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)

	var h bytes.Buffer
	writeVarInt(&h, 777)
	writeString(&h, "localhost")
	binary.Write(&h, binary.BigEndian, uint16(port))
	writeVarInt(&h, 2)
	conn.Write(frame(0x00, h.Bytes()))

	var hello bytes.Buffer
	writeString(&hello, "dumper")
	hello.Write(make([]byte, 16))
	conn.Write(frame(0x00, hello.Bytes()))

	state := "login"
	for {
		id, data, err := readPacket(br)
		if err != nil {
			fmt.Printf("%s: read failed: %v\n", state, err)
			return
		}
		name := cbNames[state][id]
		fmt.Printf("%-7s <- id=%-3d len=%-7d %s\n", state, id, len(data), name)

		switch state {
		case "login":
			switch id {
			case 2: // login_finished
				conn.Write(frame(0x03, nil)) // login acknowledged
				state = "config"
			case 3:
				fmt.Println("!! server requested compression; aborting")
				return
			case 0:
				return
			}
		case "config":
			switch id {
			case 7: // registry_data
				r := bytes.NewReader(data)
				reg, err := readString(r)
				if err != nil {
					break
				}
				n, _ := readVarInt(r)
				fmt.Printf("        registry %s entries=%d\n", reg, n)
				for range min(n, 6) {
					e, err := readString(r)
					if err != nil {
						break
					}
					hasNBT, _ := r.ReadByte()
					fmt.Printf("          %s nbt=%d\n", e, hasNBT)
					if hasNBT == 1 {
						// cannot skip NBT portably; stop listing this registry
						fmt.Printf("          (… NBT present, stopping listing)\n")
						break
					}
				}
			case 14: // update_tags
				r := bytes.NewReader(data)
				n, _ := readVarInt(r)
				fmt.Printf("        tags for %d registries\n", n)
				for range n {
					reg, err := readString(r)
					if err != nil {
						break
					}
					tn, err := readVarInt(r)
					if err != nil {
						break
					}
					fmt.Printf("          %s tags=%d\n", reg, tn)
					ok := true
					for range tn {
						if !ok {
							break
						}
						if _, err := readString(r); err != nil {
							ok = false
							break
						}
						cnt, err := readVarInt(r)
						if err != nil {
							ok = false
							break
						}
						for range cnt {
							if _, err := readVarInt(r); err != nil {
								ok = false
								break
							}
						}
					}
				}
			case 13: // update_enabled_features
				r := bytes.NewReader(data)
				n, _ := readVarInt(r)
				for range n {
					f, _ := readString(r)
					fmt.Printf("        feature %s\n", f)
				}
			case 15: // select_known_packs
				r := bytes.NewReader(data)
				n, _ := readVarInt(r)
				for range n {
					ns, _ := readString(r)
					id, _ := readString(r)
					ver, _ := readString(r)
					fmt.Printf("        offers %s:%s %s\n", ns, id, ver)
				}
				var kp bytes.Buffer
				writeVarInt(&kp, 1)
				writeString(&kp, "minecraft")
				writeString(&kp, "core")
				writeString(&kp, coreVersion)
				conn.Write(frame(0x07, kp.Bytes()))
			case 4: // keep_alive
				conn.Write(frame(0x04, data))
			case 5: // ping
				conn.Write(frame(0x05, data))
			case 3: // finish_configuration
				conn.Write(frame(0x03, nil))
				fmt.Println("        ack sent; entering play")
				state = "play"
			case 20: // code_of_conduct
				fmt.Println("        (code of conduct; not acknowledging)")
			}
		case "play":
			return
		}
	}
}
