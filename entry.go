package main

import (
	"encoding/binary"
	"fmt"
	"time"
)

// An index entry, as this app keeps it, starts at the slot's used flag. The
// game's record starts 4 bytes earlier with the slot number; see put. After
// the flag, CRC, size and 64-bit time come 21 bytes of title (after their
// count) and the details the menus show: a header, the match's date, two
// 34-byte players with the fighter at +16, then the hour and minute, UTC.
const (
	entryTitleCount = 18
	entryTitle      = 22
	entryTitleLen   = 21
	entryYear       = 47
	entryMonth      = 49
	entryDay        = 50
	entryPlayer1    = 51
	entryPlayerSize = 34
	entryFighter    = 16
	entryHour       = 119
	entryMinute     = 120
)

// Native fighter order, the IDs the game stores.
var fighterNames = []string{"Ryu", "Ken", "Chun-Li", "E. Honda", "Blanka", "Zangief", "Guile", "Dhalsim", "Balrog", "Vega",
	"Sagat", "M. Bison", "C. Viper", "Rufus", "El Fuerte", "Abel", "Seth", "Akuma", "Gouken", "T. Hawk",
	"Cammy", "Fei Long", "Dee Jay", "Sakura", "Rose", "Gen", "Dan", "Guy", "Cody", "Ibuki",
	"Makoto", "Dudley", "Adon", "Hakan", "Juri", "Yun", "Yang", "Evil Ryu", "Oni", "Rolento",
	"Elena", "Poison", "Hugo", "Decapre"}

type entryDetails struct {
	Fighters [2]string
	Played   time.Time // UTC, to the minute
}

func fighterName(id byte) string {
	if int(id) < len(fighterNames) {
		return fighterNames[id]
	}
	return fmt.Sprintf("fighter %d", id)
}

// details reads what the game's menu shows for an entry.
func details(e []byte) entryDetails {
	var d entryDetails
	if len(e) != entrySize {
		return d
	}
	for side := range d.Fighters {
		d.Fighters[side] = fighterName(e[entryPlayer1+side*entryPlayerSize+entryFighter])
	}
	d.Played = time.Date(int(binary.LittleEndian.Uint16(e[entryYear:])), time.Month(e[entryMonth]), int(e[entryDay]),
		int(e[entryHour]), int(e[entryMinute]), 0, 0, time.UTC)
	return d
}

func (d entryDetails) matchup() string { return d.Fighters[0] + " vs " + d.Fighters[1] }

// redate makes an entry read as saved at `now`: the save time, which decides
// which slot the game replaces next, and the date and time the list shows and
// sorts by. The date it was played goes into the title. The list shows the
// newest 30 entries and no more, so a put-back replay with its own old date
// would often not be listed at all.
func redate(e []byte, now time.Time) {
	played := details(e).Played
	binary.LittleEndian.PutUint64(e[9:], uint64(now.Unix()))
	utc := now.UTC()
	binary.LittleEndian.PutUint16(e[entryYear:], uint16(utc.Year()))
	e[entryMonth], e[entryDay] = byte(utc.Month()), byte(utc.Day())
	e[entryHour], e[entryMinute] = byte(utc.Hour()), byte(utc.Minute())
	binary.LittleEndian.PutUint32(e[entryTitleCount:], entryTitleLen)
	title := make([]byte, entryTitleLen)
	copy(title, played.Format("2006-01-02 15:04"))
	copy(e[entryTitle:], title)
}
