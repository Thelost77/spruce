package playlists

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.loading {
		status := m.styles.Muted.Render("loading playlists...")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, status)
	}
	if m.err != nil {
		errStr := m.styles.Error.Render(fmt.Sprintf("error loading playlists: %v", m.err))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, errStr)
	}
	if m.level == LevelTracks {
		return m.trackList.View()
	}
	if len(m.playlistList.Items()) == 0 {
		empty := m.styles.Muted.Render("No playlists found.")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, empty)
	}
	return m.playlistList.View()
}
