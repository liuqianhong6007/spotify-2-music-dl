package musicdl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"spotify-to-musicdl/internal/httpx"
)

func TestParseJSONSearch(t *testing.T) {
	payload := map[string]any{
		"code": 200,
		"msg":  "success",
		"data": map[string]any{
			"type": "song",
			"songs": []any{
				map[string]any{
					"id":       "123",
					"source":   "netease",
					"name":     "稻香",
					"artist":   "周杰伦",
					"album":    "魔杰座",
					"duration": 223,
					"extra": map[string]any{
						"ext":     "flac",
						"bitrate": 999,
					},
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	response := &httpx.Response{
		StatusCode: 200,
		Header:     map[string][]string{"Content-Type": {"application/json"}},
		Body:       body,
	}
	candidates, err := parseJSONSearch(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	if candidates[0].ID != "123" || candidates[0].Bitrate != 999 {
		t.Fatalf("unexpected candidate: %#v", candidates[0])
	}
}

func TestParseNativeWebSearch(t *testing.T) {
	rawHTML := `
<html><body><ul>
<li class="song-card"
    data-id="456"
    data-source="qq"
    data-album-id="a1"
    data-album="魔杰座"
    data-duration="223"
    data-name="稻香"
    data-artist="周杰伦"
    data-cover="https://example.test/cover.jpg"
    data-extra='{"ext":"mp3","bitrate":320,"size":1234}'>
  <div class="actions">
    <a class="btn-circle btn-dl btn-download"
       href="/music/download?id=456&amp;source=qq&amp;name=%E7%A8%BB%E9%A6%99">下载</a>
  </div>
</li>
</ul></body></html>`
	candidates, err := parseWebSearch(rawHTML, "http://nas:8080")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.ID != "456" || candidate.Source != "qq" || candidate.Duration != 223 {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
	if candidate.DownloadURL == "" {
		t.Fatal("download URL was not extracted")
	}
}

func TestContentDispositionUTF8Filename(t *testing.T) {
	header := `attachment; filename="fallback.mp3"; filename*=utf-8''%E7%A8%BB%E9%A6%99%20-%20%E5%91%A8%E6%9D%B0%E4%BC%A6.flac`
	got := contentDispositionFilename(header)
	want := "稻香 - 周杰伦.flac"
	if got != want {
		t.Fatalf("filename = %q, want %q", got, want)
	}
}

func TestPrefixURLs(t *testing.T) {
	client, err := New("http://nas:8080", "auto", "music", nil, nil, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.searchURL("web"); got != "http://nas:8080/music/search" {
		t.Fatalf("web search URL = %q", got)
	}
	if got := client.searchURL("v1"); got != "http://nas:8080/api/v1/music/search" {
		t.Fatalf("v1 search URL = %q", got)
	}
}

func TestBaseURLAlreadyContainsPrefix(t *testing.T) {
	client, err := New("http://nas:8080/music", "auto", "music", nil, nil, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.searchURL("web"); got != "http://nas:8080/music/search" {
		t.Fatalf("web search URL = %q", got)
	}
	if got := client.searchURL("v1"); got != "http://nas:8080/api/v1/music/search" {
		t.Fatalf("v1 search URL = %q", got)
	}
}

func TestSearchAndSaveOnNativeWebMode(t *testing.T) {
	client, err := New("http://nas:8080", "auto", "music", nil, nil, 5, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	client.http.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		contentType := "application/json"
		body := ""
		switch r.URL.Path {
		case "/api/v1/music/search":
			status = http.StatusNotFound
			body = "not found"
		case "/music/search":
			contentType = "text/html; charset=utf-8"
			body = `<li class="song-card" data-id="456" data-source="qq" data-duration="223" data-name="稻香" data-artist="周杰伦" data-extra='{"ext":"mp3"}'>
<a class="btn-download" href="/music/download?id=456&amp;source=qq&amp;name=%E7%A8%BB%E9%A6%99">download</a></li>`
		case "/music/download":
			if r.Method != http.MethodPost || r.URL.Query().Get("save_local") != "1" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				status = http.StatusBadRequest
				body = `{"error":"bad request"}`
				break
			}
			body = `{"status":"ok","saved":true,"path":"/data/稻香.mp3","filename":"稻香.mp3"}`
		default:
			status = http.StatusNotFound
			body = "not found"
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})

	candidates, err := client.Search(context.Background(), "稻香 周杰伦")
	if err != nil {
		t.Fatal(err)
	}
	if client.ActiveMode() != "web" {
		t.Fatalf("active mode = %q, want web", client.ActiveMode())
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	result, err := client.SaveOnNAS(context.Background(), candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "saved" || result.Path == "" {
		t.Fatalf("unexpected save result: %#v", result)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
