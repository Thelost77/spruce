package queue

import (
	"fmt"

	"github.com/Thelost77/spruce/internal/ui"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if len(m.tracks) == 0 {
		emptyStr := m.styles.Muted.Render("Queue is empty. Select tracks in the Library browser to play.")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, emptyStr)
	}

	var header string
	if m.isPlaying && m.currentIndex >= 0 && m.currentIndex < len(m.tracks) {
		cur := m.tracks[m.currentIndex]
		statusIcon := "▶"
		if m.isPaused {
			statusIcon = "⏸"
		}
		pos := ui.FormatTimestamp(m.positionSeconds)
		dur := ui.FormatTimestamp(m.durationSeconds)
		if m.durationSeconds <= 0 {
			dur = ui.FormatTimestamp(cur.Duration())
		}

		shuffleStr := ""
		if m.isShuffle {
			shuffleStr = " " + m.styles.Accent.Render("🔀 [Shuffle]")
		}

		titleStr := m.styles.Accent.Render(fmt.Sprintf("%s %s", statusIcon, cur.Name)) + shuffleStr
		artistStr := m.styles.Muted.Render(cur.DisplayArtist())
		timeStr := m.styles.Muted.Render(fmt.Sprintf("[%s / %s]", pos, dur))

		header = lipgloss.JoinVertical(lipgloss.Left,
			titleStr,
			lipgloss.JoinHorizontal(lipgloss.Left, artistStr, " • ", timeStr),
		) + "\n\n"
	}

	listView := m.list.View()
	content := lipgloss.JoinVertical(lipgloss.Left, header, listView)
	return content
}
