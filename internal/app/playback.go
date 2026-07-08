package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/quarckster/go-mpris-server/pkg/types"

	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/player"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) startPlaybackAt(index int) (*Model, tea.Cmd) {
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
			cmds = append(cmds, player.LaunchCmd(m.mpv, url, 0, false, nil, m.playerState.Volume))
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) stopPlayback() (*Model, tea.Cmd) {
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

func (m *Model) handlePositionMsg(msg player.PositionMsg) (*Model, tea.Cmd) {
	if msg.Generation != m.playGeneration {
		return m, nil
	}

	if msg.Err != nil {
		if isMpvPropertyUnavailable(msg.Err) {
			logger.Warn("player position poll temporarily unavailable", "err", msg.Err)
			if m.mpv != nil {
				return m, player.TickCmd(m.mpv, m.playGeneration)
			}
			return m, nil
		}
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

	if m.client != nil && m.currentIndex >= 0 && m.currentIndex < len(m.tracks) && time.Since(m.lastHeartbeat) >= 15*time.Second {
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

func isMpvPropertyUnavailable(err error) bool {
	return strings.Contains(err.Error(), "property unavailable")
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
	if !m.queueScreen.IsFiltering() && !m.queueScreen.HasActiveFilter() {
		m.queueScreen.SetQueue(m.tracks, m.currentIndex)
	}
	m.queueScreen.SetPlaybackState(
		m.IsPlaying(),
		m.IsPaused(),
		m.playerState.Position,
		m.playerState.Duration,
	)
	if m.mprisState != nil {
		m.mprisState.mu.Lock()
		oldState := *m.mprisState
		m.mprisState.IsPlaying = m.IsPlaying()
		m.mprisState.IsPaused = m.IsPaused()
		if m.IsPlaying() && m.currentIndex >= 0 && m.currentIndex < len(m.tracks) {
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
		newState := *m.mprisState
		m.mprisState.mu.Unlock()

		if m.mprisBridge != nil {
			handler := m.mprisBridge.EventHandler()
			if handler != nil && handler.Player != nil {
				playbackChanged := oldState.IsPlaying != newState.IsPlaying ||
					oldState.IsPaused != newState.IsPaused ||
					oldState.Speed != newState.Speed
				metadataChanged := oldState.Title != newState.Title ||
					oldState.ItemID != newState.ItemID ||
					!authorsEqual(oldState.Authors, newState.Authors)
				volumeChanged := oldState.Volume != newState.Volume
				positionChanged := oldState.Position != newState.Position

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
						pos := types.Microseconds(newState.Position * 1_000_000)
						_ = handler.Player.OnSeek(pos)
					}
				}
			}
		}
	}
	m.propagateSize()
}

func (m *Model) handleSeek(offset float64) (*Model, tea.Cmd) {
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

func (m *Model) handleSeekAbsolute(pos float64) (*Model, tea.Cmd) {
	if m.mpv == nil || !m.IsPlaying() {
		return m, nil
	}
	if pos < 0 {
		pos = 0
	}
	if m.playerState.Duration > 0 && pos > m.playerState.Duration {
		pos = m.playerState.Duration
	}
	m.playerState.Position = pos
	m.syncQueueScreen()
	return m, player.SeekCmd(m.mpv, pos)
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

func (m *Model) handlePlayerEvent(msg tea.Msg) (*Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case SleepTimerExpiredMsg:
		if m.IsPlaying() && !m.sleepDeadline.IsZero() && msg.Generation == m.sleepGeneration {
			logger.Info("sleep timer expired, stopping playback")
			m.sleepDeadline = time.Time{}
			m.sleepDuration = 0
			m.playerState.SleepRemaining = ""
			newM, cmd := m.stopPlayback()
			return newM, cmd, true
		}
		return m, nil, true

	case player.PositionMsg:
		newM, cmd := m.handlePositionMsg(msg)
		return newM, cmd, true

	case player.PlayerReadyMsg:
		var cmds []tea.Cmd
		if m.mpv != nil {
			cmds = append(cmds, player.TickCmd(m.mpv, m.playGeneration))
			cmds = append(cmds, m.mpv.WatchEvents(m.playGeneration))
			cmds = append(cmds, player.SetVolumeCmd(m.mpv, m.playerState.Volume))
			cmds = append(cmds, player.SetSpeedCmd(m.mpv, m.playerState.Speed))
		}
		return m, tea.Batch(cmds...), true

	case player.PlayerEndMsg:
		if msg.Generation != m.playGeneration {
			return m, nil, true
		}
		if msg.Reason == "eof" {
			if m.repeatTrackID != "" {
				for idx, t := range m.tracks {
					if t.ID == m.repeatTrackID {
						newM, cmd := m.startPlaybackAt(idx)
						return newM, cmd, true
					}
				}
			}
			nextIdx := m.currentIndex + 1
			if nextIdx < len(m.tracks) {
				newM, cmd := m.startPlaybackAt(nextIdx)
				return newM, cmd, true
			}
			if m.repeatQueue && len(m.tracks) > 0 {
				newM, cmd := m.startPlaybackAt(0)
				return newM, cmd, true
			}
			newM, cmd := m.stopPlayback()
			return newM, cmd, true
		}
		logger.Error("player ended with non-eof reason", "reason", msg.Reason, "err", msg.Err)
		newM, cmd := m.stopPlayback()
		if msg.Err != nil {
			errCmd := newM.err.SetError(msg.Err)
			return newM, tea.Batch(cmd, errCmd), true
		}
		return newM, cmd, true

	case player.PlayerLaunchErrMsg:
		logger.Error("player failed to launch", "err", msg.Err)
		newM, cmd := m.stopPlayback()
		errCmd := newM.err.SetError(msg.Err)
		return newM, tea.Batch(cmd, errCmd), true
	}
	return m, nil, false
}

var sleepDurations = []time.Duration{
	0,
	15 * time.Minute,
	30 * time.Minute,
	45 * time.Minute,
	60 * time.Minute,
}

func (m *Model) cycleSleepTimer() (*Model, tea.Cmd) {
	nextIdx := 0
	for i, d := range sleepDurations {
		if d == m.sleepDuration {
			nextIdx = (i + 1) % len(sleepDurations)
			break
		}
	}
	return m.setSleepTimer(sleepDurations[nextIdx])
}

func (m *Model) setSleepTimer(d time.Duration) (*Model, tea.Cmd) {
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
