package app

import (
	"context"
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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	return Model{
		screen:        ScreenLogin,
		loginScreen:   login.New(styles),
		libraryScreen: library.New(styles),
		queueScreen:   queue.New(styles),
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

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.loginScreen.SetSize(width, height)
	m.libraryScreen.SetSize(width, height)
	m.queueScreen.SetSize(width, height)
}

func (m Model) Init() tea.Cmd {
	return m.loginScreen.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
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
			return m, m.libraryScreen.Init()
		}

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
			newM, cmd := m.startPlaybackAt(m.currentIndex + 1)
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
		newM, cmd := m.startPlaybackAt(m.currentIndex + 1)
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
		return content
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
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
