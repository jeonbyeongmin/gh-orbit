// Command orbit is the gh-orbit TUI.
//
// Built as a `gh` extension: `gh extension install jeonbyeongmin/gh-orbit`,
// invoked as `gh orbit`. The binary must keep the name `gh-orbit` for the
// extension manager to find it.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jeonbyeongmin/gh-orbit/internal/config"
	"github.com/jeonbyeongmin/gh-orbit/internal/tui"
)

// version is overwritten at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	logFile, err := config.OpenLog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "orbit: cannot open log: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logFile.Close() }()
	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("orbit start (version=%s)", version)

	p := tea.NewProgram(tui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Printf("orbit exit error: %v", err)
		fmt.Fprintf(os.Stderr, "orbit: %v\n", err)
		os.Exit(1)
	}
}
