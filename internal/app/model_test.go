package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/mpris"
	"github.com/Thelost77/spruce/internal/player"
	"github.com/Thelost77/spruce/internal/screens/library"
	"github.com/Thelost77/spruce/internal/screens/login"
	"github.com/Thelost77/spruce/internal/screens/queue"
	"github.com/Thelost77/spruce/internal/ui/components"
	tea "github.com/charmbracelet/bubbletea"
)

func TestAppModel_LifecycleAndMPRIS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Users/AuthenticateByName" {
			_ = json.NewEncoder(w).Encode(jellyfin.AuthResponse{
				User:        jellyfin.User{ID: "usr-1", Name: "alice"},
				AccessToken: "tok-1",
			})
			return
		}
		if r.URL.Path == "/Users/usr-1/Views" {
			_ = json.NewEncoder(w).Encode(struct{ Items []jellyfin.Library }{
				Items: []jellyfin.Library{{ID: "lib-1", Name: "Music", CollectionType: "music"}},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	m := New(nil, nil)
	m.SetSize(80, 24)

	// Test LoginSuccessMsg -> triggers music libraries fetch
	loginMsg := login.LoginSuccessMsg{Token: "tok-1", ServerURL: server.URL, Username: "alice", UserID: "usr-1"}
	newM, cmd := m.Update(loginMsg)
	m = newM.(Model)
	if m.client == nil {
		t.Fatal("expected client set after login")
	}
	if cmd == nil {
		t.Fatal("expected fetch libs cmd")
	}

	// Wait, tea.Batch returns a batch cmd, let's test musicLibrariesLoadedMsg directly
	libsMsg := musicLibrariesLoadedMsg{libraries: []jellyfin.Library{{ID: "lib-1", Name: "Music"}}}
	newM, _ = m.Update(libsMsg)
	m = newM.(Model)
	if m.screen != ScreenLibrary {
		t.Errorf("expected ScreenLibrary after libs loaded, got %v", m.screen)
	}

	// Test PlayTracksMsg
	tracks := []jellyfin.Track{
		{ID: "t-1", Name: "Track 1", RunTimeTicks: 1800000000, Artists: []string{"Artist A"}},
		{ID: "t-2", Name: "Track 2", RunTimeTicks: 2400000000, Artists: []string{"Artist A"}},
	}
	playMsg := library.PlayTracksMsg{Tracks: tracks, StartIndex: 0}
	newM, _ = m.Update(playMsg)
	m = newM.(Model)

	if m.screen != ScreenQueue {
		t.Errorf("expected ScreenQueue during playback, got %v", m.screen)
	}
	if !m.IsPlaying() || m.CurrentItemID() != "t-1" {
		t.Errorf("unexpected playing state: playing=%v, id=%q", m.IsPlaying(), m.CurrentItemID())
	}

	// Test MPRIS NextMsg
	newM, _ = m.Update(mpris.NextMsg{})
	m = newM.(Model)
	if m.currentIndex != 1 || m.CurrentItemID() != "t-2" {
		t.Errorf("expected advance to track 2, got idx=%d, id=%q", m.currentIndex, m.CurrentItemID())
	}

	// Test MPRIS PreviousMsg (position <= 3.0 -> goes back to track 0)
	m.playerState.Position = 1.0
	newM, _ = m.Update(mpris.PreviousMsg{})
	m = newM.(Model)
	if m.currentIndex != 0 || m.CurrentItemID() != "t-1" {
		t.Errorf("expected prev track 0, got idx=%d, id=%q", m.currentIndex, m.CurrentItemID())
	}

	// Test PositionMsg EOF error -> advances queue
	posMsg := player.PositionMsg{Generation: m.playGeneration, Err: errors.New("EOF")}
	newM, _ = m.Update(posMsg)
	m = newM.(Model)
	if m.currentIndex != 1 || m.CurrentItemID() != "t-2" {
		t.Errorf("expected EOF to advance to track 2, got idx=%d", m.currentIndex)
	}

	// Test PositionMsg EOF on final track -> stops playback
	posMsg = player.PositionMsg{Generation: m.playGeneration, Err: errors.New("EOF")}
	newM, _ = m.Update(posMsg)
	m = newM.(Model)
	if m.IsPlaying() {
		t.Errorf("expected playback stopped after final EOF")
	}

	// Test QueueActionMsg clear
	m.tracks = tracks
	m.currentIndex = 0
	newM, _ = m.Update(queue.QueueActionMsg{Action: "clear"})
	m = newM.(Model)
	if len(m.tracks) != 0 || m.IsPlaying() {
		t.Errorf("expected queue cleared")
	}

	// Test View rendering
	v := m.View()
	if v == "" {
		t.Error("expected non-empty view")
	}
}

func TestAppModel_CommandPaletteAndGlobalKeys(t *testing.T) {
	m := New(nil, nil)
	m.SetSize(80, 24)
	m.screen = ScreenLibrary

	// Test open palette with ctrl+p
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = newM.(Model)
	if !m.palette.Visible() {
		t.Fatal("expected palette to be visible after ctrl+p")
	}

	// Test View overlay when palette is open
	v := m.View()
	if v == "" {
		t.Error("expected non-empty view when palette is overlaid")
	}

	// Test executing a static palette action directly via handlePaletteAction
	newM, _ = m.handlePaletteAction(components.ActionShowQueue, "", nil)
	m = newM.(Model)
	if m.screen != ScreenQueue {
		t.Errorf("expected screen to switch to queue, got %v", m.screen)
	}
	m.palette.Close()

	// Test global key s (shuffle toggle)
	if m.shuffle {
		t.Error("expected shuffle false initially")
	}
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = newM.(Model)
	if !m.shuffle {
		t.Error("expected shuffle true after pressing s")
	}

	// Setup tracks to test global n / p
	tracks := []jellyfin.Track{
		{ID: "t-1", Name: "Track 1", RunTimeTicks: 1800000000},
		{ID: "t-2", Name: "Track 2", RunTimeTicks: 1800000000},
	}
	m.tracks = tracks
	m.currentIndex = 0

	// Test global n (next)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = newM.(Model)
	if m.currentIndex != 1 {
		t.Errorf("expected currentIndex 1 after n, got %d", m.currentIndex)
	}

	// Test global p (previous)
	newM, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = newM.(Model)
	if m.currentIndex != 0 {
		t.Errorf("expected currentIndex 0 after p, got %d", m.currentIndex)
	}
}
