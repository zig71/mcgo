package world

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Region is one Anvil region file: a 32x32 grid of chunks, addressed by
// (chunk & 31) on each axis.
type Region struct {
	f *os.File
}

// RegionPath returns the file a chunk lives in, relative to a world directory.
// Region coordinates are the chunk coordinates shifted right by 5.
func RegionPath(dir string, cx, cz int32) string {
	return filepath.Join(dir, "region", fmt.Sprintf("r.%d.%d.mca", cx>>5, cz>>5))
}

func OpenRegion(path string) (*Region, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &Region{f: f}, nil
}

func (r *Region) Close() error { return r.f.Close() }

// Chunk returns the decompressed NBT bytes for a chunk. The bool is false when
// the region simply has no entry for it, which is not an error.
func (r *Region) Chunk(cx, cz int32) ([]byte, bool, error) {
	idx := int((cx&31)+(cz&31)*32) * 4
	header := make([]byte, 4)
	if _, err := r.f.ReadAt(header, int64(idx)); err != nil {
		if err == io.EOF {
			return nil, false, nil // region file shorter than the index implies
		}
		return nil, false, err
	}
	offset := int64(header[0])<<16 | int64(header[1])<<8 | int64(header[2])
	if offset == 0 || header[3] == 0 {
		return nil, false, nil // not generated
	}

	// A chunk sits at offset*4096: 4-byte length, 1-byte compression, payload.
	head := make([]byte, 5)
	if _, err := r.f.ReadAt(head, offset*4096); err != nil {
		return nil, false, fmt.Errorf("region: chunk header at sector %d: %w", offset, err)
	}
	length := int(binary.BigEndian.Uint32(head[:4]))
	if length < 1 {
		return nil, false, fmt.Errorf("region: bad chunk length %d", length)
	}
	comp := head[4]
	data := make([]byte, length-1)
	if _, err := r.f.ReadAt(data, offset*4096+5); err != nil {
		return nil, false, fmt.Errorf("region: chunk body: %w", err)
	}

	switch comp {
	case 1: // gzip
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false, err
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		return out, true, err
	case 2: // zlib
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false, err
		}
		defer zr.Close()
		out, err := io.ReadAll(zr)
		return out, true, err
	case 3: // uncompressed
		return data, true, nil
	default:
		return nil, false, fmt.Errorf("region: unsupported compression %d", comp)
	}
}
