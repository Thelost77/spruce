package app

import (
	"context"
	"strconv"
	"time"

	"github.com/quarckster/go-mpris-server/pkg/types"

	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/player"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) startPlaybackAt(index int) (Model, tea.Cmd) {
	if index < 0 || index >= len(m.tracks) {
		return m.stopPlayback()
	}

	m.currentIndex = index
	m.playGeneration++
	track := m.tracks[index]
	m.playerState.Title = track.Name
	m.playerState.Position = 0
	m.playerState.Duration = track.Duration()
	m.playerState.Playing = true
	m.lastHeartbeat = time.Now()

	logger.Info("starting track playback", "index", index, "id", track.ID, "title", track.Name)

	m.syncQueueScreen()

	var cmds []tea.Cmd
	if m.client != nil {
		playSessionID := strconv.FormatInt(time.Now().UnixNano(), 16)
		m.playSessionID = playSessionID
		url := m.client.StreamURL(track.ID, playSessionID)
		client := m.client
		itemID := track.ID

		startReqCmd := func() tea.Msg {
			_ = client.ReportPlaybackStart(context.Background(), itemID, playSessionID)
			return nil
		}
		cmds = append(cmds, startReqCmd)

		if m.mpv != nil {
			cmds = append(cmds, player.LaunchCmd(m.mpv, url, 0, false, nil))
		}
	}

	return m, tea.Batch(cmds...)
}

func (m Model) stopPlayback() (Model, tea.Cmd) {
	logger.Info("stopping playback")
	m.playGeneration++
	m.playerState.Title = ""
	m.playerState.Playing = false
	m.playerState.Position = 0
	m.sleepDeadline = time.Time{}
	m.sleepDuration = 0
	m.playerState.SleepRemaining = ""

	var cmds []tea.Cmd
	if m.client != nil && m.currentIndex >= 0 && m.currentIndex < len(m.tracks) {
		client := m.client
		itemID := m.tracks[m.currentIndex].ID
		pos := m.playerState.Position
		playSessionID := m.playSessionID
		stopReqCmd := func() tea.Msg {
			_ = client.ReportPlaybackStopped(context.Background(), itemID, pos, playSessionID)
			return nil
		}
		cmds = append(cmds, stopReqCmd)
	}

	if m.mpv != nil {
		cmds = append(cmds, player.QuitCmd(m.mpv))
	}

	m.currentIndex = -1
	m.syncQueueScreen()
	return m, tea.Batch(cmds...)
}

func (m Model) handlePositionMsg(msg player.PositionMsg) (Model, tea.Cmd) {
	if msg.Generation != m.playGeneration {
		return m, nil
	}

	if msg.Err != nil {
		logger.Error("player position poll failed (fatal)", "err", msg.Err)
		// Do NOT auto-advance — this is a fatal load/socket error, not EOF.
		// EOF is delivered via PlayerEndMsg{Reason:"eof"}.
		return m.stopPlayback()
	}

	m.playerState.Position = msg.Position
	if msg.Duration > 0 {
		m.playerState.Duration = msg.Duration
	}
	m.playerState.Playing = !msg.Paused

	if !m.sleepDeadline.IsZero() {
		remaining := time.Until(m.sleepDeadline)
		if remaining <= 0 {
			m.playerState.SleepRemaining = ""
		} else {
			m.playerState.SleepRemaining = formatSleepRemaining(remaining)
		}
	}

	m.syncQueueScreen()

	var cmds []tea.Cmd
	if m.mpv != nil {
		cmds = append(cmds, player.TickCmd(m.mpv, m.playGeneration))
	}

	if m.client != nil && time.Since(m.lastHeartbeat) >= 15*time.Second {
		m.lastHeartbeat = time.Now()
		client := m.client
		itemID := m.tracks[m.currentIndex].ID
		pos := m.playerState.Position
		paused := msg.Paused
		playSessionID := m.playSessionID
		heartbeatCmd := func() tea.Msg {
			_ = client.ReportPlaybackProgress(context.Background(), itemID, pos, paused, playSessionID)
			return nil
		}
		cmds = append(cmds, heartbeatCmd)
	}

	return m, tea.Batch(cmds...)
}

func authorsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Model) syncQueueScreen() {
	if !m.queueScreen.IsFiltering() {
		m.queueScreen.SetQueue(m.tracks, m.currentIndex)
	}
	m.queueScreen.SetPlaybackState(
		m.IsPlaying(),
		m.IsPaused(),
		m.playerState.Position,
		m.playerState.Duration,
	)
	if m.mprisState != nil {
		oldState := *m.mprisState
		m.mprisState.IsPlaying = m.IsPlaying()
		m.mprisState.IsPaused = m.IsPaused()
		if m.IsPlaying() {
			m.mprisState.Title = m.tracks[m.currentIndex].Name
			m.mprisState.Authors = m.tracks[m.currentIndex].Artists
			m.mprisState.ItemID = m.tracks[m.currentIndex].ID
			if m.playerState.Duration <= 0 {
				m.mprisState.Duration = m.tracks[m.currentIndex].Duration()
			} else {
				m.mprisState.Duration = m.playerState.Duration
			}
		} else {
			m.mprisState.Title = ""
			m.mprisState.Authors = nil
			m.mprisState.ItemID = ""
			m.mprisState.Duration = 0
		}
		m.mprisState.Position = m.playerState.Position
		m.mprisState.Speed = m.playerState.Speed
		m.mprisState.Volume = m.playerState.Volume
		m.mprisState.QueueLen = len(m.tracks)

		if m.mprisBridge != nil {
			handler := m.mprisBridge.EventHandler()
			if handler != nil && handler.Player != nil {
				playbackChanged := oldState.IsPlaying != m.mprisState.IsPlaying ||
					oldState.IsPaused != m.mprisState.IsPaused ||
					oldState.Speed != m.mprisState.Speed
				metadataChanged := oldState.Title != m.mprisState.Title ||
					oldState.ItemID != m.mprisState.ItemID ||
					!authorsEqual(oldState.Authors, m.mprisState.Authors)
				volumeChanged := oldState.Volume != m.mprisState.Volume
				positionChanged := oldState.Position != m.mprisState.Position

				if playbackChanged {
					_ = handler.Player.OnPlayback()
				}
				if metadataChanged {
					_ = handler.Player.OnTitle()
				}
				if volumeChanged {
					_ = handler.Player.OnVolume()
				}
				if positionChanged {
					now := time.Now()
					if now.Sub(m.lastMprisEmit) >= time.Second {
						m.lastMprisEmit = now
						pos := types.Microseconds(m.mprisState.Position * 1_000_000)
						_ = handler.Player.OnSeek(pos)
					}
				}
			}
		}
	}
	m.propagateSize()
}

func (m Model) handleSeek(offset float64) (Model, tea.Cmd) {
	if m.mpv == nil || !m.IsPlaying() {
		return m, nil
	}
	target := m.playerState.Position + offset
	if target < 0 {
		target = 0
	}
	if m.playerState.Duration > 0 && target > m.playerState.Duration {
		target = m.playerState.Duration
	}
	m.playerState.Position = target
	m.syncQueueScreen()
	return m, player.SeekCmd(m.mpv, target)
}

func (m *Model) mprisPlaybackCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	return func() tea.Msg {
		_ = handler.Player.OnPlayback()
		return nil
	}
}

func (m *Model) mprisPlayPauseCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	return func() tea.Msg {
		_ = handler.Player.OnPlayPause()
		return nil
	}
}

func (m *Model) mprisEndedCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	return func() tea.Msg {
		_ = handler.Player.OnEnded()
		return nil
	}
}

func (m *Model) mprisTitleCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	return func() tea.Msg {
		_ = handler.Player.OnTitle()
		return nil
	}
}

func (m *Model) mprisPositionCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	now := time.Now()
	if now.Sub(m.lastMprisEmit) < time.Second {
		return nil
	}
	m.lastMprisEmit = now
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	pos := types.Microseconds(m.playerState.Position * 1_000_000)
	return func() tea.Msg {
		_ = handler.Player.OnSeek(pos)
		return nil
	}
}

func (m *Model) mprisVolumeCmd() tea.Cmd {
	if m.mprisBridge == nil {
		return nil
	}
	handler := m.mprisBridge.EventHandler()
	if handler == nil || handler.Player == nil {
		return nil
	}
	return func() tea.Msg {
		_ = handler.Player.OnVolume()
		return nil
	}
}
