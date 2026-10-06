package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// The game lists replays from index files, not from the replay files
// themselves. Each index holds one 125-byte entry per slot: a used flag, the
// replay's CRC-32, its size, a Unix time, then the details the menus show
// (date, fighters and so on).
//
//   - replays-swan.dat indexes the recent-match ring: entry i is slot 300+i.
//     It starts with a CRC-32 of everything after its first 4 bytes.
//   - LIST indexes the hand-saved slots 0-299 after a 12-byte header.
//
// Both also have the usual N.0 file holding the CRC-32 of the whole file.
const (
	entrySize     = 125
	ringIndexName = "replays-swan.dat"
	ringIndexHead = 0x2C
	savedIndex    = "LIST"
	savedIndexOff = 12
)

var errIndexFormat = errors.New("the game's replay index is not in the expected format")

type replayIndex struct {
	path     string
	data     []byte
	first    int // slot number of entry 0
	offset   int // byte offset of entry 0
	count    int
	innerCRC bool // replays-swan.dat keeps a CRC of its body at offset 0
}

func readRingIndex(dir string) (*replayIndex, error) {
	idx, err := readIndex(filepath.Join(dir, ringIndexName), ringFirst, ringIndexHead, true)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(idx.data[8:12], []byte("SRL\x00")) || !bytes.Equal(idx.data[0x1C:0x20], []byte("SRI\x00")) {
		return nil, errIndexFormat
	}
	if idx.count < ringLast-ringFirst+1 {
		return nil, errIndexFormat
	}
	return idx, nil
}

func readSavedIndex(dir string) (*replayIndex, error) {
	return readIndex(filepath.Join(dir, savedIndex), savedFirst, savedIndexOff, false)
}

func readIndex(path string, first, offset int, innerCRC bool) (*replayIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum, err := os.ReadFile(path + ".0")
	if err != nil {
		return nil, err
	}
	if len(sum) != 4 || binary.LittleEndian.Uint32(sum) != crc32.ChecksumIEEE(data) {
		return nil, errChecksum
	}
	if len(data) < offset+entrySize {
		return nil, errIndexFormat
	}
	if innerCRC && binary.LittleEndian.Uint32(data) != crc32.ChecksumIEEE(data[4:]) {
		return nil, errChecksum
	}
	count := (len(data) - offset) / entrySize
	return &replayIndex{path: path, data: data, first: first, offset: offset, count: count, innerCRC: innerCRC}, nil
}

// raw returns slot n's entry when it is in use, whatever replay it names.
func (idx *replayIndex) raw(n int) []byte {
	i := n - idx.first
	if i < 0 || i >= idx.count {
		return nil
	}
	e := idx.data[idx.offset+i*entrySize : idx.offset+(i+1)*entrySize]
	if e[0] != 1 {
		return nil
	}
	return append([]byte(nil), e...)
}

// entry returns slot n's entry when it describes the replay with the given
// CRC, or nil.
func (idx *replayIndex) entry(n int, crc uint32) []byte {
	if e := idx.raw(n); e != nil && binary.LittleEndian.Uint32(e[1:]) == crc {
		return e
	}
	return nil
}

// put replaces slot n's entry and writes the index back with fresh checksums.
// An entry as kept here starts at the used flag; the game's record starts
// 4 bytes earlier with the slot number, so the last 4 bytes of a kept entry
// are the following slot's number and must not be written over it.
func (idx *replayIndex) put(n int, e []byte) error {
	i := n - idx.first
	if i < 0 || i >= idx.count || len(e) != entrySize {
		return fmt.Errorf("slot %d has no place in %s", n, filepath.Base(idx.path))
	}
	off := idx.offset + i*entrySize
	binary.LittleEndian.PutUint32(idx.data[off-4:], uint32(n))
	copy(idx.data[off:], e[:entrySize-4])
	if idx.innerCRC {
		binary.LittleEndian.PutUint32(idx.data, crc32.ChecksumIEEE(idx.data[4:]))
	}
	if err := writeReplaced(idx.path, idx.data); err != nil {
		return err
	}
	sum := make([]byte, 4)
	binary.LittleEndian.PutUint32(sum, crc32.ChecksumIEEE(idx.data))
	return writeReplaced(idx.path+".0", sum)
}

// indexFor returns the index that covers slot n.
func indexFor(dir string, n int) (*replayIndex, error) {
	if n >= ringFirst {
		return readRingIndex(dir)
	}
	return readSavedIndex(dir)
}

func validEntry(e []byte, crc uint32, size int) bool {
	return len(e) == entrySize && e[0] == 1 &&
		binary.LittleEndian.Uint32(e[1:]) == crc &&
		binary.LittleEndian.Uint32(e[5:]) == uint32(size)
}
