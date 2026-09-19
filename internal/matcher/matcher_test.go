package matcher

import (
	"testing"

	"spotify-to-musicdl/internal/model"
)

func TestChooseExactCandidate(t *testing.T) {
	track := model.SpotifyTrack{
		ID:              "sp-1",
		Name:            "稻香",
		Artists:         []string{"周杰伦"},
		DurationSeconds: 223,
	}
	exact := model.Candidate{ID: "1", Source: "netease", Name: "稻香", Artist: "周杰伦", Duration: 223, Bitrate: 999}
	wrong := model.Candidate{ID: "2", Source: "qq", Name: "稻香", Artist: "其他歌手", Duration: 180}
	best, ranked := ChooseBest(track, []model.Candidate{wrong, exact}, 70, 20, nil)
	if best == nil {
		t.Fatal("expected an exact match")
	}
	if best.Candidate.ID != "1" {
		t.Fatalf("best candidate = %s, want 1", best.Candidate.ID)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Fatalf("expected exact candidate to outrank wrong candidate: %#v", ranked)
	}
}

func TestRemasterSuffixNormalization(t *testing.T) {
	track := model.SpotifyTrack{Name: "稻香", Artists: []string{"周杰伦"}, DurationSeconds: 223}
	candidate := model.Candidate{Name: "稻香 (Remastered 2020)", Artist: "周杰伦", Duration: 224}
	result := Score(track, candidate, nil)
	if result.TitleScore != 65 {
		t.Fatalf("title score = %d, want 65", result.TitleScore)
	}
	if result.Score < 80 {
		t.Fatalf("score = %d, want >= 80", result.Score)
	}
}

func TestMultipleArtists(t *testing.T) {
	track := model.SpotifyTrack{
		Name:            "千里之外",
		Artists:         []string{"周杰伦", "费玉清"},
		DurationSeconds: 243,
	}
	candidate := model.Candidate{Name: "千里之外", Artist: "周杰伦/费玉清", Duration: 243}
	result := Score(track, candidate, nil)
	if result.ArtistScore != 25 {
		t.Fatalf("artist score = %d, want 25", result.ArtistScore)
	}
}

func TestMissingDurationDoesNotRejectStrongMatch(t *testing.T) {
	track := model.SpotifyTrack{Name: "稻香", Artists: []string{"周杰伦"}, DurationSeconds: 223}
	candidate := model.Candidate{Name: "稻香", Artist: "周杰伦"}
	best, _ := ChooseBest(track, []model.Candidate{candidate}, 70, 20, nil)
	if best == nil {
		t.Fatal("expected strong title/artist match to pass")
	}
}
