package playlists

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/screens/library"
	"github.com/Thelost77/spruce/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPlaylistsModel_NavigationAndQueueActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/usr/Items":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Playlist `json:"Items"`
				TotalRecordCount int                 `json:"TotalRecordCount"`
			}{
				Items:            []jellyfin.Playlist{{ID: "pl-1", Name: "Road Trip", Count: 2}},
				TotalRecordCount: 1,
			})
		case "/Playlists/pl-1/Items":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Track `json:"Items"`
				TotalRecordCount int              `json:"TotalRecordCount"`
			}{
				Items: []jellyfin.Track{
					{ID: "t-1", Name: "One"},
					{ID: "t-2", Name: "Two"},
				},
				TotalRecordCount: 2,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := New(ui.DefaultStyles())
	m.SetSize(80, 24)
	m.SetClient(jellyfin.NewClient(server.URL, "tok", "usr"))

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected playlists init cmd")
	}
	m, _ = m.Update(cmd())
	if len(m.Playlists()) != 1 || m.Playlists()[0].Name != "Road Trip" {
		t.Fatalf("unexpected playlists: %+v", m.Playlists())
	}

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected playlist tracks cmd")
	}
	m, _ = m.Update(cmd())
	if m.CurrentLevel() != LevelTracks || len(m.Tracks()) != 2 {
		t.Fatalf("expected tracks level with 2 tracks, got level=%v tracks=%+v", m.CurrentLevel(), m.Tracks())
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	msg := cmd()
	addOne, ok := msg.(library.AddTrackToQueueMsg)
	if !ok || addOne.Track.ID != "t-1" {
		t.Fatalf("a returned %T %+v, want AddTrackToQueueMsg t-1", msg, addOne)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	msg = cmd()
	addAll, ok := msg.(library.AddTracksToQueueMsg)
	if !ok || len(addAll.Tracks) != 2 {
		t.Fatalf("A returned %T %+v, want AddTracksToQueueMsg with 2 tracks", msg, addAll)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	msg = cmd()
	addShuffled, ok := msg.(library.AddShuffledTracksToQueueMsg)
	if !ok || len(addShuffled.Tracks) != 2 {
		t.Fatalf("S returned %T %+v, want AddShuffledTracksToQueueMsg with 2 tracks", msg, addShuffled)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.CurrentLevel() != LevelPlaylists {
		t.Fatalf("Esc should return to playlist level, got %v", m.CurrentLevel())
	}
}
