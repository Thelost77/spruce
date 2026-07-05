package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
	"github.com/Thelost77/spruce/internal/screens/metadataedit"
	"github.com/Thelost77/spruce/internal/screens/queue"
	"github.com/Thelost77/spruce/internal/secrets"
	"github.com/Thelost77/spruce/internal/ui"
	"github.com/Thelost77/spruce/internal/ui/components"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

type musicLibrariesLoadedMsg struct {
	libraries []jellyfin.Library
	err       error
}

type Model struct {
	screen    Screen
	backStack []Screen
	keys      KeyMap

	loginScreen        login.Model
	libraryScreen      library.Model
	queueScreen        queue.Model
	metadataEditScreen metadataedit.Model
	palette            components.Palette
	help               components.HelpOverlay
	err                components.ErrorBanner
	repeatTrackID      string
	repeatQueue        bool

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
	playSessionID  string
	lastHeartbeat  time.Time
	lastMprisEmit  time.Time

	sleepDeadline   time.Time
	sleepDuration   time.Duration
	sleepGeneration uint64

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
	actualCfg := config.Default()
	if cfg != nil {
		if cfg.Player.Speed != 0 {
			actualCfg.Player.Speed = cfg.Player.Speed
		}
		if cfg.Player.SeekSeconds != 0 {
			actualCfg.Player.SeekSeconds = cfg.Player.SeekSeconds
		}
		if cfg.Server.Address != "" {
			actualCfg.Server = cfg.Server
		}
		if cfg.Theme.Background != "" {
			actualCfg.Theme = cfg.Theme
		}
		if cfg.Keybinds.Quit != "" {
			actualCfg.Keybinds.Quit = cfg.Keybinds.Quit
		}
		if cfg.Keybinds.PlayPause != "" {
			actualCfg.Keybinds.PlayPause = cfg.Keybinds.PlayPause
		}
		if cfg.Keybinds.SeekForward != "" {
			actualCfg.Keybinds.SeekForward = cfg.Keybinds.SeekForward
		}
		if cfg.Keybinds.SeekBackward != "" {
			actualCfg.Keybinds.SeekBackward = cfg.Keybinds.SeekBackward
		}
		if cfg.Keybinds.NextInQueue != "" {
			actualCfg.Keybinds.NextInQueue = cfg.Keybinds.NextInQueue
		}
		if cfg.Keybinds.SpeedUp != "" {
			actualCfg.Keybinds.SpeedUp = cfg.Keybinds.SpeedUp
		}
		if cfg.Keybinds.SpeedDown != "" {
			actualCfg.Keybinds.SpeedDown = cfg.Keybinds.SpeedDown
		}
		if cfg.Keybinds.VolumeUp != "" {
			actualCfg.Keybinds.VolumeUp = cfg.Keybinds.VolumeUp
		}
		if cfg.Keybinds.VolumeDown != "" {
			actualCfg.Keybinds.VolumeDown = cfg.Keybinds.VolumeDown
		}
		if cfg.Keybinds.NextChapter != "" {
			actualCfg.Keybinds.NextChapter = cfg.Keybinds.NextChapter
		}
		if cfg.Keybinds.PrevChapter != "" {
			actualCfg.Keybinds.PrevChapter = cfg.Keybinds.PrevChapter
		}
		if cfg.Keybinds.SleepTimer != "" {
			actualCfg.Keybinds.SleepTimer = cfg.Keybinds.SleepTimer
		}
		if cfg.Keybinds.Back != "" {
			actualCfg.Keybinds.Back = cfg.Keybinds.Back
		}
	}
	var p player.Player
	if mpv != nil {
		p = mpv
	}
	pal := components.NewPalette()
	pal.SetStyles(styles)
	return Model{
		screen:        ScreenLogin,
		keys:          DefaultKeyMap(actualCfg.Keybinds),
		loginScreen:   login.New(styles),
		libraryScreen: library.New(styles),
		queueScreen:   queue.New(styles),
		palette:       pal,
		help:          components.NewHelpOverlay(styles),
		cfg:           cfg,
		mpv:           mpv,
		mprisState:    &MprisState{},
		playerState:   player.NewModel(p, actualCfg, styles),
		err:           components.NewErrorBanner(styles.Error),
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

// screenHeight returns the available height for screen content.
func (m Model) screenHeight() int {
	h := m.height
	h -= headerHeight
	if m.err.HasError() {
		h -= errorBannerHeight
	}
	h -= hintsHeight
	if m.playerState.Title != "" {
		h -= playerFooterHeight
	}
	if h < 0 {
		return 0
	}
	return h
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	w := normalizeViewWidth(width)
	sh := m.screenHeight()
	m.loginScreen.SetSize(w, sh)
	m.libraryScreen.SetSize(w, sh)
	m.queueScreen.SetSize(w, sh)
	m.playerState.SetWidth(w)
	m.err.SetWidth(w)
	m.palette.SetSize(width, height)
	m.help.SetSize(width, height)
}

func (m Model) Init() tea.Cmd {
	if m.cfg != nil && m.cfg.Server.Address != "" && m.cfg.Server.UserID != "" {
		return m.savedLoginCmd()
	}
	return m.loginScreen.Init()
}

func (m Model) savedLoginCmd() tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		token := ""
		if cfg.Server.Token != "" {
			token = cfg.Server.Token
			if err := secrets.SetToken(cfg.Server.Address, cfg.Server.Username, token); err != nil {
				logger.Warn("failed to migrate token to keychain", "err", err)
			} else {
				cfg.Server.Token = ""
				if err := config.Save(filepath.Join(config.ConfigDir(), "config.toml"), *cfg); err != nil {
					logger.Warn("failed to clear migrated token from config", "err", err)
				}
			}
		} else {
			var err error
			token, err = secrets.GetToken(cfg.Server.Address, cfg.Server.Username)
			if err != nil {
				if !errors.Is(err, secrets.ErrNotFound) {
					logger.Warn("failed to load token from keychain", "err", err)
				}
				return nil
			}
		}
		return login.LoginSuccessMsg{
			Token:     token,
			ServerURL: cfg.Server.Address,
			Username:  cfg.Server.Username,
			UserID:    cfg.Server.UserID,
		}
	}
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
		if msg.String() == "ctrl+c" {
			if m.currentIndex >= 0 && m.client != nil {
				newM, stopCmd := m.stopPlayback()
				return newM, tea.Batch(stopCmd, tea.Quit)
			}
			return m, tea.Quit
		}
		if m.screen == ScreenMetadataEdit {
			var cmd tea.Cmd
			m.metadataEditScreen, cmd = m.metadataEditScreen.Update(msg)
			return m, cmd
		}
		if m.screen != ScreenLogin {
			isFiltering := false
			if m.screen == ScreenLibrary && m.libraryScreen.IsFiltering() {
				isFiltering = true
			} else if m.screen == ScreenQueue && m.queueScreen.IsFiltering() {
				isFiltering = true
			}
			if !isFiltering && key.Matches(msg, m.keys.Help) {
				m.help.Toggle()
				return m, nil
			}
			if m.help.Visible() {
				if key.Matches(msg, m.keys.Back) {
					m.help.Hide()
				}
				return m, nil
			}
			if key.Matches(msg, m.keys.GlobalPalette) {
				m.openCommandPalette()
				return m, nil
			}
			if !isFiltering {
				if key.Matches(msg, m.keys.Quit) {
					if m.currentIndex >= 0 && m.client != nil {
						newM, stopCmd := m.stopPlayback()
						return newM, tea.Batch(stopCmd, tea.Quit)
					}
					return m, tea.Quit
				}
				if m.IsPlaying() {
					if key.Matches(msg, m.keys.SleepTimer) {
						return m.cycleSleepTimer()
					}
					if key.Matches(msg, m.playerState.SeekForwardKey()) {
						seek := 10.0
						if m.cfg != nil {
							seek = float64(m.cfg.Player.SeekSeconds)
						}
						return m.handleSeek(seek)
					}
					if key.Matches(msg, m.playerState.SeekBackKey()) {
						seek := 10.0
						if m.cfg != nil {
							seek = float64(m.cfg.Player.SeekSeconds)
						}
						return m.handleSeek(-seek)
					}
					if key.Matches(msg, m.keys.NextTrack) {
						if len(m.tracks) > 0 && m.currentIndex+1 < len(m.tracks) {
							return m.startPlaybackAt(m.nextIndex(m.currentIndex + 1))
						}
						return m, nil
					}
					if key.Matches(msg, m.keys.PrevTrack) {
						if m.playerState.Position > 3.0 {
							return m.handleSeek(-m.playerState.Position)
						}
						if len(m.tracks) > 0 && m.currentIndex-1 >= 0 {
							return m.startPlaybackAt(m.currentIndex - 1)
						}
						return m, nil
					}
					if key.Matches(msg, m.keys.RepeatTrack) {
						return m.Update(queue.QueueActionMsg{
							Action:  "repeat_track",
							Index:   m.currentIndex,
							TrackID: m.tracks[m.currentIndex].ID,
						})
					}
					if key.Matches(msg, m.keys.RepeatQueue) {
						return m.Update(queue.QueueActionMsg{Action: "repeat_queue"})
					}
					oldSpeed := m.playerState.Speed
					oldVol := m.playerState.Volume
					oldPlaying := m.playerState.Playing
					newPlayer, playerCmd := m.playerState.Update(msg)
					m.playerState = newPlayer
					if newPlayer.Speed != oldSpeed || newPlayer.Volume != oldVol || newPlayer.Playing != oldPlaying || playerCmd != nil {
						m.syncQueueScreen()
					}
					if playerCmd != nil {
						return m, playerCmd
					}
				}
			}
		}

		if msg.String() == "tab" && m.screen != ScreenLogin {
			if m.screen == ScreenLibrary {
				m.screen = ScreenQueue
				m.propagateSize()
				return m, m.initScreen(ScreenQueue)
			}
			if m.screen == ScreenQueue {
				m.screen = ScreenLibrary
				m.propagateSize()
				return m, m.initScreen(ScreenLibrary)
			}
		}
		if key.Matches(msg, m.keys.Back) && m.screen != ScreenLogin {
			if m.screen == ScreenQueue {
				if m.queueScreen.IsFiltering() || m.queueScreen.HasActiveFilter() {
					var cmd tea.Cmd
					m.queueScreen, cmd = m.queueScreen.Update(msg)
					return m, cmd
				}
				return m, nil
			}
			if m.screen == ScreenLibrary && (m.libraryScreen.IsFiltering() || m.libraryScreen.HasActiveFilter() || m.libraryScreen.CurrentLevel() == library.LevelTracks) {
				var cmd tea.Cmd
				m.libraryScreen, cmd = m.libraryScreen.Update(msg)
				return m, cmd
			}
			return m.back()
		}

	case NavigateMsg:
		return m.navigate(msg.Target)

	case metadataedit.SavedMsg:
		var cmd tea.Cmd
		m.metadataEditScreen, cmd = m.metadataEditScreen.Update(msg)
		if msg.Err == nil {
			for i := range m.tracks {
				if m.tracks[i].ID == msg.ItemID {
					if msg.Req.Name != "" {
						m.tracks[i].Name = msg.Req.Name
					}
					if msg.Req.Artists != nil {
						m.tracks[i].Artists = msg.Req.Artists
					}
					if msg.Req.Album != "" {
						m.tracks[i].Album = msg.Req.Album
					}
					if i == m.currentIndex {
						m.playerState.Title = m.tracks[i].Name
					}
				}
			}
			m.libraryScreen.PatchTrack(msg.ItemID, msg.Req.Name, msg.Req.Artists, msg.Req.Album)
			m.syncQueueScreen()
		}
		return m, cmd

	case BackMsg, metadataedit.BackMsg:
		return m.back()

	case login.LoginSuccessMsg:
		logger.Info("login succeeded, setting client", "user", msg.Username)
		if m.cfg != nil {
			m.cfg.Server.Address = msg.ServerURL
			m.cfg.Server.Username = msg.Username
			m.cfg.Server.UserID = msg.UserID
			m.cfg.Server.Token = ""
			if err := secrets.SetToken(msg.ServerURL, msg.Username, msg.Token); err != nil {
				logger.Warn("failed to save token to keychain", "err", err)
			}
			if err := config.Save(filepath.Join(config.ConfigDir(), "config.toml"), *m.cfg); err != nil {
				logger.Warn("failed to save config", "err", err)
			}
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
		if msg.Err != nil {
			return m, m.err.SetError(msg.Err)
		}
		return m, nil

	case library.PlayTracksMsg:
		logger.Info("received PlayTracksMsg", "count", len(msg.Tracks), "start", msg.StartIndex)
		if len(msg.Tracks) > 0 {
			m.tracks = append(msg.Tracks, m.tracks...)
		}
		newM, cmd := m.startPlaybackAt(msg.StartIndex)
		return newM, cmd

	case library.AddTrackToQueueMsg:
		logger.Info("adding track to queue", "title", msg.Track.Name)
		wasEmpty := !m.IsPlaying()
		m.tracks = append(m.tracks, msg.Track)
		if wasEmpty {
			return m.startPlaybackAt(len(m.tracks) - 1)
		}
		m.syncQueueScreen()
		return m, nil

	case library.AddTracksToQueueMsg:
		logger.Info("adding tracks to queue", "count", len(msg.Tracks))
		wasEmpty := !m.IsPlaying()
		firstIdx := len(m.tracks)
		m.tracks = append(m.tracks, msg.Tracks...)
		if wasEmpty && len(m.tracks) > 0 {
			return m.startPlaybackAt(firstIdx)
		}
		m.syncQueueScreen()
		return m, nil

	case library.AddShuffledTracksToQueueMsg:
		logger.Info("adding shuffled tracks to queue", "count", len(msg.Tracks))
		wasEmpty := !m.IsPlaying()
		firstIdx := len(m.tracks)
		shuffled := make([]jellyfin.Track, len(msg.Tracks))
		copy(shuffled, msg.Tracks)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		m.tracks = append(m.tracks, shuffled...)
		if wasEmpty && len(m.tracks) > 0 {
			return m.startPlaybackAt(firstIdx)
		}
		m.syncQueueScreen()
		return m, nil

	case library.EditMetadataMsg:
		m.metadataEditScreen = metadataedit.New(m.styles, m.client, msg.ItemID, msg.ItemType, msg.Track, msg.Album)
		m.metadataEditScreen.SetSize(m.width, m.screenHeight())
		return m.navigate(ScreenMetadataEdit)

	case SleepTimerExpiredMsg:
		if m.IsPlaying() && !m.sleepDeadline.IsZero() && msg.Generation == m.sleepGeneration {
			logger.Info("sleep timer expired, stopping playback")
			m.sleepDeadline = time.Time{}
			m.sleepDuration = 0
			m.playerState.SleepRemaining = ""
			return m.stopPlayback()
		}
		return m, nil

	case player.PositionMsg:
		newM, cmd := m.handlePositionMsg(msg)
		return newM, cmd

	case player.PlayerReadyMsg:
		var cmds []tea.Cmd
		if m.mpv != nil {
			cmds = append(cmds, player.TickCmd(m.mpv, m.playGeneration))
			cmds = append(cmds, m.mpv.WatchEvents(m.playGeneration))
			cmds = append(cmds, player.SetVolumeCmd(m.mpv, m.playerState.Volume))
			cmds = append(cmds, player.SetSpeedCmd(m.mpv, m.playerState.Speed))
		}
		return m, tea.Batch(cmds...)

	case player.PlayerEndMsg:
		if msg.Generation != m.playGeneration {
			return m, nil
		}
		if msg.Reason == "eof" {
			if m.repeatTrackID != "" {
				for idx, t := range m.tracks {
					if t.ID == m.repeatTrackID {
						return m.startPlaybackAt(idx)
					}
				}
			}
			nextIdx := m.currentIndex + 1
			if nextIdx < len(m.tracks) {
				return m.startPlaybackAt(nextIdx)
			}
			if m.repeatQueue && len(m.tracks) > 0 {
				return m.startPlaybackAt(0)
			}
			return m.stopPlayback()
		}
		logger.Error("player ended with non-eof reason", "reason", msg.Reason, "err", msg.Err)
		newM, cmd := m.stopPlayback()
		if msg.Err != nil {
			errCmd := newM.err.SetError(msg.Err)
			return newM, tea.Batch(cmd, errCmd)
		}
		return newM, cmd

	case player.PlayerLaunchErrMsg:
		logger.Error("player failed to launch", "err", msg.Err)
		newM, cmd := m.stopPlayback()
		errCmd := newM.err.SetError(msg.Err)
		return newM, tea.Batch(cmd, errCmd)

	case components.ErrorDismissMsg:
		m.err.Dismiss()
		return m, nil

	case queue.JumpQueueMsg:
		newM, cmd := m.startPlaybackAt(msg.Index)
		return newM, cmd

	case queue.RemoveQueueMsg:
		if msg.Index >= 0 && msg.Index < len(m.tracks) {
			if m.tracks[msg.Index].ID == m.repeatTrackID {
				m.repeatTrackID = ""
				m.playerState.RepeatStatus = ""
			}
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
		case "edit_metadata":
			if msg.Index >= 0 && msg.Index < len(m.tracks) {
				trk := m.tracks[msg.Index]
				m.metadataEditScreen = metadataedit.New(m.styles, m.client, trk.ID, "Track", &trk, nil)
				m.metadataEditScreen.SetSize(m.width, m.screenHeight())
				return m.navigate(ScreenMetadataEdit)
			}
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
			if len(m.tracks) > 1 {
				currentID := ""
				if m.currentIndex >= 0 && m.currentIndex < len(m.tracks) {
					currentID = m.tracks[m.currentIndex].ID
				}
				rand.Shuffle(len(m.tracks), func(i, j int) {
					m.tracks[i], m.tracks[j] = m.tracks[j], m.tracks[i]
				})
				if currentID != "" {
					for idx, t := range m.tracks {
						if t.ID == currentID {
							m.currentIndex = idx
							break
						}
					}
				}
				m.syncQueueScreen()
			}
			return m, nil
		case "repeat_track":
			if msg.TrackID != "" {
				if m.repeatTrackID == msg.TrackID {
					m.repeatTrackID = ""
					m.playerState.RepeatStatus = ""
				} else {
					m.repeatTrackID = msg.TrackID
					m.repeatQueue = false
					m.playerState.RepeatStatus = "🔂 Track"
				}
				m.syncQueueScreen()
			}
			return m, nil
		case "repeat_queue":
			m.repeatQueue = !m.repeatQueue
			if m.repeatQueue {
				m.repeatTrackID = ""
				m.playerState.RepeatStatus = "🔁 Queue"
			} else {
				m.playerState.RepeatStatus = ""
			}
			m.syncQueueScreen()
			return m, nil
		case "clear":
			m.repeatTrackID = ""
			m.repeatQueue = false
			m.playerState.RepeatStatus = ""
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
		m.syncQueueScreen()
		if m.mpv != nil {
			return m, player.SetVolumeCmd(m.mpv, msg.Volume)
		}

	case mpris.SetRateMsg:
		m.playerState.Speed = msg.Rate
		m.syncQueueScreen()
		if m.mpv != nil {
			return m, player.SetSpeedCmd(m.mpv, msg.Rate)
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
	case ScreenMetadataEdit:
		m.metadataEditScreen, cmd = m.metadataEditScreen.Update(msg)
	}

	return m, cmd
}

// View is defined in render.go.

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

func (a mprisStateAccessor) IsPlaying() bool     { return a.s != nil && a.s.IsPlaying }
func (a mprisStateAccessor) IsPaused() bool      { return a.s != nil && a.s.IsPaused }
func (a mprisStateAccessor) HasActiveItem() bool { return a.s != nil && a.s.IsPlaying }
func (a mprisStateAccessor) CurrentTitle() string {
	if a.s != nil {
		return a.s.Title
	}
	return ""
}
func (a mprisStateAccessor) CurrentAuthors() []string {
	if a.s != nil {
		return a.s.Authors
	}
	return nil
}
func (a mprisStateAccessor) CurrentItemID() string {
	if a.s != nil {
		return a.s.ItemID
	}
	return ""
}
func (a mprisStateAccessor) PlayerPosition() float64 {
	if a.s != nil {
		return a.s.Position
	}
	return 0
}
func (a mprisStateAccessor) PlayerDuration() float64 {
	if a.s != nil {
		return a.s.Duration
	}
	return 0
}
func (a mprisStateAccessor) PlayerSpeed() float64 {
	if a.s != nil {
		return a.s.Speed
	}
	return 1.0
}
func (a mprisStateAccessor) PlayerVolume() int {
	if a.s != nil {
		return a.s.Volume
	}
	return 100
}
func (a mprisStateAccessor) QueueLength() int {
	if a.s != nil {
		return a.s.QueueLen
	}
	return 0
}

func (m Model) nextIndex(defaultNext int) int {
	if m.repeatTrackID != "" {
		for idx, t := range m.tracks {
			if t.ID == m.repeatTrackID {
				return idx
			}
		}
	}
	if defaultNext >= len(m.tracks) && m.repeatQueue && len(m.tracks) > 0 {
		return 0
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

		for _, a := range m.libraryScreen.Albums() {
			if strings.Contains(strings.ToLower(a.Name), query) {
				artist := "Unknown Artist"
				if len(a.Artists) > 0 {
					artist = strings.Join(a.Artists, ", ")
				}
				results = append(results, components.PaletteItem{
					Label:  fmt.Sprintf("Album: %s — %s", a.Name, artist),
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
		if album, ok := data.(jellyfin.Album); ok {
			m.screen = ScreenLibrary
			cmd := m.libraryScreen.SelectAlbum(album)
			return m, cmd
		}
	case components.ActionPlayDirect:
		if track, ok := data.(jellyfin.Track); ok {
			m.tracks = append(m.tracks, track)
			return m.startPlaybackAt(len(m.tracks) - 1)
		}
	case components.ActionSleep15:
		return m.setSleepTimer(15 * time.Minute)
	case components.ActionSleep30:
		return m.setSleepTimer(30 * time.Minute)
	case components.ActionSleep45:
		return m.setSleepTimer(45 * time.Minute)
	case components.ActionSleep60:
		return m.setSleepTimer(60 * time.Minute)
	case components.ActionSleepOff:
		return m.setSleepTimer(0)
	}
	return m, nil
}

var sleepDurations = []time.Duration{
	0,
	15 * time.Minute,
	30 * time.Minute,
	45 * time.Minute,
	60 * time.Minute,
}

func (m Model) cycleSleepTimer() (Model, tea.Cmd) {
	nextIdx := 0
	for i, d := range sleepDurations {
		if d == m.sleepDuration {
			nextIdx = (i + 1) % len(sleepDurations)
			break
		}
	}
	return m.setSleepTimer(sleepDurations[nextIdx])
}

func (m Model) setSleepTimer(d time.Duration) (Model, tea.Cmd) {
	m.sleepDuration = d
	if d == 0 {
		m.sleepDeadline = time.Time{}
		m.playerState.SleepRemaining = ""
		logger.Info("sleep timer disabled")
		return m, nil
	}
	m.sleepGeneration++
	m.sleepDeadline = time.Now().Add(d)
	m.playerState.SleepRemaining = formatSleepRemaining(d)
	logger.Info("sleep timer set", "duration", d)
	return m, sleepTimerCmd(d, m.sleepGeneration)
}

func sleepTimerCmd(d time.Duration, generation uint64) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return SleepTimerExpiredMsg{Generation: generation}
	})
}

func formatSleepRemaining(d time.Duration) string {
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", mins, secs)
}
