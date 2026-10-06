// usf4-replay-saver copies Ultra Street Fighter IV replays out of the game's
// 10-match ring before the game overwrites them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

var version = "dev"

func main() {
	// Started with no arguments on Windows (a double-click), it opens the
	// window. Any argument runs the command line version.
	if len(os.Args) == 1 && guiAvailable {
		os.Exit(runGUI(false))
	}
	if len(os.Args) == 2 && os.Args[1] == "-minimized" && guiAvailable {
		os.Exit(runGUI(true))
	}
	attachConsole()
	os.Exit(run())
}

func run() int {
	out := flag.String("out", defaultOutputDir(), "folder to copy replays into")
	saveDir := flag.String("save-dir", "", "the game's SSF4_SaveData folder, if it is not found on its own")
	once := flag.Bool("once", false, "copy what is there now and exit instead of watching")
	all := flag.Bool("all", false, "also copy the replays you saved by hand in the game (slots 0-299)")
	list := flag.Bool("list", false, "list the replays already copied and exit")
	restoreFile := flag.String("restore", "", "experimental: put a copied replay back into the game's recent matches")
	interval := flag.Duration("interval", 5*time.Second, "how often to check for new replays while watching")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("usf4-replay-saver", version)
		return 0
	}
	if *list {
		return report(listArchive(*out))
	}

	dirs := findSaveDirs()
	if *saveDir != "" {
		dirs = []string{*saveDir}
	}
	if len(dirs) == 0 {
		fmt.Println("Could not find the game's save folder. It is normally at")
		fmt.Println(`  Steam\userdata\<your id>\45760\remote\CAPCOM\SUPERSTREETFIGHTERIV\SSF4_SaveData`)
		fmt.Println("Run the tool again with -save-dir set to that folder.")
		return 1
	}

	a, err := openArchive(*out)
	if err != nil {
		return report(fmt.Errorf("could not open %s: %w", *out, err))
	}

	if *restoreFile != "" {
		if len(dirs) > 1 {
			fmt.Println("More than one Steam account has USF4 saves. Pick one with -save-dir:")
			for _, d := range dirs {
				fmt.Println("  " + d)
			}
			return 1
		}
		slot, err := restore(*restoreFile, dirs[0], a)
		if err != nil {
			return report(err)
		}
		if slot < 0 {
			fmt.Println("That replay is already in the game's recent matches. Nothing to do.")
			return 0
		}
		fmt.Printf("Restored into slot %d of %s\n", slot, dirs[0])
		fmt.Println("Start the game and open your recent replays to watch it. Save it in the game if you")
		fmt.Println("want to keep it there, because your next match may replace it.")
		return 0
	}

	fmt.Printf("usf4-replay-saver %s\n", version)
	for _, d := range dirs {
		fmt.Println("Game saves: " + d)
	}
	fmt.Println("Copying to: " + *out)

	first := matchFirst
	if *all {
		first = savedFirst
	}
	result, err := a.scan(dirs, first, ringLast)
	if err != nil {
		return report(err)
	}
	printSaved(result.saved)
	fmt.Printf("%d replays in the folder.\n", a.count())
	if *once {
		return 0
	}

	fmt.Println()
	fmt.Println("Watching for new matches. Leave this window open while you play.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			fmt.Println("Stopped.")
			return 0
		case <-ticker.C:
			result, err := a.scan(dirs, matchFirst, ringLast)
			if err != nil {
				fmt.Println("Error: " + err.Error())
				continue
			}
			printSaved(result.saved)
		}
	}
}

func printSaved(names []string) {
	for _, name := range names {
		fmt.Printf("%s  saved %s\n", time.Now().Format("15:04:05"), name)
	}
}

func report(err error) int {
	if err != nil {
		if errors.Is(err, errGameRunning) {
			fmt.Println("Close Street Fighter IV first, then try again.")
			return 1
		}
		fmt.Println("Error: " + err.Error())
		return 1
	}
	return 0
}
