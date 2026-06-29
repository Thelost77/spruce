package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Thelost77/spruce/internal/logger"
)

// HTTPStatusError wraps a non-2xx HTTP response status.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Body)
}

// IsHTTPStatus reports whether err or one of its wrapped errors is an HTTPStatusError
// for the given status code.
func IsHTTPStatus(err error, statusCode int) bool {
	var statusErr *HTTPStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == statusCode
}

const maxResponseSize = 50 * 1024 * 1024 // 50 MB

// Client is an HTTP client for the Jellyfin API.
type Client struct {
	baseURL    string
	token      string
	userID     string
	httpClient *http.Client
}

// NewClient creates a new Jellyfin API client.
func NewClient(baseURL, token, userID string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		userID:  userID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// BaseURL returns the configured server URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Token returns the active auth token.
func (c *Client) Token() string {
	return c.token
}

// UserID returns the active user ID.
func (c *Client) UserID() string {
	return c.userID
}

// SetAuth updates the active auth credentials.
func (c *Client) SetAuth(token, userID string) {
	c.token = token
	c.userID = userID
}

// authHeader constructs the MediaBrowser Authorization header string.
func (c *Client) authHeader() string {
	header := `MediaBrowser Client="spruce", Device="Linux", DeviceId="spruce-tui", Version="1.0.0"`
	if c.token != "" {
		header += `, Token="` + c.token + `"`
	}
	return header
}

// StreamURL constructs the direct stream URL for an audio item.
func (c *Client) StreamURL(itemID string) string {
	return fmt.Sprintf("%s/Audio/%s/stream?static=true", c.baseURL, itemID)
}

// StreamHeaders returns the HTTP headers needed by mpv to authenticate direct streaming.
func (c *Client) StreamHeaders() []string {
	return []string{"Authorization: " + c.authHeader()}
}

// do executes an authenticated HTTP request and returns the response body.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	start := time.Now()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Error("http request failed", "method", method, "path", path, "err", err, "duration", time.Since(start))
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limitedReader := io.LimitReader(resp.Body, maxResponseSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		logger.Error("http response read failed", "method", method, "path", path, "status", resp.StatusCode, "err", err, "duration", time.Since(start))
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(data) > maxResponseSize {
		logger.Error("http response body too large", "method", method, "path", path, "status", resp.StatusCode, "limit", maxResponseSize, "duration", time.Since(start))
		return nil, fmt.Errorf("response body exceeds %d byte limit", maxResponseSize)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.Error("http request returned unexpected status", "method", method, "path", path, "status", resp.StatusCode, "body", string(data), "duration", time.Since(start))
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(data)}
	}

	logger.Debug("http request completed", "method", method, "path", path, "status", resp.StatusCode, "duration", time.Since(start))
	return data, nil
}

// Login authenticates a user against Jellyfin server.
func (c *Client) Login(ctx context.Context, username, password string) (*AuthResponse, error) {
	reqBody := AuthRequest{
		Username: username,
		Pw:       password,
	}
	data, err := c.do(ctx, http.MethodPost, "/Users/AuthenticateByName", reqBody)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	var resp AuthResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	c.SetAuth(resp.AccessToken, resp.User.ID)
	return &resp, nil
}

// GetMusicLibraries fetches all user views and returns those related to music.
func (c *Client) GetMusicLibraries(ctx context.Context) ([]Library, error) {
	if c.userID == "" {
		return nil, errors.New("user ID not set")
	}
	path := fmt.Sprintf("/Users/%s/Views", url.PathEscape(c.userID))
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get views: %w", err)
	}
	var resp itemsResponse[Library]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode views: %w", err)
	}
	var musicLibs []Library
	for _, lib := range resp.Items {
		if strings.EqualFold(lib.CollectionType, "music") || lib.CollectionType == "" {
			musicLibs = append(musicLibs, lib)
		}
	}
	return musicLibs, nil
}

// GetArtists fetches music artists within a library.
func (c *Client) GetArtists(ctx context.Context, libraryID string) ([]Artist, error) {
	if c.userID == "" {
		return nil, errors.New("user ID not set")
	}
	params := url.Values{}
	params.Set("ParentId", libraryID)
	params.Set("SortBy", "SortName")
	params.Set("SortOrder", "Ascending")
	params.Set("IncludeItemTypes", "MusicArtist,Artist")
	params.Set("Recursive", "true")

	path := fmt.Sprintf("/Users/%s/Items?%s", url.PathEscape(c.userID), params.Encode())
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get artists: %w", err)
	}
	var resp itemsResponse[Artist]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode artists: %w", err)
	}
	return resp.Items, nil
}

// GetAlbums fetches music albums for a given artist.
func (c *Client) GetAlbums(ctx context.Context, artistID string) ([]Album, error) {
	if c.userID == "" {
		return nil, errors.New("user ID not set")
	}
	params := url.Values{}
	params.Set("ParentId", artistID)
	params.Set("SortBy", "ProductionYear,SortName")
	params.Set("SortOrder", "Ascending")
	params.Set("IncludeItemTypes", "MusicAlbum")
	params.Set("Recursive", "true")

	path := fmt.Sprintf("/Users/%s/Items?%s", url.PathEscape(c.userID), params.Encode())
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get albums: %w", err)
	}
	var resp itemsResponse[Album]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode albums: %w", err)
	}
	return resp.Items, nil
}

// GetTracks fetches audio tracks for a given album.
func (c *Client) GetTracks(ctx context.Context, albumID string) ([]Track, error) {
	if c.userID == "" {
		return nil, errors.New("user ID not set")
	}
	params := url.Values{}
	params.Set("ParentId", albumID)
	params.Set("SortBy", "ParentIndexNumber,IndexNumber")
	params.Set("SortOrder", "Ascending")
	params.Set("IncludeItemTypes", "Audio")
	params.Set("Recursive", "true")

	path := fmt.Sprintf("/Users/%s/Items?%s", url.PathEscape(c.userID), params.Encode())
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get tracks: %w", err)
	}
	var resp itemsResponse[Track]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode tracks: %w", err)
	}
	return resp.Items, nil
}

// GetAllTracks fetches all audio tracks within a library.
func (c *Client) GetAllTracks(ctx context.Context, libraryID string) ([]Track, error) {
	if c.userID == "" {
		return nil, errors.New("user ID not set")
	}
	params := url.Values{}
	params.Set("ParentId", libraryID)
	params.Set("SortBy", "SortName")
	params.Set("SortOrder", "Ascending")
	params.Set("IncludeItemTypes", "Audio")
	params.Set("Recursive", "true")

	path := fmt.Sprintf("/Users/%s/Items?%s", url.PathEscape(c.userID), params.Encode())
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get all tracks: %w", err)
	}
	var resp itemsResponse[Track]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode all tracks: %w", err)
	}
	return resp.Items, nil
}

// ReportPlaybackStart reports to Jellyfin that playback has begun.
func (c *Client) ReportPlaybackStart(ctx context.Context, itemID string) error {
	body := PlaybackProgressRequest{
		ItemID:     itemID,
		PlayMethod: "DirectStream",
		CanSeek:    true,
	}
	_, err := c.do(ctx, http.MethodPost, "/Sessions/Playing", body)
	return err
}

// ReportPlaybackProgress reports active playback position heartbeats.
func (c *Client) ReportPlaybackProgress(ctx context.Context, itemID string, positionSeconds float64, paused bool) error {
	body := PlaybackProgressRequest{
		ItemID:        itemID,
		PositionTicks: SecondsToTicks(positionSeconds),
		IsPaused:      paused,
		PlayMethod:    "DirectStream",
		CanSeek:       true,
	}
	_, err := c.do(ctx, http.MethodPost, "/Sessions/Playing/Progress", body)
	return err
}

// ReportPlaybackStopped reports that playback has stopped.
func (c *Client) ReportPlaybackStopped(ctx context.Context, itemID string, positionSeconds float64) error {
	body := PlaybackProgressRequest{
		ItemID:        itemID,
		PositionTicks: SecondsToTicks(positionSeconds),
		PlayMethod:    "DirectStream",
		CanSeek:       true,
	}
	_, err := c.do(ctx, http.MethodPost, "/Sessions/Playing/Stopped", body)
	return err
}
