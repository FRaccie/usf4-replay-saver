package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// USF4 keeps its saves under Steam Cloud as numbered files. Slots 0-299 are
// the replays a player saved by hand (indexed by the LIST file). Slots
// 300-309 are a ring the game cycles through, rewriting one after every
// match. Each slot N has a sidecar N.0 holding the CRC-32 of N, little endian.
const (
	appID     = "45760"
	ringFirst = 300
	ringLast  = 309
	// With Ember Netplay the game's match list is slots 280 to 309; the
	// stock game uses 300 to 309 and leaves 280 to 299 to replays the
	// native online service handed out, which are worth keeping too.
	matchFirst    = 280
	savedFirst    = 0
	savedLast     = 299
	replayMagic   = "#BRP"
	headerTimeOff = 0x10
	minReplaySize = 0x40
)

var saveSubdir = filepath.Join("remote", "CAPCOM", "SUPERSTREETFIGHTERIV", "SSF4_SaveData")

type slot struct {
	dir     string
	number  int
	data    []byte
	crc     uint32
	written time.Time // the game's own timestamp from the replay header
}

// findSaveDirs returns every SSF4_SaveData folder under the Steam roots,
// one per Steam account that has played the game.
func findSaveDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	for _, root := range steamRoots() {
		accounts, _ := filepath.Glob(filepath.Join(root, "userdata", "*"))
		for _, account := range accounts {
			dir := filepath.Join(account, appID, saveSubdir)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			key := dir
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				key = resolved
			}
			if !seen[key] {
				seen[key] = true
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

var errNotReplay = errors.New("not a replay")
var errChecksum = errors.New("checksum does not match (the game may still be writing it)")

// readSlot reads slot N and checks it against N.0. A slot that is empty,
// a placeholder, or mid-write returns an error.
func readSlot(dir string, number int) (*slot, error) {
	path := filepath.Join(dir, strconv.Itoa(number))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < minReplaySize || !bytes.HasPrefix(data, []byte(replayMagic)) {
		return nil, errNotReplay
	}
	sum, err := os.ReadFile(path + ".0")
	if err != nil {
		return nil, err
	}
	if len(sum) != 4 {
		return nil, errChecksum
	}
	crc := crc32.ChecksumIEEE(data)
	if binary.LittleEndian.Uint32(sum) != crc {
		return nil, errChecksum
	}
	return &slot{dir: dir, number: number, data: data, crc: crc, written: headerTime(data)}, nil
}

// headerTime decodes the FILETIME the game stamps at offset 0x10.
func headerTime(data []byte) time.Time {
	if len(data) < headerTimeOff+8 {
		return time.Time{}
	}
	ft := binary.LittleEndian.Uint64(data[headerTimeOff:])
	if ft == 0 {
		return time.Time{}
	}
	const epochDiff = 116444736000000000 // 1601-01-01 to 1970-01-01 in 100 ns units
	if ft < epochDiff {
		return time.Time{}
	}
	return time.Unix(0, int64(ft-epochDiff)*100)
}

func validateReplay(data []byte) error {
	if len(data) < minReplaySize || !bytes.HasPrefix(data, []byte(replayMagic)) {
		return fmt.Errorf("%w: missing the %q header", errNotReplay, replayMagic)
	}
	return nil
}
