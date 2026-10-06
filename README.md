# USF4 Replay Saver

Ultra Street Fighter IV on PC keeps only your last 10 matches as replays. After every match it overwrites one of them, so after a long session most of your games are gone unless you saved each one by hand in the Replay Channel.

This app saves every match for you. It is a stopgap until Ember Netplay can save replays itself.

<img src="docs/screenshot.png" alt="The app window listing saved replays" width="390">

## Use it

1. Download `usf4-replay-saver-windows-amd64.zip` from the [latest release](https://github.com/Confetti3/usf4-replay-saver/releases/latest) and unzip it anywhere.
2. Open `usf4-replay-saver.exe` and leave it open while you play.

That's it. Each match shows up in the window as soon as it ends, and the files go to `Documents\USF4 Replays`. Opening it after a session still saves your last 10 matches.

Tick **Start with Windows** if you don't want to remember to open it. It then starts minimized every time you log in.

Windows may warn that the app is unrecognized, because it is not code signed. Choose **More info** and then **Run anyway**. The app needs the Microsoft Edge WebView2 Runtime, which Windows 10 and 11 normally already have; if it is missing, the app tells you where to get it.

## Watching a saved replay again

Close the game, press **Watch now** next to a replay, and the game starts through Steam with that replay in its recent replays; **Watch in game** only puts it in, for you to start the game yourself through Steam.

With [Ember Netplay](https://github.com/Confetti3/SF4-Ember-Netplay) installed the button is **Watch in Ember** instead: it hands the replay to Ember, which puts it into the running game and opens the battle log on it, no restart; with the game closed, Ember starts it with the replay. The app adds it to the game's recent matches, in place of the oldest one (which it has already saved), and updates the game's list. It shows up as the newest entry, dated now, with the date it was played as its title: the list shows only its newest 30 entries, sorted by date, so an old replay with its own date would often not be listed at all. Your next match may replace it again, so save it in the game if you want it to stay there.

Start the game through Steam after this, not from a shortcut to `SSFIV.exe`: Steam keeps its own list of the save files' sizes, and the game reads through it. Steam checks the folder when it starts the game; without that check the game reads the put-back replay at the old file's size and reports it as damaged.

To do this the app keeps the game's list details for each replay in a hidden `.index` folder inside `USF4 Replays`. Keep that folder with your replays if you move them.

Replays saved by versions before 0.3.0 only have those details if they were still among your 10 recent matches when 0.3.0 first ran. For the others the button is greyed out, but the replay files are kept.

Steam uploads the changed save the next time you start the game. If Steam ever shows a Cloud conflict for USF4 after this, keep the local files.

## If it says "Waiting for Street Fighter IV"

Play one match and it starts on its own. If it still says that afterwards, paste the game's save folder into the box. It is normally at

```
Steam\userdata\<your Steam ID>\45760\remote\CAPCOM\SUPERSTREETFIGHTERIV\SSF4_SaveData
```

If no replays ever appear, check that replay recording is turned on in the game's options. It has not been confirmed yet whether the game keeps its 10 recent replays with that option off.

## Linux and Steam Deck

There is no window on Linux yet. Download the `linux-amd64` archive and run `./usf4-replay-saver` in a terminal. It looks in the usual Steam folders (`~/.steam/steam`, `~/.local/share/Steam` and the Flatpak one) and saves to `~/USF4 Replays`. Replays are stored by Steam itself, not inside the Proton prefix, so it works the same under Proton.

## Command line

Run the exe with any of these options to use it without the window:

| Option | What it does |
| --- | --- |
| `-once` | Copy what is there now and exit, instead of watching |
| `-all` | Also copy the replays you saved by hand in the game (up to 300) |
| `-list` | List the replays already copied and check that none are damaged |
| `-out <folder>` | Copy into a different folder |
| `-save-dir <folder>` | Use this save folder instead of searching for it |
| `-restore <file>` | Put a copied replay back into the game's recent matches |
| `-interval 5s` | How often to check for a new replay while watching |

## Good to know

- The app only reads the game's files, except when you press **Watch in game**.
- Each file is the game's own replay data, unchanged. The name is the date and time the match was recorded plus a short checksum, which the app uses to skip replays it already has.
- If more than one Steam account on the PC has played the game, it saves replays from all of them into the same folder. Its log lists every save folder it found.
- Its settings and a small log live in `%LOCALAPPDATA%\usf4-replay-saver`.

## How the game stores replays

The game keeps its saves in Steam Cloud, in the folder above. Every save is a numbered file with a small `N.0` file beside it that holds the CRC-32 of the save.

- Files `300` to `309` hold the last 10 matches. The game writes the replay of every Versus battle there, spectated ones too, into an empty slot or else the one with the oldest save time. The list in the game is sorted by the match's date, so the replaced row is not always the bottom one.
- `replays-swan.dat` is the list the game shows for those 10. Each 125-byte record, after a 40-byte header, describes one slot: its number, a used flag, the replay's CRC-32, size, 64-bit save time, a 21-character title, and the details the menu shows (date, the two fighters at +67 and +101, the hour and minute at the end, UTC). The file starts with a CRC-32 of the rest of it. This app keeps each record from its used flag on.
- Files `0` to `299` hold replays you saved by hand from the Replay Channel. A file called `LIST` indexes them with records in the same layout after an 8-byte header. With [Ember Netplay](https://github.com/Confetti3/SF4-Ember-Netplay) the game's match list is slots 280 to 309 instead, so this app watches those too; in the stock game slots 280 to 299 hold replays the online service handed out.
- Every replay starts with `#BRP` and carries the time it was recorded at byte 16. The replay itself names no one; the record holds Steam IDs for native online matches and nothing for Ember matches, which is why the list here shows fighters, not players.

## Building

Install Go 1.26 or newer and run `go build -ldflags "-H windowsgui"`. `build.ps1 -Version v0.3.0` makes the release archives. The icon, manifest and version info come from `winres/`; after changing them, run `go run github.com/tc-hib/go-winres@latest make --arch amd64`.

## License

MIT. See [LICENSE](LICENSE).
