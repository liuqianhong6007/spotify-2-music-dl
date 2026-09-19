package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spotify-to-musicdl/internal/httpx"
	"spotify-to-musicdl/internal/model"
	"spotify-to-musicdl/internal/util"
)

const apiBaseURL = "https://api.spotify.com/v1"

type Client struct {
	auth     *Auth
	http     *httpx.Client
	progress func(int, int)
}

func NewClient(auth *Auth, timeout time.Duration, retries int) *Client {
	return &Client{
		auth: auth,
		http: httpx.NewClient(timeout, retries, time.Second),
	}
}

func (c *Client) SetProgress(progress func(count int, page int)) {
	c.progress = progress
}

func (c *Client) LikedTracks(ctx context.Context, pageSize int) ([]model.SpotifyTrack, error) {
	if pageSize < 1 || pageSize > 50 {
		return nil, fmt.Errorf("Spotify page size 必须在 1 到 50 之间")
	}
	tracks := make([]model.SpotifyTrack, 0)
	seen := map[string]bool{}
	nextURL := apiBaseURL + "/me/tracks?" + url.Values{
		"limit":  {strconv.Itoa(pageSize)},
		"offset": {"0"},
	}.Encode()
	page := 0

	for nextURL != "" {
		var payload likedTracksPage
		if err := c.getJSON(ctx, nextURL, &payload); err != nil {
			return nil, err
		}
		for _, item := range payload.Items {
			track := parseLikedItem(item)
			if track.ID == "" || seen[track.ID] {
				continue
			}
			seen[track.ID] = true
			tracks = append(tracks, track)
		}
		page++
		if c.progress != nil {
			c.progress(len(tracks), page)
		}
		if payload.Next == nil {
			nextURL = ""
		} else {
			nextURL = *payload.Next
		}
	}
	return tracks, nil
}

func (c *Client) getJSON(ctx context.Context, rawURL string, target any) error {
	var lastResponse *httpx.Response
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.auth.AccessToken(ctx)
		if err != nil {
			return err
		}
		response, err := c.http.Do(ctx, http.MethodGet, rawURL, http.Header{
			"Authorization": {"Bearer " + token},
			"Accept":        {"application/json"},
		}, nil)
		if err != nil {
			return err
		}
		lastResponse = response
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.auth.InvalidateAccessToken()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Spotify API 请求失败 HTTP %d: %s", response.StatusCode, util.Truncate(response.Text(), 500))
		}
		if err := json.Unmarshal(response.Body, target); err != nil {
			return fmt.Errorf("Spotify API 返回非 JSON: %w", err)
		}
		return nil
	}
	if lastResponse != nil {
		return fmt.Errorf("Spotify API 鉴权失败 HTTP %d", lastResponse.StatusCode)
	}
	return fmt.Errorf("Spotify API 鉴权失败")
}

type likedTracksPage struct {
	Items []likedItem `json:"items"`
	Next  *string     `json:"next"`
}

type likedItem struct {
	AddedAt string        `json:"added_at"`
	Track   *trackPayload `json:"track"`
}

type trackPayload struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Artists      []artistPayload   `json:"artists"`
	Album        albumPayload      `json:"album"`
	DurationMS   int               `json:"duration_ms"`
	ExternalURLs map[string]string `json:"external_urls"`
	IsLocal      bool              `json:"is_local"`
	Explicit     bool              `json:"explicit"`
}

type artistPayload struct {
	Name string `json:"name"`
}

type albumPayload struct {
	Name string `json:"name"`
}

func parseLikedItem(item likedItem) model.SpotifyTrack {
	if item.Track == nil {
		return model.SpotifyTrack{}
	}
	artists := make([]string, 0, len(item.Track.Artists))
	for _, artist := range item.Track.Artists {
		if value := stringsTrim(artist.Name); value != "" {
			artists = append(artists, value)
		}
	}
	return model.SpotifyTrack{
		ID:              stringsTrim(item.Track.ID),
		Name:            stringsTrim(item.Track.Name),
		Artists:         artists,
		Album:           stringsTrim(item.Track.Album.Name),
		DurationSeconds: item.Track.DurationMS / 1000,
		AddedAt:         item.AddedAt,
		URL:             item.Track.ExternalURLs["spotify"],
		IsLocal:         item.Track.IsLocal,
		Explicit:        item.Track.Explicit,
	}
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}
