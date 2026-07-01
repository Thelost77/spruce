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
		case itemTypes == "MusicAlbum" && parentID == "lib-1":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Album `json:"Items"`
				TotalRecordCount int              `json:"TotalRecordCount"`
			}{
				Items:            []jellyfin.Album{{ID: "alb-1", Name: "OK Computer", ProductionYear: 1997, Artists: []string{"Radiohead"}}},
				TotalRecordCount: 1,
			})
		case itemTypes == "Audio" && parentID == "alb-1":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Track `json:"Items"`
				TotalRecordCount int              `json:"TotalRecordCount"`
			}{
				Items: []jellyfin.Track{
					{ID: "t-1", Name: "Airbag", IndexNumber: 1, RunTimeTicks: 2840000000},
					{ID: "t-2", Name: "Paranoid Android", IndexNumber: 2, RunTimeTicks: 3870000000},
				},
				TotalRecordCount: 2,
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

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected init cmd")
	}
	msg := cmd()
	m, _ = m.Update(msg)
	if m.CurrentLevel() != LevelAlbums {
		t.Fatalf("expected LevelAlbums, got %v", m.CurrentLevel())
	}
	if len(m.albumList.Items()) != 1 {
		t.Fatalf("expected 1 album item, got %d", len(m.albumList.Items()))
	}

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

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected play tracks cmd")
	}
	playMsg := cmd()
	ptm, ok := playMsg.(PlayTracksMsg)
	if !ok {
		t.Fatalf("expected PlayTracksMsg, got %T", playMsg)
	}
	if len(ptm.Tracks) != 1 || ptm.StartIndex != 0 {
		t.Errorf("unexpected PlayTracksMsg: %+v", ptm)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.CurrentLevel() != LevelAlbums {
		t.Errorf("expected back to LevelAlbums, got %v", m.CurrentLevel())
	}

	v := m.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestLibraryModel_Actions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		itemTypes := r.URL.Query().Get("IncludeItemTypes")
		parentID := r.URL.Query().Get("ParentId")
		switch {
		case itemTypes == "MusicAlbum" && parentID == "lib-1":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Album `json:"Items"`
				TotalRecordCount int              `json:"TotalRecordCount"`
			}{
				Items:            []jellyfin.Album{{ID: "alb-1", Name: "OK Computer"}},
				TotalRecordCount: 1,
			})
		case itemTypes == "Audio" && parentID == "alb-1":
			_ = json.NewEncoder(w).Encode(struct {
				Items            []jellyfin.Track `json:"Items"`
				TotalRecordCount int              `json:"TotalRecordCount"`
			}{
				Items: []jellyfin.Track{
					{ID: "t-1", Name: "Airbag", IndexNumber: 1},
					{ID: "t-2", Name: "Paranoid Android", IndexNumber: 2},
				},
				TotalRecordCount: 2,
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

	m, _ = m.Update(AlbumsLoadedMsg{Albums: []jellyfin.Album{{ID: "alb-1", Name: "OK Computer"}}})
	if m.CurrentLevel() != LevelAlbums {
		t.Fatalf("expected LevelAlbums, got %v", m.CurrentLevel())
	}

	// Test 'a' at LevelAlbums
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected cmd on 'a' at LevelAlbums")
	}
	addMsg, ok := cmd().(AddTracksToQueueMsg)
	if !ok || len(addMsg.Tracks) != 2 {
		t.Fatalf("expected AddTracksToQueueMsg with 2 tracks, got %T %+v", cmd(), addMsg)
	}

	// Move to LevelTracks
	m, _ = m.Update(TracksLoadedMsg{Tracks: []jellyfin.Track{{ID: "t-1", Name: "Airbag"}, {ID: "t-2", Name: "Paranoid Android"}}})
	if m.CurrentLevel() != LevelTracks {
		t.Fatalf("expected LevelTracks, got %v", m.CurrentLevel())
	}

	// Test 'a' at LevelTracks
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd == nil {
		t.Fatal("expected cmd on 'a' at LevelTracks")
	}
	addOne, ok := cmd().(AddTrackToQueueMsg)
	if !ok || addOne.Track.ID != "t-1" {
		t.Fatalf("expected AddTrackToQueueMsg for t-1, got %T %+v", cmd(), addOne)
	}

	// Test 'A' at LevelTracks
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if cmd == nil {
		t.Fatal("expected cmd on 'A' at LevelTracks")
	}
	addAll, ok := cmd().(AddTracksToQueueMsg)
	if !ok || len(addAll.Tracks) != 2 {
		t.Fatalf("expected AddTracksToQueueMsg with 2 tracks, got %T %+v", cmd(), addAll)
	}

	// Test Esc resets filter when active
	if m.HasActiveFilter() {
		t.Fatal("expected no active filter initially")
	}
}
