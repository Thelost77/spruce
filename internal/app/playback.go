package app

import (
	"context"
	"time"

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
		url := m.client.StreamURL(track.ID)
		headers := m.client.StreamHeaders()
		client := m.client
		itemID := track.ID

		startReqCmd := func() tea.Msg {
			_ = client.ReportPlaybackStart(context.Background(), itemID)
			return nil
		}
		cmds = append(cmds, startReqCmd)

		if m.mpv != nil {
			cmds = append(cmds, player.LaunchCmd(m.mpv, url, 0, false, headers))
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

	var cmds []tea.Cmd
	if m.client != nil && m.currentIndex >= 0 && m.currentIndex < len(m.tracks) {
		client := m.client
		itemID := m.tracks[m.currentIndex].ID
		pos := m.playerState.Position
		stopReqCmd := func() tea.Msg {
			_ = client.ReportPlaybackStopped(context.Background(), itemID, pos)
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
		logger.Info("track ended or player error, advancing queue", "err", msg.Err)
		nextIdx := m.nextIndex(m.currentIndex + 1)
		if nextIdx < len(m.tracks) && nextIdx != m.currentIndex {
			return m.startPlaybackAt(nextIdx)
		}
		return m.stopPlayback()
	}

	m.playerState.Position = msg.Position
	if msg.Duration > 0 {
		m.playerState.Duration = msg.Duration
	}
	m.playerState.Playing = !msg.Paused

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
		heartbeatCmd := func() tea.Msg {
			_ = client.ReportPlaybackProgress(context.Background(), itemID, pos, paused)
			return nil
		}
		cmds = append(cmds, heartbeatCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) syncQueueScreen() {
	m.queueScreen.SetQueue(m.tracks, m.currentIndex)
	m.queueScreen.SetPlaybackState(
		m.IsPlaying(),
		m.IsPaused(),
		m.playerState.Position,
		m.playerState.Duration,
	)
	if m.mprisState != nil {
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
	}
}
