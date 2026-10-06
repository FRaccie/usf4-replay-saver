//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	guiAvailable = true
	windowTitle  = "USF4 Replay Saver"
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "USF4 Replay Saver"
	scanEvery    = 3 * time.Second
)

//go:embed ui.html
var uiHTML string

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procFindWindow    = user32.NewProc("FindWindowW")
	procShowWindow    = user32.NewProc("ShowWindow")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procMessageBox    = user32.NewProc("MessageBoxW")
	procDpiForSystem  = user32.NewProc("GetDpiForSystem")
	dwmapi            = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmSetAttr    = dwmapi.NewProc("DwmSetWindowAttribute")
)

type guiConfig struct {
	SaveDir string `json:"saveDir,omitempty"`
}

type gui struct {
	view    webview2.WebView
	archive *archive
	log     *log.Logger
	dataDir string

	mu     sync.Mutex
	config guiConfig
	dirs   []string
	fresh  map[string]bool // replays saved since the window opened
}

type replayInfo struct {
	Name     string `json:"name"`
	Time     int64  `json:"time"` // Unix milliseconds
	New      bool   `json:"new"`
	CanWatch bool   `json:"canWatch"` // the game's list entry for it was kept
	Matchup  string `json:"matchup"`  // "Ryu vs Ken", from the kept entry
}

type guiState struct {
	Found    bool         `json:"found"`
	OutDir   string       `json:"outDir"`
	Expected string       `json:"expected"`
	Replays  []replayInfo `json:"replays"`
	Startup  bool         `json:"startup"`
	Ember    bool         `json:"ember"` // Ember Netplay is installed, so it can play a replay in a running game
	Version  string       `json:"version"`
}

type actionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func runGUI(minimized bool) int {
	// One copy at a time; a second launch brings the first to the front.
	name, _ := windows.UTF16PtrFromString(`Local\usf4-replay-saver`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		title, _ := windows.UTF16PtrFromString(windowTitle)
		if hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(title))); hwnd != 0 {
			procShowWindow.Call(hwnd, 9) // SW_RESTORE
			procSetForeground.Call(hwnd)
		}
		return 0
	}
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}

	g := &gui{fresh: map[string]bool{}, dataDir: appDataDir()}
	os.MkdirAll(g.dataDir, 0o755)
	logFile, err := os.OpenFile(filepath.Join(g.dataDir, "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		defer logFile.Close()
		g.log = log.New(logFile, "", log.LstdFlags)
	} else {
		g.log = log.New(os.Stderr, "", log.LstdFlags)
	}
	g.log.Printf("started %s", version)
	g.loadConfig()

	g.archive, err = openArchive(defaultOutputDir())
	if err != nil {
		messageBox("Could not create the replays folder:\n" + err.Error())
		return 1
	}
	g.dirs = g.findDirs()
	g.logDirs(g.dirs)
	if g.startupEnabled() {
		g.setStartup(true) // keeps the entry pointing at this copy of the exe
	}

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(g.dataDir, "WebView2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  windowTitle,
			Width:  scaled(520),
			Height: scaled(700),
			IconId: 1,
			Center: true,
		},
	})
	if view == nil {
		g.log.Printf("WebView2 could not start")
		messageBox("USF4 Replay Saver needs the Microsoft Edge WebView2 Runtime, which is missing on this PC.\n\n" +
			"Get it from https://go.microsoft.com/fwlink/p/?LinkId=2124703 and then open this app again.")
		return 1
	}
	defer view.Destroy()
	g.view = view
	view.SetSize(int(scaled(400)), int(scaled(480)), webview2.HintMin)
	if !lightTheme() {
		dark := int32(1)
		procDwmSetAttr.Call(uintptr(view.Window()), 20, uintptr(unsafe.Pointer(&dark)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
	}

	view.Bind("getState", g.state)
	view.Bind("openFolder", g.openFolder)
	view.Bind("showFile", g.showFile)
	view.Bind("watchInGame", g.watchInGame)
	view.Bind("watchNow", g.watchNow)
	view.Bind("watchInEmber", g.watchInEmber)
	view.Bind("setSaveDir", g.setSaveDir)
	view.Bind("setStartup", func(on bool) actionResult {
		if err := g.setStartup(on); err != nil {
			return actionResult{Message: "Could not change the startup setting: " + err.Error()}
		}
		return actionResult{OK: true}
	})
	view.SetHtml(uiHTML)
	if minimized {
		procShowWindow.Call(uintptr(view.Window()), 6) // SW_MINIMIZE
	}

	stop := make(chan struct{})
	go g.watch(stop)
	view.Run()
	close(stop)
	return 0
}

// scaled converts a size at 100% display scaling to the pixels this
// DPI-aware window is measured in.
func scaled(size int) uint {
	dpi := uintptr(96)
	if procDpiForSystem.Find() == nil {
		if d, _, _ := procDpiForSystem.Call(); d != 0 {
			dpi = d
		}
	}
	return uint(size * int(dpi) / 96)
}

// lightTheme reports the Windows app theme, which the page also follows.
func lightTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err != nil || v != 0
}

func appDataDir() string {
	if base, err := os.UserCacheDir(); err == nil { // %LOCALAPPDATA%
		return filepath.Join(base, "usf4-replay-saver")
	}
	return filepath.Join(os.TempDir(), "usf4-replay-saver")
}

func messageBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(windowTitle)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10) // MB_ICONERROR
}

func (g *gui) loadConfig() {
	data, err := os.ReadFile(filepath.Join(g.dataDir, "config.json"))
	if err == nil {
		json.Unmarshal(data, &g.config)
	}
}

func (g *gui) saveConfig() error {
	data, _ := json.MarshalIndent(g.config, "", "  ")
	return os.WriteFile(filepath.Join(g.dataDir, "config.json"), data, 0o644)
}

func (g *gui) findDirs() []string {
	if g.config.SaveDir != "" {
		if info, err := os.Stat(g.config.SaveDir); err == nil && info.IsDir() {
			return []string{g.config.SaveDir}
		}
	}
	return findSaveDirs()
}

// watch copies new replays as the game writes them and tells the page.
func (g *gui) watch(stop <-chan struct{}) {
	ticker := time.NewTicker(scanEvery)
	defer ticker.Stop()
	for {
		g.mu.Lock()
		if len(g.dirs) == 0 {
			if g.dirs = g.findDirs(); len(g.dirs) > 0 {
				g.logDirs(g.dirs)
			}
		}
		dirs := g.dirs
		g.mu.Unlock()

		changed := false
		if len(dirs) > 0 {
			result, err := g.archive.scan(dirs, matchFirst, ringLast)
			if err != nil {
				g.log.Printf("scan: %v", err)
			}
			g.mu.Lock()
			for _, name := range result.saved {
				g.fresh[name] = true
				g.log.Printf("saved %s", name)
			}
			g.mu.Unlock()
			if result.entries > 0 {
				g.log.Printf("kept %d list entries", result.entries)
			}
			changed = len(result.saved) > 0 || result.entries > 0
		}
		if changed && g.view != nil {
			g.view.Dispatch(func() { g.view.Eval("window.refresh && window.refresh()") })
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func (g *gui) state() guiState {
	g.mu.Lock()
	found := len(g.dirs) > 0
	fresh := make(map[string]bool, len(g.fresh))
	for k, v := range g.fresh {
		fresh[k] = v
	}
	g.mu.Unlock()

	s := guiState{
		Found:    found,
		OutDir:   g.archive.dir,
		Expected: `Steam\userdata\<your Steam ID>\45760\remote\CAPCOM\SUPERSTREETFIGHTERIV\SSF4_SaveData`,
		Startup:  g.startupEnabled(),
		Ember:    emberInstalled(),
		Version:  version,
		Replays:  []replayInfo{},
	}
	entries, _ := os.ReadDir(g.archive.dir)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, replayExt) {
			continue
		}
		const stamp = "2006-01-02_15-04-05"
		var when time.Time
		err := errors.New("no timestamp")
		if len(name) >= len(stamp) {
			when, err = time.ParseInLocation(stamp, name[:len(stamp)], time.Local)
		}
		if err != nil {
			if info, err := entry.Info(); err == nil {
				when = info.ModTime()
			}
		}
		info := replayInfo{Name: name, Time: when.UnixMilli(), New: fresh[name]}
		if m := archivedName.FindStringSubmatch(name); m != nil {
			if crc, err := strconv.ParseUint(m[1], 16, 32); err == nil {
				if e := g.archive.loadEntry(uint32(crc)); e != nil {
					info.CanWatch = true
					info.Matchup = details(e).matchup()
				}
			}
		}
		s.Replays = append(s.Replays, info)
	}
	sort.Slice(s.Replays, func(i, j int) bool { return s.Replays[i].Time > s.Replays[j].Time })
	return s
}

func (g *gui) openFolder() {
	exec.Command("explorer.exe", g.archive.dir).Start()
}

func (g *gui) replayPath(name string) (string, bool) {
	if name != filepath.Base(name) || !strings.HasSuffix(name, replayExt) {
		return "", false
	}
	path := filepath.Join(g.archive.dir, name)
	_, err := os.Stat(path)
	return path, err == nil
}

func (g *gui) showFile(name string) {
	if path, ok := g.replayPath(name); ok {
		cmd := exec.Command("explorer.exe")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`explorer.exe /select,"%s"`, path)}
		cmd.Start()
	}
}

func (g *gui) watchInGame(name string) actionResult {
	path, ok := g.replayPath(name)
	if !ok {
		return actionResult{Message: "That replay file is missing from the folder."}
	}
	g.mu.Lock()
	dirs := g.dirs
	g.mu.Unlock()
	if len(dirs) == 0 {
		return actionResult{Message: "The game's save folder has not been found yet."}
	}
	dir := activeSaveDir(dirs)
	slot, err := restore(path, dir, g.archive)
	switch {
	case errors.Is(err, errGameRunning):
		return actionResult{Message: "Close Street Fighter IV first, then try again."}
	case errors.Is(err, errNoEntry):
		return actionResult{Message: noEntryMessage}
	case err != nil:
		g.log.Printf("restore %s: %v", name, err)
		return actionResult{Message: "That did not work: " + err.Error()}
	case slot < 0:
		return actionResult{OK: true, Message: "That replay is already in the game. Start the game and open your recent replays."}
	}
	g.log.Printf("restored %s into slot %d of %s", name, slot, dir)
	return actionResult{OK: true, Message: "Done. Start the game through Steam and open your recent replays; it is the newest one."}
}

// watchNow puts the replay back and starts the game through Steam, which
// refreshes Steam's list of the save files on the way (the game reads its
// files through that list, so a put-back replay needs it).
func (g *gui) watchNow(name string) actionResult {
	result := g.watchInGame(name)
	if !result.OK {
		return result
	}
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", "steam://rungameid/45760").Start(); err != nil {
		g.log.Printf("start game: %v", err)
		return actionResult{OK: true, Message: "The replay is in the game, but Steam could not be asked to start it. Start the game through Steam and open your recent replays."}
	}
	return actionResult{OK: true, Message: "The game is starting. Open your recent replays; it is the newest one."}
}

// emberInstalled reports whether Ember Netplay's launcher has registered the
// ember: link scheme, which a replay link needs.
func emberInstalled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\ember\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	key.Close()
	return true
}

// watchInEmber hands the replay to Ember Netplay through an ember://replay
// link: Ember's launcher gives it to the running game, which plays it from
// its main menu, or starts the game with it. Nothing is written here; Ember
// does the put-back itself, through Steam, while the game runs.
func (g *gui) watchInEmber(name string) actionResult {
	path, ok := g.replayPath(name)
	if !ok {
		return actionResult{Message: "That replay file is missing from the folder."}
	}
	link := "ember://replay/open?file=" + url.PathEscape(path)
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Start(); err != nil {
		g.log.Printf("open %s: %v", link, err)
		return actionResult{Message: "Ember could not be asked to play it: " + err.Error()}
	}
	g.log.Printf("handed %s to Ember", name)
	return actionResult{OK: true, Message: "Handed to Ember. With the game at its main menu it opens the battle log on this replay."}
}

const noEntryMessage = "This one was saved by an older version of the app without the details the game needs to list it. Replays saved from now on can be put back."

func (g *gui) logDirs(dirs []string) {
	if len(dirs) == 0 {
		g.log.Printf("no save folder found")
	}
	for _, dir := range dirs {
		g.log.Printf("save folder %s", dir)
	}
}

// activeSaveDir picks the account that played most recently when more than
// one Steam account on the PC has USF4 saves.
func activeSaveDir(dirs []string) string {
	best, bestTime := dirs[0], time.Time{}
	for _, dir := range dirs {
		for n := matchFirst; n <= ringLast; n++ {
			if s, err := readSlot(dir, n); err == nil && s.written.After(bestTime) {
				best, bestTime = dir, s.written
			}
		}
	}
	return best
}

func (g *gui) setSaveDir(input string) actionResult {
	dir := strings.Trim(strings.TrimSpace(input), `"`)
	if dir == "" {
		return actionResult{Message: "Paste the folder path first."}
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return actionResult{Message: "That folder does not exist."}
	}
	if _, err := os.Stat(filepath.Join(dir, "LIST")); err != nil {
		if _, err := os.Stat(filepath.Join(dir, "300")); err != nil {
			return actionResult{Message: "That does not look like the game's save folder. It should be the folder named SSF4_SaveData."}
		}
	}
	g.mu.Lock()
	g.config.SaveDir = dir
	g.dirs = []string{dir}
	err := g.saveConfig()
	g.mu.Unlock()
	if err != nil {
		return actionResult{Message: "Could not remember that folder: " + err.Error()}
	}
	return actionResult{OK: true}
}

func (g *gui) startupEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValueName)
	return err == nil
}

func (g *gui) setStartup(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValueName, fmt.Sprintf(`"%s" -minimized`, exe))
}
