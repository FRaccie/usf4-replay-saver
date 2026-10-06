package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var (
	errGameRunning = errors.New("close Street Fighter IV first; the game rewrites its save files while it runs")
	errNoEntry     = errors.New("this replay was saved without the game's list details, so the game cannot show it; replays saved from now on can be put back")
)

// restore puts an archived replay back into the game's recent-match ring the
// way the game writes one after a match: the replay file, its checksum, and
// its entry in the ring index the menus read. It replaces the ring slot with
// the oldest match, after archiving that one. The game must be closed. It
// returns the slot written, or -1 when the replay was already there.
func restore(file string, dir string, a *archive) (int, error) {
	if running, err := gameRunning(); err == nil && running {
		return 0, errGameRunning
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	if err := validateReplay(data); err != nil {
		return 0, err
	}
	crc := crc32.ChecksumIEEE(data)
	entry := a.loadEntry(crc)
	if !validEntry(entry, crc, len(data)) {
		return 0, errNoEntry
	}
	target, ring, err := ringTarget(dir, crc)
	if err != nil {
		return 0, fmt.Errorf("the game's list of recent replays could not be read: %w", err)
	}
	if target < 0 {
		return -1, nil
	}
	if s, err := readSlot(dir, target); err == nil {
		if _, _, err := a.save(s); err != nil {
			return 0, fmt.Errorf("could not save slot %d before replacing it: %w", target, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) && err != errNotReplay && err != errChecksum {
		return 0, fmt.Errorf("slot %d could not be read, so it was left alone: %w", target, err)
	}
	if _, err := a.keepEntry(ring.raw(target)); err != nil {
		return 0, fmt.Errorf("could not save slot %d's list details before replacing them: %w", target, err)
	}
	redate(entry, time.Now())

	path := filepath.Join(dir, strconv.Itoa(target))
	if err := writeReplaced(path, data); err != nil {
		return 0, err
	}
	sum := make([]byte, 4)
	binary.LittleEndian.PutUint32(sum, crc)
	if err := writeReplaced(path+".0", sum); err != nil {
		return 0, err
	}
	if err := ring.put(target, entry); err != nil {
		return 0, fmt.Errorf("the replay was written but the game's list was not updated: %w", err)
	}
	return target, nil
}

// ringTarget picks the match slot to write, with the index that covers it,
// the way the game picks one after a match: an empty or inconsistent slot
// first, then the slot whose entry has the oldest time. It returns -1 when the
// replay and its entry are already in place. A slot holding the replay without
// a matching entry (left by an older version of this app) is repaired in place.
func ringTarget(dir string, crc uint32) (int, *replayIndex, error) {
	ring, err := readRingIndex(dir)
	if err != nil {
		return 0, nil, err
	}
	saved, _ := readSavedIndex(dir)
	best, bestTime, bestEmpty := -1, uint32(0), false
	var bestIndex *replayIndex
	for n := matchFirst; n <= ringLast; n++ {
		idx := ring
		if n < ringFirst {
			idx = saved
		}
		if idx == nil {
			continue
		}
		s, err := readSlot(dir, n)
		var e []byte
		if err == nil {
			e = idx.entry(n, s.crc)
		}
		if err == nil && s.crc == crc {
			if e != nil {
				return -1, idx, nil
			}
			return n, idx, nil
		}
		if err != nil || e == nil {
			if !bestEmpty {
				best, bestIndex, bestEmpty = n, idx, true
			}
			continue
		}
		if bestEmpty {
			continue
		}
		if t := binary.LittleEndian.Uint32(e[9:]); best < 0 || t < bestTime {
			best, bestIndex, bestTime = n, idx, t
		}
	}
	return best, bestIndex, nil
}

func writeReplaced(path string, data []byte) error {
	tmp := path + ".restore-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
