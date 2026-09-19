package spotify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExportFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liked-songs.csv")
	content := "\ufeffTrack URI,Track Name,Artist Name(s),Album Name,Track Duration (ms),Added At,Explicit\n" +
		"spotify:track:sp-1,稻香,周杰伦,魔杰座,223000,2026-01-01T00:00:00Z,false\n" +
		"https://open.spotify.com/track/sp-2,Test Song,Artist A; Artist B,Album,3:45,2026-01-02T00:00:00Z,true\n" +
		"spotify:track:sp-1,重复歌曲,周杰伦,魔杰座,223000,2026-01-01T00:00:00Z,false\n" +
		"spotify:track:sp-3,,无名,Album,100000,,false\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	tracks, err := LoadExportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("track count = %d, want 2", len(tracks))
	}
	if tracks[0].ID != "sp-1" || tracks[0].Name != "稻香" || tracks[0].DurationSeconds != 223 {
		t.Fatalf("unexpected first track: %#v", tracks[0])
	}
	if tracks[1].ID != "sp-2" || tracks[1].DurationSeconds != 225 || len(tracks[1].Artists) != 2 {
		t.Fatalf("unexpected second track: %#v", tracks[1])
	}
	if !tracks[1].Explicit || tracks[1].URL == "" {
		t.Fatalf("unexpected second track flags: %#v", tracks[1])
	}
}

func TestLoadExportFileRejectsMissingNameHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.csv")
	if err := os.WriteFile(path, []byte("Track URI,Artist Name(s)\nspotify:track:sp-1,Artist\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadExportFile(path); err == nil {
		t.Fatal("LoadExportFile() error = nil, want missing header error")
	}
}
