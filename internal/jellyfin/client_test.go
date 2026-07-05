package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_Login(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users/AuthenticateByName" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		var req AuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if req.Username != "alice" || req.Pw != "secret" {
			t.Errorf("unexpected creds: %+v", req)
		}

		resp := AuthResponse{
			User:        User{ID: "user-123", Name: "alice"},
			AccessToken: "token-xyz",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "")
	res, err := client.Login(context.Background(), "alice", "secret")
	if err != nil {
		t.Fatalf("Login error: %v", err)
	}
	if res.AccessToken != "token-xyz" || res.User.ID != "user-123" {
		t.Errorf("unexpected login result: %+v", res)
	}
	if client.Token() != "token-xyz" || client.UserID() != "user-123" {
		t.Errorf("client state not updated: token=%q, user=%q", client.Token(), client.UserID())
	}
}

func TestClient_GetMusicLibraries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="token-xyz"`) {
			t.Errorf("missing auth token header: %q", r.Header.Get("Authorization"))
		}
		resp := itemsResponse[Library]{
			Items: []Library{
				{ID: "lib-1", Name: "Music", CollectionType: "music"},
				{ID: "lib-2", Name: "Movies", CollectionType: "movies"},
				{ID: "lib-3", Name: "Songs", CollectionType: ""},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token-xyz", "user-123")
	libs, err := client.GetMusicLibraries(context.Background())
	if err != nil {
		t.Fatalf("GetMusicLibraries error: %v", err)
	}
	if len(libs) != 1 {
		t.Fatalf("expected 1 music library (strict CollectionType==music), got %d", len(libs))
	}
	if libs[0].Name != "Music" {
		t.Errorf("unexpected library: %+v", libs)
	}
}

func TestClient_GetArtistsAlbumsTracks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parentID := r.URL.Query().Get("ParentId")
		itemTypes := r.URL.Query().Get("IncludeItemTypes")

		switch {
		case itemTypes == "MusicArtist,Artist" && parentID == "lib-1":
			_ = json.NewEncoder(w).Encode(itemsResponse[Artist]{
				Items: []Artist{{ID: "art-1", Name: "Pink Floyd"}},
			})
		case itemTypes == "MusicAlbum" && parentID == "art-1":
			_ = json.NewEncoder(w).Encode(itemsResponse[Album]{
				Items: []Album{{ID: "alb-1", Name: "Dark Side of the Moon", ProductionYear: 1973}},
			})
		case itemTypes == "Audio" && parentID == "alb-1":
			_ = json.NewEncoder(w).Encode(itemsResponse[Track]{
				Items: []Track{{ID: "trk-1", Name: "Time", RunTimeTicks: 4230000000, Artists: []string{"Pink Floyd"}}},
			})
		case itemTypes == "Audio" && parentID == "lib-1":
			_ = json.NewEncoder(w).Encode(itemsResponse[Track]{
				Items: []Track{{ID: "trk-1", Name: "Time", RunTimeTicks: 4230000000, Artists: []string{"Pink Floyd"}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "token-xyz", "user-123")
	ctx := context.Background()

	artists, err := client.GetArtists(ctx, "lib-1")
	if err != nil || len(artists) != 1 || artists[0].Name != "Pink Floyd" {
		t.Fatalf("GetArtists failed: %+v, err=%v", artists, err)
	}

	allTracks, err := client.GetAllTracks(ctx, "lib-1")
	if err != nil || len(allTracks) != 1 || allTracks[0].Name != "Time" {
		t.Fatalf("GetAllTracks failed: %+v, err=%v", allTracks, err)
	}

	albums, err := client.GetAlbums(ctx, "art-1")
	if err != nil || len(albums) != 1 || albums[0].Name != "Dark Side of the Moon" {
		t.Fatalf("GetAlbums failed: %+v, err=%v", albums, err)
	}

	tracks, err := client.GetTracks(ctx, "alb-1")
	if err != nil || len(tracks) != 1 || tracks[0].Name != "Time" {
		t.Fatalf("GetTracks failed: %+v, err=%v", tracks, err)
	}
	if tracks[0].Duration() != 423.0 {
		t.Errorf("expected track duration 423s, got %f", tracks[0].Duration())
	}
	if tracks[0].DisplayArtist() != "Pink Floyd" {
		t.Errorf("expected DisplayArtist 'Pink Floyd', got %q", tracks[0].DisplayArtist())
	}
}

func TestClient_GetPlaylists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/user-123/Items":
			if r.URL.Query().Get("IncludeItemTypes") != "Playlist" {
				t.Fatalf("IncludeItemTypes = %q, want Playlist", r.URL.Query().Get("IncludeItemTypes"))
			}
			_ = json.NewEncoder(w).Encode(itemsResponse[Playlist]{
				Items:            []Playlist{{ID: "pl-1", Name: "Favorites", Count: 2}},
				TotalRecordCount: 1,
			})
		case "/Playlists/pl-1/Items":
			if r.URL.Query().Get("userId") != "user-123" {
				t.Fatalf("userId = %q, want user-123", r.URL.Query().Get("userId"))
			}
			_ = json.NewEncoder(w).Encode(itemsResponse[Track]{
				Items:            []Track{{ID: "trk-1", Name: "One"}, {ID: "trk-2", Name: "Two"}},
				TotalRecordCount: 2,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "token-xyz", "user-123")
	playlists, err := client.GetPlaylists(context.Background())
	if err != nil {
		t.Fatalf("GetPlaylists error: %v", err)
	}
	if len(playlists) != 1 || playlists[0].Name != "Favorites" || playlists[0].Count != 2 {
		t.Fatalf("unexpected playlists: %+v", playlists)
	}

	tracks, err := client.GetPlaylistTracks(context.Background(), "pl-1")
	if err != nil {
		t.Fatalf("GetPlaylistTracks error: %v", err)
	}
	if len(tracks) != 2 || tracks[1].Name != "Two" {
		t.Fatalf("unexpected playlist tracks: %+v", tracks)
	}
}

func TestClient_StreamHelpersAndProgress(t *testing.T) {
	progressCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Sessions/Playing/Progress" {
			progressCalled = true
			var req PlaybackProgressRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ItemID != "trk-1" || req.PositionTicks != 120000000 {
				t.Errorf("unexpected progress req: %+v", req)
			}
			if req.PlayMethod != "DirectPlay" {
				t.Errorf("expected PlayMethod DirectPlay, got %q", req.PlayMethod)
			}
			if req.PlaySessionId != "sess-1" {
				t.Errorf("expected PlaySessionId sess-1, got %q", req.PlaySessionId)
			}
			if req.MediaSourceId != "trk-1" {
				t.Errorf("expected MediaSourceId trk-1, got %q", req.MediaSourceId)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "token-xyz", "user-123")
	streamURL := client.StreamURL("trk-1", "test-session")
	wantQuery := "api_key=token-xyz&deviceId=spruce-tui&playSessionId=test-session&static=true"
	if streamURL != server.URL+"/Audio/trk-1/stream?"+wantQuery {
		t.Errorf("unexpected StreamURL: %q", streamURL)
	}
	headers := client.StreamHeaders()
	if len(headers) != 1 || !strings.HasPrefix(headers[0], "Authorization: MediaBrowser") {
		t.Errorf("unexpected StreamHeaders: %v", headers)
	}

	err := client.ReportPlaybackProgress(context.Background(), "trk-1", 12.0, false, "sess-1")
	if err != nil {
		t.Fatalf("ReportPlaybackProgress error: %v", err)
	}
	if !progressCalled {
		t.Error("progress endpoint was not called")
	}
}
