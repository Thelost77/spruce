package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Thelost77/spruce/internal/config"
	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/mpris"
	"github.com/Thelost77/spruce/internal/player"
	"github.com/Thelost77/spruce/internal/screens/library"
	"github.com/Thelost77/spruce/internal/screens/login"
	"github.com/Thelost77/spruce/internal/screens/queue"
	"github.com/Thelost77/spruce/internal/ui"
	"github.com/Thelost77/spruce/internal/ui/components"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type musicLibrariesLoadedMsg struct {
	libraries []jellyfin.Library
	err       error
}

type Model struct {
	screen Screen

	loginScreen   login.Model
	libraryScreen library.Model
	queueScreen   queue.Model
	palette       components.Palette
	shuffle       bool

	client *jellyfin.Client
	cfg    *config.Config
	mpv    *player.Mpv

	program     *tea.Program
	mprisBridge *mpris.Bridge
	mprisState  *MprisState

	playerState player.Model

	tracks       []jellyfin.Track
	currentIndex int

	playGeneration uint64
	lastHeartbeat  time.Time

	width  int
	height int
	styles ui.Styles
}

type MprisState struct {
	IsPlaying bool
	IsPaused  bool
	Title     string
	Authors   []string
	ItemID    string
	Position  float64
	Duration  float64
	Speed     float64
	Volume    int
	QueueLen  int
}

func New(cfg *config.Config, mpv *player.Mpv) Model {
	styles := ui.DefaultStyles()
	var actualCfg config.Config
	if cfg != nil {
		actualCfg = *cfg
	} else {
		actualCfg = config.Config{Player: config.PlayerConfig{Speed: 1.0}}
	}
	var p player.Player
	if mpv != nil {
		p = mpv
	}
	pal := components.NewPalette()
	pal.SetStyles(styles)
	return Model{
		screen:        ScreenLogin,
		loginScreen:   login.New(styles),
		libraryScreen: library.New(styles),
		queueScreen:   queue.New(styles),
		palette:       pal,
		cfg:           cfg,
		mpv:           mpv,
		mprisState:    &MprisState{},
		playerState:   player.NewModel(p, actualCfg, styles),
		currentIndex:  -1,
		styles:        styles,
	}
}

func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
	m.mprisBridge = mpris.NewBridge(p)
	state := m.mprisState
	m.mprisBridge.Bind(func() mpris.ModelAccessor {
		return mprisStateAccessor{state}
	})
	m.mprisBridge.Start()
}

func (m *Model) Cleanup() {
	if m.mprisBridge != nil {
		_ = m.mprisBridge.Stop()
	}
	if m.mpv != nil {
		_ = m.mpv.Quit()
	}
}

func (m Model) screenHeight() int {
	h := m.height
	if m.playerState.Title != "" {
		h--
	}
	if h < 0 {
		return 0
	}
	return h
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	sh := m.screenHeight()
	m.loginScreen.SetSize(width, sh)
	m.libraryScreen.SetSize(width, sh)
	m.queueScreen.SetSize(width, sh)
	m.playerState.SetWidth(width)
	m.palette.SetSize(width, height)
}

func (m Model) Init() tea.Cmd {
	if m.cfg != nil && m.cfg.Server.Address != "" && m.cfg.Server.Token != "" && m.cfg.Server.UserID != "" {
		return func() tea.Msg {
			return login.LoginSuccessMsg{
				Token:     m.cfg.Server.Token,
				ServerURL: m.cfg.Server.Address,
				Username:  m.cfg.Server.Username,
				UserID:    m.cfg.Server.UserID,
			}
		}
	}
	return m.loginScreen.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.palette.Visible() {
		cmd, handled := m.palette.Update(msg)
		if handled {
			action, _, _, itemID, data := m.palette.SelectedAction()
			if action != components.ActionNone {
				m.palette.ClearSelection()
				m.palette.Close()
				return m.handlePaletteAction(action, itemID, data)
			}
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		if m.screen != ScreenLogin {
			if msg.String() == "ctrl+p" {
				m.openCommandPalette()
				return m, nil
			}
			isFiltering := false
			if m.screen == ScreenLibrary && m.libraryScreen.IsFiltering() {
				isFiltering = true
			} else if m.screen == ScreenQueue && m.queueScreen.IsFiltering() {
				isFiltering = true
			}
			if !isFiltering {
				switch msg.String() {
				case "q":
					return m, tea.Quit
				case " ":
					if m.mpv != nil {
						return m, player.TogglePauseCmd(m.mpv, m.playerState.Playing)
					}
					return m, nil
				case "n":
					if len(m.tracks) > 0 && m.currentIndex+1 < len(m.tracks) {
						return m.startPlaybackAt(m.nextIndex(m.currentIndex + 1))
					}
					return m, nil
				case "p":
					if m.playerState.Position > 3.0 {
						if m.mpv != nil {
							return m, player.SeekCmd(m.mpv, 0)
						}
						return m, nil
					}
					if len(m.tracks) > 0 && m.currentIndex-1 >= 0 {
						return m.startPlaybackAt(m.currentIndex - 1)
					}
					return m, nil
				case "s":
					m.shuffle = !m.shuffle
					m.queueScreen.SetShuffle(m.shuffle)
					return m, nil
				}
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.screen != ScreenLogin {
				if m.screen == ScreenLibrary {
					m.screen = ScreenQueue
				} else {
					m.screen = ScreenLibrary
				}
				return m, nil
			}
		}

	case login.LoginSuccessMsg:
		logger.Info("login succeeded, setting client", "user", msg.Username)
		if m.cfg != nil {
			m.cfg.Server.Address = msg.ServerURL
			m.cfg.Server.Username = msg.Username
			m.cfg.Server.Token = msg.Token
			m.cfg.Server.UserID = msg.UserID
			_ = config.Save(filepath.Join(config.ConfigDir(), "config.toml"), *m.cfg)
		}
		m.client = jellyfin.NewClient(msg.ServerURL, msg.Token, msg.UserID)
		client := m.client
		fetchLibsCmd := func() tea.Msg {
			libs, err := client.GetMusicLibraries(context.Background())
			return musicLibrariesLoadedMsg{libraries: libs, err: err}
		}
		var cmd tea.Cmd
		m.loginScreen, cmd = m.loginScreen.Update(msg)
		return m, tea.Batch(cmd, fetchLibsCmd)

	case musicLibrariesLoadedMsg:
		if msg.err == nil && len(msg.libraries) > 0 {
			libID := msg.libraries[0].ID
			m.libraryScreen.SetClient(m.client, libID)
			m.screen = ScreenLibrary
			return m, tea.Batch(m.libraryScreen.Init(), m.libraryScreen.FetchAllTracksCmd())
		}

	case library.AllTracksLoadedMsg:
		m.libraryScreen, _ = m.libraryScreen.Update(msg)
		return m, nil

	case library.PlayTracksMsg:
		logger.Info("received PlayTracksMsg", "count", len(msg.Tracks), "start", msg.StartIndex)
		m.tracks = msg.Tracks
		m.screen = ScreenQueue
		newM, cmd := m.startPlaybackAt(msg.StartIndex)
		return newM, cmd

	case player.PositionMsg:
		newM, cmd := m.handlePositionMsg(msg)
		return newM, cmd

	case player.PlayerReadyMsg:
		var cmds []tea.Cmd
		if m.mpv != nil {
			cmds = append(cmds, player.TickCmd(m.mpv, m.playGeneration))
		}
		return m, tea.Batch(cmds...)

	case player.PlayerLaunchErrMsg:
		logger.Error("player failed to launch", "err", msg.Err)
		newM, cmd := m.stopPlayback()
		return newM, cmd

	case queue.JumpQueueMsg:
		newM, cmd := m.startPlaybackAt(msg.Index)
		return newM, cmd

	case queue.RemoveQueueMsg:
		if msg.Index >= 0 && msg.Index < len(m.tracks) {
			m.tracks = append(m.tracks[:msg.Index], m.tracks[msg.Index+1:]...)
			if m.currentIndex == msg.Index {
				if len(m.tracks) == 0 {
					newM, cmd := m.stopPlayback()
					return newM, cmd
				}
				if m.currentIndex >= len(m.tracks) {
					m.currentIndex = len(m.tracks) - 1
				}
				newM, cmd := m.startPlaybackAt(m.currentIndex)
				return newM, cmd
			} else if m.currentIndex > msg.Index {
				m.currentIndex--
			}
			m.syncQueueScreen()
		}
		return m, nil

	case queue.QueueActionMsg:
		switch msg.Action {
		case "toggle_pause":
			m.playerState.Playing = !m.playerState.Playing
			m.syncQueueScreen()
			if m.mpv != nil {
				return m, player.TogglePauseCmd(m.mpv, m.playerState.Playing)
			}
		case "next":
			newM, cmd := m.startPlaybackAt(m.nextIndex(m.currentIndex + 1))
			return newM, cmd
		case "prev":
			if m.playerState.Position > 3.0 {
				if m.mpv != nil {
					return m, player.SeekCmd(m.mpv, 0)
				}
			} else {
				newM, cmd := m.startPlaybackAt(m.currentIndex - 1)
				return newM, cmd
			}
		case "shuffle":
			m.shuffle = !m.shuffle
			m.queueScreen.SetShuffle(m.shuffle)
			return m, nil
		case "clear":
			m.tracks = nil
			m.currentIndex = -1
			newM, cmd := m.stopPlayback()
			return newM, cmd
		}

	case mpris.PlayPauseMsg:
		m.playerState.Playing = !m.playerState.Playing
		m.syncQueueScreen()
		if m.mpv != nil {
			return m, player.TogglePauseCmd(m.mpv, m.playerState.Playing)
		}

	case mpris.NextMsg:
		newM, cmd := m.startPlaybackAt(m.nextIndex(m.currentIndex + 1))
		return newM, cmd

	case mpris.PreviousMsg:
		if m.playerState.Position > 3.0 {
			if m.mpv != nil {
				return m, player.SeekCmd(m.mpv, 0)
			}
		} else {
			newM, cmd := m.startPlaybackAt(m.currentIndex - 1)
			return newM, cmd
		}

	case mpris.SeekMsg:
		if m.mpv != nil {
			return m, player.SeekCmd(m.mpv, msg.Offset)
		}

	case mpris.SetVolumeMsg:
		m.playerState.Volume = msg.Volume
		if m.mpv != nil {
			return m, player.SetVolumeCmd(m.mpv, msg.Volume)
		}
	}

	var cmd tea.Cmd
	switch m.screen {
	case ScreenLogin:
		m.loginScreen, cmd = m.loginScreen.Update(msg)
	case ScreenLibrary:
		m.libraryScreen, cmd = m.libraryScreen.Update(msg)
	case ScreenQueue:
		m.queueScreen, cmd = m.queueScreen.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	var content string
	switch m.screen {
	case ScreenLibrary:
		content = m.libraryScreen.View()
	case ScreenQueue:
		content = m.queueScreen.View()
	default:
		content = m.loginScreen.View()
	}
	if m.width == 0 {
		if m.palette.Visible() {
			return m.overlayPaletteModal(content)
		}
		return content
	}
	placed := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	if m.palette.Visible() {
		return m.overlayPaletteModal(placed)
	}
	return placed
}

// ModelAccessor implementation for mpris.Bridge.Bind

func (m Model) IsPlaying() bool {
	return len(m.tracks) > 0 && m.currentIndex >= 0 && m.currentIndex < len(m.tracks)
}

func (m Model) IsPaused() bool {
	return !m.playerState.Playing
}

func (m Model) HasActiveItem() bool {
	return m.IsPlaying()
}

func (m Model) CurrentTitle() string {
	if m.IsPlaying() {
		return m.tracks[m.currentIndex].Name
	}
	return ""
}

func (m Model) CurrentAuthors() []string {
	if m.IsPlaying() {
		return m.tracks[m.currentIndex].Artists
	}
	return nil
}

func (m Model) CurrentItemID() string {
	if m.IsPlaying() {
		return m.tracks[m.currentIndex].ID
	}
	return ""
}

func (m Model) PlayerPosition() float64 {
	return m.playerState.Position
}

func (m Model) PlayerDuration() float64 {
	if m.IsPlaying() && m.playerState.Duration <= 0 {
		return m.tracks[m.currentIndex].Duration()
	}
	return m.playerState.Duration
}

func (m Model) PlayerSpeed() float64 {
	return m.playerState.Speed
}

func (m Model) PlayerVolume() int {
	return m.playerState.Volume
}

func (m Model) QueueLength() int {
	return len(m.tracks)
}

type mprisStateAccessor struct{ s *MprisState }

func (a mprisStateAccessor) IsPlaying() bool          { return a.s != nil && a.s.IsPlaying }
func (a mprisStateAccessor) IsPaused() bool           { return a.s != nil && a.s.IsPaused }
func (a mprisStateAccessor) HasActiveItem() bool      { return a.s != nil && a.s.IsPlaying }
func (a mprisStateAccessor) CurrentTitle() string     { if a.s != nil { return a.s.Title }; return "" }
func (a mprisStateAccessor) CurrentAuthors() []string { if a.s != nil { return a.s.Authors }; return nil }
func (a mprisStateAccessor) CurrentItemID() string    { if a.s != nil { return a.s.ItemID }; return "" }
func (a mprisStateAccessor) PlayerPosition() float64  { if a.s != nil { return a.s.Position }; return 0 }
func (a mprisStateAccessor) PlayerDuration() float64  { if a.s != nil { return a.s.Duration }; return 0 }
func (a mprisStateAccessor) PlayerSpeed() float64     { if a.s != nil { return a.s.Speed }; return 1.0 }
func (a mprisStateAccessor) PlayerVolume() int        { if a.s != nil { return a.s.Volume }; return 100 }
func (a mprisStateAccessor) QueueLength() int         { if a.s != nil { return a.s.QueueLen }; return 0 }

func (m Model) nextIndex(defaultNext int) int {
	if m.shuffle && len(m.tracks) > 1 {
		for i := 0; i < 10; i++ {
			idx := int(time.Now().UnixNano()) % len(m.tracks)
			if idx < 0 {
				idx = -idx
			}
			if idx != m.currentIndex {
				return idx
			}
		}
	}
	return defaultNext
}

func (m *Model) openCommandPalette() {
	staticItems := []components.PaletteItem{
		{Label: "Go to Library", Action: components.ActionGoLibrary},
		{Label: "Go to Queue", Action: components.ActionShowQueue},
		{Label: "Toggle Play / Pause", Action: components.ActionTogglePlay},
		{Label: "Next Track", Action: components.ActionNextChapter},
		{Label: "Previous Track", Action: components.ActionPrevChapter},
		{Label: "Clear Queue", Action: components.ActionClearQueue},
	}
	m.palette.Open(staticItems, m.contentSearchFunc())
}

func (m *Model) contentSearchFunc() components.SearchFunc {
	return func(query string) []components.PaletteItem {
		if query == "" {
			return nil
		}
		query = strings.ToLower(query)
		var results []components.PaletteItem

		for _, a := range m.libraryScreen.Artists() {
			if strings.Contains(strings.ToLower(a.Name), query) {
				results = append(results, components.PaletteItem{
					Label:  fmt.Sprintf("Artist: %s", a.Name),
					Action: components.ActionOpenSelected,
					ItemID: a.ID,
					Data:   a,
				})
			}
		}

		tracksToSearch := m.libraryScreen.AllTracks()
		if len(tracksToSearch) == 0 {
			tracksToSearch = m.libraryScreen.Tracks()
		}
		for _, t := range tracksToSearch {
			if strings.Contains(strings.ToLower(t.Name), query) || strings.Contains(strings.ToLower(t.DisplayArtist()), query) {
				results = append(results, components.PaletteItem{
					Label:  fmt.Sprintf("Track: %s — %s", t.Name, t.DisplayArtist()),
					Action: components.ActionPlayDirect,
					ItemID: t.ID,
					Data:   t,
				})
			}
		}

		for _, t := range m.tracks {
			if strings.Contains(strings.ToLower(t.Name), query) || strings.Contains(strings.ToLower(t.DisplayArtist()), query) {
				results = append(results, components.PaletteItem{
					Label:  fmt.Sprintf("Queue: %s — %s", t.Name, t.DisplayArtist()),
					Action: components.ActionPlayDirect,
					ItemID: t.ID,
					Data:   t,
				})
			}
		}

		return results
	}
}

func (m Model) handlePaletteAction(action components.PaletteAction, itemID string, data any) (tea.Model, tea.Cmd) {
	switch action {
	case components.ActionGoLibrary:
		m.screen = ScreenLibrary
		return m, nil
	case components.ActionShowQueue:
		m.screen = ScreenQueue
		return m, nil
	case components.ActionTogglePlay:
		m.playerState.Playing = !m.playerState.Playing
		m.syncQueueScreen()
		if m.mpv != nil {
			return m, player.TogglePauseCmd(m.mpv, m.playerState.Playing)
		}
		return m, nil
	case components.ActionNextChapter:
		if len(m.tracks) > 0 && m.currentIndex+1 < len(m.tracks) {
			return m.startPlaybackAt(m.nextIndex(m.currentIndex + 1))
		}
		return m, nil
	case components.ActionPrevChapter:
		if m.playerState.Position > 3.0 {
			if m.mpv != nil {
				return m, player.SeekCmd(m.mpv, 0)
			}
			return m, nil
		}
		if len(m.tracks) > 0 && m.currentIndex-1 >= 0 {
			return m.startPlaybackAt(m.currentIndex - 1)
		}
		return m, nil
	case components.ActionClearQueue:
		newM, _ := m.Update(queue.QueueActionMsg{Action: "clear"})
		return newM, nil
	case components.ActionOpenSelected:
		if artist, ok := data.(jellyfin.Artist); ok {
			m.screen = ScreenLibrary
			cmd := m.libraryScreen.SelectArtist(artist)
			return m, cmd
		}
	case components.ActionPlayDirect:
		if track, ok := data.(jellyfin.Track); ok {
			m.tracks = append(m.tracks, track)
			return m.startPlaybackAt(len(m.tracks) - 1)
		}
	}
	return m, nil
}

func (m Model) overlayPaletteModal(content string) string {
	overlay := m.palette.View()
	if overlay == "" {
		return content
	}
	if m.width <= 0 || m.height <= 0 {
		return lipgloss.JoinVertical(lipgloss.Left, content, "", overlay)
	}

	w := m.width
	if w > 120 {
		w = 120
	}
	baseLines := normalizeOverlayCanvas(content, w, m.height)
	overlayLines := strings.Split(overlay, "\n")
	overlayWidth := lipgloss.Width(overlay)
	overlayHeight := len(overlayLines)
	if overlayWidth <= 0 || overlayHeight == 0 {
		return content
	}

	x := max(0, (m.width-overlayWidth)/2)
	y := max(0, (m.height-overlayHeight)/2)
	for i, line := range overlayLines {
		if y+i >= len(baseLines) {
			break
		}
		lineWidth := lipgloss.Width(line)
		left := ansi.Truncate(baseLines[y+i], x, "")
		right := ansi.TruncateLeft(baseLines[y+i], x+lineWidth, "")
		baseLines[y+i] = left + line + right
	}

	return strings.Join(baseLines, "\n")
}

func normalizeOverlayCanvas(content string, width, height int) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}

	canvas := make([]string, 0, height)
	for _, line := range lines {
		line = ansi.Truncate(line, width, "")
		if lipgloss.Width(line) < width {
			line += strings.Repeat(" ", width-lipgloss.Width(line))
		}
		canvas = append(canvas, line)
	}
	for len(canvas) < height {
		canvas = append(canvas, strings.Repeat(" ", width))
	}
	return canvas
}
