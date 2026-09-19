package state

import (
	"os"
	"path/filepath"
	"testing"

	"spotify-to-musicdl/internal/model"
)

func TestDestinationChangeForcesProcessing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	track := model.SpotifyTrack{ID: "sp-1", Name: "稻香", Artists: []string{"周杰伦"}}
	candidate := model.Candidate{ID: "1", Source: "netease", Name: "稻香", Artist: "周杰伦"}
	match := model.MatchResult{Candidate: candidate, Score: 100}
	result := model.DownloadResult{Status: StatusDownloaded, Path: filepath.Join(t.TempDir(), "song.mp3")}
	if err := writeEmptyFile(result.Path); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSuccess(track, match, result, "local"); err != nil {
		t.Fatal(err)
	}
	skip, _ := store.ShouldSkip(track, false, false, "local")
	if !skip {
		t.Fatal("expected local destination to be skipped")
	}
	skip, reason := store.ShouldSkip(track, false, false, "nas")
	if skip {
		t.Fatal("expected destination change to force processing")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestMissingLocalFileForcesProcessing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	track := model.SpotifyTrack{ID: "sp-1", Name: "稻香"}
	match := model.MatchResult{Candidate: model.Candidate{ID: "1", Source: "netease", Name: "稻香"}, Score: 100}
	result := model.DownloadResult{Status: StatusDownloaded, Path: filepath.Join(t.TempDir(), "missing.mp3")}
	if err := store.RecordSuccess(track, match, result, "local"); err != nil {
		t.Fatal(err)
	}
	skip, reason := store.ShouldSkip(track, false, false, "local")
	if skip {
		t.Fatal("expected missing file to force processing")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
}

func writeEmptyFile(path string) error {
	return os.WriteFile(path, []byte("audio"), 0o600)
}
