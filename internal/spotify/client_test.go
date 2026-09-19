package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLikedTracksPaginationShape(t *testing.T) {
	auth, err := NewAuth(AuthConfig{
		StaticAccessToken: "test-token",
		TokenFile:         t.TempDir() + "/token.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(auth, 5*time.Second, 0)
	client.http.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		body := `{
			"items": [{
				"added_at": "2026-01-01T00:00:00Z",
				"track": {
					"id": "sp-1",
					"name": "稻香",
					"artists": [{"name": "周杰伦"}],
					"album": {"name": "魔杰座"},
					"duration_ms": 223000,
					"external_urls": {"spotify": "https://open.spotify.com/track/sp-1"},
					"is_local": false,
					"explicit": false
				}
			}],
			"next": null
		}`
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	tracks, err := client.LikedTracks(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("track count = %d, want 1", len(tracks))
	}
	if tracks[0].ID != "sp-1" || tracks[0].DurationSeconds != 223 || len(tracks[0].Artists) != 1 {
		t.Fatalf("unexpected track: %#v", tracks[0])
	}
}

func TestLikedTracksPremiumRequiredErrorIsActionable(t *testing.T) {
	auth, err := NewAuth(AuthConfig{
		StaticAccessToken: "test-token",
		TokenFile:         t.TempDir() + "/token.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(auth, 5*time.Second, 0)
	client.http.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"error":{"status":403,"message":"Active premium subscription required for the owner of the app. When the subscription status changes, it can take a few hours before requests are allowed again."}}`
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	_, err = client.LikedTracks(context.Background(), 50)
	if err == nil {
		t.Fatal("LikedTracks() error = nil, want Premium error")
	}
	if !strings.Contains(err.Error(), "--spotify-export-file") || !strings.Contains(err.Error(), "Premium") {
		t.Fatalf("LikedTracks() error = %q, want actionable Premium error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
