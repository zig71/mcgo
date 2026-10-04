package v777

import (
	"bytes"
	"encoding/binary"
	"sort"
	"testing"

	"mcgo/internal/nbt"
)

// TestRegistryNBTAlignsWithEntries walks the embedded NBT blob the way the
// server does when it sends registry data, and parses every entry.
//
// The blob and RegistryEntries are produced by two different generators. If
// they disagree — a registry missing from one, an entry added to the other —
// the server reads lengths from the wrong offsets and sends clients garbage
// that parses as *something*, so the failure surfaces as "Failed to parse
// <element> from pack vanilla" on a real client rather than here.
func TestRegistryNBTAlignsWithEntries(t *testing.T) {
	regs := make([]string, 0, len(RegistryEntries))
	for r := range RegistryEntries {
		regs = append(regs, r)
	}
	sort.Strings(regs)

	off, entries := 0, 0
	for _, reg := range regs {
		for _, name := range RegistryEntries[reg] {
			if off+4 > len(RegistryNBT) {
				t.Fatalf("%s/%s: blob ends at %d, no length prefix", reg, name, off)
			}
			n := int(binary.BigEndian.Uint32(RegistryNBT[off:]))
			if n <= 0 || off+4+n > len(RegistryNBT) {
				t.Fatalf("%s/%s: entry claims %d bytes at %d, blob is %d",
					reg, name, n, off+4, len(RegistryNBT))
			}
			// The blob holds anonymous tags (wire form: type byte then
			// payload). The reader wants a named root, so splice in an empty
			// name rather than misreading the payload as one.
			raw := RegistryNBT[off+4 : off+4+n]
			named := append([]byte{raw[0], 0, 0}, raw[1:]...)
			if _, _, err := nbt.Read(bytes.NewReader(named)); err != nil {
				t.Fatalf("%s/%s: entry does not parse: %v", reg, name, err)
			}
			off += 4 + n
			entries++
		}
	}
	if off != len(RegistryNBT) {
		t.Errorf("consumed %d of %d blob bytes: %d left over", off, len(RegistryNBT), len(RegistryNBT)-off)
	}
	t.Logf("%d registries, %d entries, %d blob bytes", len(regs), entries, len(RegistryNBT))
}
