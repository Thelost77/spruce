package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Thelost77/spruce/internal/app"
	"github.com/Thelost77/spruce/internal/config"
	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/player"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	closeLog := logger.Init()
	defer closeLog()
	logger.Info("starting spruce TUI")

	cfgDir := config.ConfigDir()
	cfg, err := config.Load(filepath.Join(cfgDir, "config.toml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	mpv := player.NewMpv()
	rootModel := app.New(&cfg, mpv)

	p := tea.NewProgram(rootModel, tea.WithAltScreen())
	rootModel.SetProgram(p)

	finalModel, err := p.Run()
	if err != nil {
		logger.Error("program exited with error", "err", err)
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	if m, ok := finalModel.(app.Model); ok {
		m.Cleanup()
	}
	logger.Info("spruce session ended")
}
