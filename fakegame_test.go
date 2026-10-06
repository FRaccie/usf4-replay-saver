package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeGame writes save folders the way USF4 does after a match: the replay,
// its checksum file, and its entry in replays-swan.dat.
type fakeGame struct {
	t   *testing.T
	dir string
}

func newFakeGame(t *testing.T) *fakeGame {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "SSF4_SaveData")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Header as the game writes it, then 300 empty entries.
	head := make([]byte, ringIndexHead)
	binary.LittleEndian.PutUint32(head[4:], 4)
	copy(head[8:], "SRL\x00")
	binary.LittleEndian.PutUint32(head[0x18:], 4)
	copy(head[0x1C:], "SRI\x00")
	binary.LittleEndian.PutUint32(head[0x28:], 300)
	data := append(head, make([]byte, 300*entrySize)...)
	binary.LittleEndian.PutUint32(data[12:], uint32(len(data)))
	g := &fakeGame{t: t, dir: dir}
	g.writeIndex(data)
	return g
}

func (g *fakeGame) writeIndex(data []byte) {
	binary.LittleEndian.PutUint32(data, crc32.ChecksumIEEE(data[4:]))
	path := filepath.Join(g.dir, ringIndexName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		g.t.Fatal(err)
	}
	writeSum(g.t, path, data)
}

func (g *fakeGame) index() []byte {
	data, err := os.ReadFile(filepath.Join(g.dir, ringIndexName))
	if err != nil {
		g.t.Fatal(err)
	}
	return data
}

// match records a match into ring slot n.
func (g *fakeGame) match(n int, stamp time.Time, fill byte) []byte {
	g.t.Helper()
	data := make([]byte, 0x80+int(fill))
	copy(data, replayMagic)
	binary.LittleEndian.PutUint64(data[headerTimeOff:], uint64(stamp.UnixNano()/100)+116444736000000000)
	for i := 0x40; i < len(data); i++ {
		data[i] = fill
	}
	path := filepath.Join(g.dir, strconv.Itoa(n))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		g.t.Fatal(err)
	}
	writeSum(g.t, path, data)

	e := make([]byte, entrySize)
	e[0] = 1
	binary.LittleEndian.PutUint32(e[1:], crc32.ChecksumIEEE(data))
	binary.LittleEndian.PutUint32(e[5:], uint32(len(data)))
	binary.LittleEndian.PutUint32(e[9:], uint32(stamp.Unix()))
	// The menu's fields: the date and time, and the two fighters (Ryu
	// against whoever fill says).
	utc := stamp.UTC()
	binary.LittleEndian.PutUint16(e[entryYear:], uint16(utc.Year()))
	e[entryMonth], e[entryDay] = byte(utc.Month()), byte(utc.Day())
	e[entryHour], e[entryMinute] = byte(utc.Hour()), byte(utc.Minute())
	e[entryPlayer1+entryFighter] = 0
	e[entryPlayer1+entryPlayerSize+entryFighter] = fill % byte(len(fighterNames))
	idx := g.index()
	off := ringIndexHead + (n-ringFirst)*entrySize
	binary.LittleEndian.PutUint32(idx[off-4:], uint32(n))
	copy(idx[off:], e)
	g.writeIndex(idx)
	return data
}

func (g *fakeGame) entry(n int) []byte {
	idx := g.index()
	off := ringIndexHead + (n-ringFirst)*entrySize
	return idx[off : off+entrySize]
}

// undated copies data with the fields a restore re-dates in the entry at
// off (save time, title, date, hour and minute) cleared, the entry's last
// 4 bytes (the next slot's number, which a put leaves alone) with them, and
// the index's leading CRC.
func undated(data []byte, off int) []byte {
	out := append([]byte(nil), data...)
	for _, r := range [][2]int{{9, 17}, {entryTitleCount, entryTitle + entryTitleLen}, {entryYear, entryPlayer1}, {entryHour, entryMinute + 1}, {entrySize - 4, entrySize}} {
		for i := off + r[0]; i < off+r[1] && i < len(out); i++ {
			out[i] = 0
		}
	}
	if off > 0 && len(out) >= 4 {
		copy(out, []byte{0, 0, 0, 0})
	}
	return out
}

func writeSum(t *testing.T, path string, data []byte) {
	t.Helper()
	sum := make([]byte, 4)
	binary.LittleEndian.PutUint32(sum, crc32.ChecksumIEEE(data))
	if err := os.WriteFile(path+".0", sum, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanKeepsIndexEntries(t *testing.T) {
	g := newFakeGame(t)
	base := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		g.match(ringFirst+i, base.Add(time.Duration(i)*time.Minute), byte(i+1))
	}
	a, err := openArchive(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.scan([]string{g.dir}, ringFirst, ringLast)
	if err != nil || len(r.saved) != 10 || r.entries != 10 {
		t.Fatalf("saved %d entries %d err %v", len(r.saved), r.entries, err)
	}
	s, _ := readSlot(g.dir, ringFirst+3)
	if !bytes.Equal(a.loadEntry(s.crc), g.entry(ringFirst+3)) {
		t.Fatal("kept entry differs from the game's")
	}
	if r, _ := a.scan([]string{g.dir}, ringFirst, ringLast); len(r.saved) != 0 || r.entries != 0 {
		t.Fatalf("second scan saved %d entries %d", len(r.saved), r.entries)
	}
}

func TestScanWaitsForTheIndexAndBackfills(t *testing.T) {
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	data := g.match(ringFirst, time.Now(), 5)
	// The replay is written but the index still describes the old match.
	idx := g.index()
	copy(idx[ringIndexHead:], make([]byte, entrySize))
	g.writeIndex(idx)
	r, _ := a.scan([]string{g.dir}, ringFirst, ringLast)
	if len(r.saved) != 1 || r.entries != 0 {
		t.Fatalf("saved %d entries %d", len(r.saved), r.entries)
	}
	g.match(ringFirst, headerTime(data), 5)
	if r, _ := a.scan([]string{g.dir}, ringFirst, ringLast); r.entries != 1 {
		t.Fatalf("entry not backfilled: %d", r.entries)
	}
}

func TestRestorePutsReplayAndEntryBack(t *testing.T) {
	if running, _ := gameRunning(); running {
		t.Skip("SSFIV.exe is running")
	}
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	base := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		g.match(ringFirst+i, base.Add(time.Duration(i)*time.Minute), byte(i+1))
	}
	a.scan([]string{g.dir}, ringFirst, ringLast)
	oldReplay, _ := readSlot(g.dir, ringFirst)
	oldEntry := append([]byte(nil), g.entry(ringFirst)...)

	// The next match lands in slot 300, so the first match only survives in
	// the archive. Slot 301 now holds the oldest match.
	g.match(ringFirst, base.Add(time.Hour), 0x40)
	a.scan([]string{g.dir}, ringFirst, ringLast)
	before301, _ := readSlot(g.dir, ringFirst+1)

	file := filepath.Join(a.dir, a.known[oldReplay.crc])
	slot, err := restore(file, g.dir, a)
	if err != nil || slot != ringFirst+1 {
		t.Fatalf("restore slot %d err %v", slot, err)
	}
	got, err := readSlot(g.dir, ringFirst+1)
	if err != nil || got.crc != oldReplay.crc {
		t.Fatalf("slot 301 crc %v err %v", got, err)
	}
	if !bytes.Equal(undated(g.entry(ringFirst+1), 0), undated(oldEntry, 0)) {
		t.Fatal("slot 301's entry is not the restored replay's")
	}
	if _, err := readRingIndex(g.dir); err != nil {
		t.Fatalf("index checksums broken: %v", err)
	}
	if !a.hasEntry(before301.crc) {
		t.Fatal("the replaced match lost its entry")
	}
	if slot, err := restore(file, g.dir, a); err != nil || slot != -1 {
		t.Fatalf("second restore slot %d err %v", slot, err)
	}
}

func TestEntryDetails(t *testing.T) {
	g := newFakeGame(t)
	stamp := time.Date(2026, 10, 5, 21, 35, 0, 0, time.UTC)
	g.match(ringFirst, stamp, 33)
	d := details(g.entry(ringFirst))
	if d.matchup() != "Ryu vs Hakan" || !d.Played.Equal(stamp) {
		t.Fatalf("details %+v", d)
	}
	e := g.entry(ringFirst)
	e[entryPlayer1+entryFighter] = 200
	if details(e).Fighters[0] != "fighter 200" {
		t.Fatal("an unknown fighter id should still read")
	}
}

func TestRestoreRedatesAndKeepsNeighbourSlotNumbers(t *testing.T) {
	if running, _ := gameRunning(); running {
		t.Skip("SSFIV.exe is running")
	}
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	base := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		g.match(ringFirst+i, base.Add(time.Duration(i)*time.Minute), byte(i+1))
	}
	a.scan([]string{g.dir}, ringFirst, ringLast)
	old, _ := readSlot(g.dir, ringFirst+7)
	g.match(ringFirst+7, base.Add(time.Hour), 0x40) // pushes match 7 out
	a.scan([]string{g.dir}, ringFirst, ringLast)

	before := time.Now()
	slot, err := restore(filepath.Join(a.dir, a.known[old.crc]), g.dir, a)
	if err != nil || slot != ringFirst {
		t.Fatalf("restore slot %d err %v", slot, err)
	}
	e := g.entry(ringFirst)
	d := details(e)
	if saved := time.Unix(int64(binary.LittleEndian.Uint32(e[9:])), 0); saved.Before(before.Truncate(time.Second)) {
		t.Fatalf("save time not now: %v", saved)
	}
	if d.Played.Before(before.UTC().Truncate(time.Minute)) {
		t.Fatalf("shown date not now: %v", d.Played)
	}
	if string(bytes.TrimRight(e[entryTitle:entryTitle+entryTitleLen], "\x00")) != base.Add(7*time.Minute).Format("2006-01-02 15:04") {
		t.Fatalf("title %q", e[entryTitle:entryTitle+entryTitleLen])
	}
	if d.matchup() != "Ryu vs Balrog" {
		t.Fatalf("fighters lost: %s", d.matchup())
	}
	// The kept entry ends with the number of the slot after the one it came
	// from (308); slot 301's own number must still be 301.
	idx := g.index()
	off := ringIndexHead + (ringFirst+1-ringFirst)*entrySize
	if n := binary.LittleEndian.Uint32(idx[off-4:]); n != ringFirst+1 {
		t.Fatalf("slot 301's number became %d", n)
	}
	if n := binary.LittleEndian.Uint32(idx[ringIndexHead-4:]); n != ringFirst {
		t.Fatalf("slot 300's number became %d", n)
	}
}

func TestRestoreRepairsAReplayWrittenWithoutItsEntry(t *testing.T) {
	if running, _ := gameRunning(); running {
		t.Skip("SSFIV.exe is running")
	}
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	base := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		g.match(ringFirst+i, base.Add(time.Duration(i)*time.Minute), byte(i+1))
	}
	a.scan([]string{g.dir}, ringFirst, ringLast)
	five, _ := readSlot(g.dir, ringFirst+5)
	fiveEntry := append([]byte(nil), g.entry(ringFirst+5)...)
	// An older version copied replay 5 over slot 2 without touching the index.
	os.WriteFile(filepath.Join(g.dir, "302"), five.data, 0o644)
	writeSum(t, filepath.Join(g.dir, "302"), five.data)

	slot, err := restore(filepath.Join(a.dir, a.known[five.crc]), g.dir, a)
	if err != nil {
		t.Fatal(err)
	}
	if slot != ringFirst+2 || !bytes.Equal(undated(g.entry(ringFirst+2), 0), undated(fiveEntry, 0)) {
		t.Fatalf("slot %d not repaired", slot)
	}
}

// After 0.2.0 replaced only a slot's file, the slot holds replay X while its
// entry still names replay Y. Y's entry must be kept, and survive a restore
// into that slot.
func TestOrphanedEntryIsKept(t *testing.T) {
	if running, _ := gameRunning(); running {
		t.Skip("SSFIV.exe is running")
	}
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	base := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		g.match(ringFirst+i, base.Add(time.Duration(i)*time.Minute), byte(i+1))
	}
	y, _ := readSlot(g.dir, ringFirst+2)
	yEntry := append([]byte(nil), g.entry(ringFirst+2)...)
	// Archive Y's file only, as 0.2.0 did, with no entry.
	a.save(y)
	// 0.2.0 then put an older replay X into slot 302 without the index.
	x := g.match(ringFirst+9, base.Add(-time.Hour), 0x30) // records X and its entry
	os.WriteFile(filepath.Join(g.dir, "302"), x, 0o644)
	writeSum(t, filepath.Join(g.dir, "302"), x)

	if r, err := a.scan([]string{g.dir}, ringFirst, ringLast); err != nil || !a.hasEntry(y.crc) {
		t.Fatalf("Y's entry not kept (entries %d, err %v)", r.entries, err)
	}
	if !bytes.Equal(a.loadEntry(y.crc), yEntry) {
		t.Fatal("kept entry for Y differs")
	}
	// Restoring Y repairs slot 302 with Y's own entry.
	slot, err := restore(filepath.Join(a.dir, a.known[y.crc]), g.dir, a)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := readSlot(g.dir, slot)
	if got.crc != y.crc || !bytes.Equal(undated(g.entry(slot), 0), undated(yEntry, 0)) {
		t.Fatalf("slot %d not restored to Y", slot)
	}
	if _, err := readRingIndex(g.dir); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesReplaysWithoutEntries(t *testing.T) {
	g := newFakeGame(t)
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	data := g.match(ringFirst, time.Now(), 3)
	a.save(&slot{data: data, crc: crc32.ChecksumIEEE(data), written: time.Now()})
	file := filepath.Join(a.dir, a.known[crc32.ChecksumIEEE(data)])
	if _, err := restore(file, g.dir, a); err != errNoEntry && err != errGameRunning {
		t.Fatalf("err %v, want errNoEntry", err)
	}
}

// TestRealSaveRoundTrip runs against a copy of a real save folder named by
// USF4_SAVE_COPY. It records the ring, lets a fake match replace one slot,
// restores the lost replay and expects the index to match the original
// byte for byte.
func TestRealSaveRoundTrip(t *testing.T) {
	dir := os.Getenv("USF4_SAVE_COPY")
	if dir == "" {
		t.Skip("set USF4_SAVE_COPY to a copy of an SSF4_SaveData folder")
	}
	if running, _ := gameRunning(); running {
		t.Skip("SSFIV.exe is running")
	}
	original, err := os.ReadFile(filepath.Join(dir, ringIndexName))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := openArchive(filepath.Join(t.TempDir(), "out"))
	if r, err := a.scan([]string{dir}, ringFirst, ringLast); err != nil || r.entries != 10 {
		t.Fatalf("entries %d err %v", r.entries, err)
	}
	// A fake match overwrites the oldest slot. Its time is older still, so
	// that slot stays the one a restore replaces, which lets the result be
	// compared with the original, apart from the fields a restore re-dates.
	target, _, _ := ringTarget(dir, 0)
	lost, _ := readSlot(dir, target)
	g := &fakeGame{t: t, dir: dir}
	g.match(target, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), 9)

	slot, err := restore(filepath.Join(a.dir, a.known[lost.crc]), dir, a)
	if err != nil || slot != target {
		t.Fatalf("restore slot %d (want %d) err %v", slot, target, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ringIndexName))
	off := ringIndexHead + (target-ringFirst)*entrySize
	if !bytes.Equal(undated(after, off), undated(original, off)) {
		t.Fatal("index differs from the original after the round trip")
	}
	back, _ := readSlot(dir, target)
	if back.crc != lost.crc {
		t.Fatal("replay differs after the round trip")
	}
}
