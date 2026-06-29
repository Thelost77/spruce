package library

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func TestLibraryModel_NavigationAndPlayback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		itemTypes := r.URL.Query().Get("IncludeItemTypes")
		parentID := r.URL.Query().Get("ParentId")

		switch {
		case itemTypes == "MusicArtist,Artist" && parentID == "lib-1":
			_ = json.NewEncoder(w).Encode(struct{ Items []jellyfin.Artist }{
				Items: []jellyfin.Artist{{ID: "art-1", Name: "Radiohead"}},
			})
		case itemTypes == "MusicAlbum" && parentID == "art-1":
			_ = json.NewEncoder(w).Encode(struct{ Items []jellyfin.Album }{
				Items: []jellyfin.Album{{ID: "alb-1", Name: "OK Computer", ProductionYear: 1997}},
			})
		case itemTypes == "Audio" && parentID == "alb-1":
			_ = json.NewEncoder(w).Encode(struct{ Items []jellyfin.Track }{
				Items: []jellyfin.Track{
					{ID: "t-1", Name: "Airbag", IndexNumber: 1, RunTimeTicks: 2840000000},
					{ID: "t-2", Name: "Paranoid Android", IndexNumber: 2, RunTimeTicks: 3870000000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := jellyfin.NewClient(server.URL, "tok", "usr")
	m := New(ui.DefaultStyles())
	m.SetSize(80, 24)
	m.SetClient(client, "lib-1")

	// 1. Init should fetch artists
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init cmd")
	}
	msg := cmd()
	m, _ = m.Update(msg)
	if m.CurrentLevel() != LevelArtists {
		t.Fatalf("expected LevelArtists, got %v", m.CurrentLevel())
	}
	if len(m.artistList.Items()) != 1 {
		t.Fatalf("expected 1 artist item, got %d", len(m.artistList.Items()))
	}

	// 2. Select artist -> fetch albums
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fetch albums cmd")
	}
	msg = cmd()
	m, _ = m.Update(msg)
	if m.CurrentLevel() != LevelAlbums {
		t.Fatalf("expected LevelAlbums, got %v", m.CurrentLevel())
	}
	if len(m.albumList.Items()) != 1 {
		t.Fatalf("expected 1 album item, got %d", len(m.albumList.Items()))
	}

	// 3. Select album -> fetch tracks
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fetch tracks cmd")
	}
	msg = cmd()
	m, _ = m.Update(msg)
	if m.CurrentLevel() != LevelTracks {
		t.Fatalf("expected LevelTracks, got %v", m.CurrentLevel())
	}
	if len(m.trackList.Items()) != 2 {
		t.Fatalf("expected 2 track items, got %d", len(m.trackList.Items()))
	}

	// 4. Select track -> PlayTracksMsg
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected play tracks cmd")
	}
	playMsg := cmd()
	ptm, ok := playMsg.(PlayTracksMsg)
	if !ok {
		t.Fatalf("expected PlayTracksMsg, got %T", playMsg)
	}
	if len(ptm.Tracks) != 2 || ptm.StartIndex != 0 {
		t.Errorf("unexpected PlayTracksMsg: %+v", ptm)
	}

	// 5. Test Back navigation (esc / h / left)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.CurrentLevel() != LevelAlbums {
		t.Errorf("expected back to LevelAlbums, got %v", m.CurrentLevel())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.CurrentLevel() != LevelArtists {
		t.Errorf("expected back to LevelArtists, got %v", m.CurrentLevel())
	}

	// Verify view rendering
	v := m.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}
