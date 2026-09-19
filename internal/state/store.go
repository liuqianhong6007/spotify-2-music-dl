package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"spotify-to-musicdl/internal/model"
	"spotify-to-musicdl/internal/util"
)

const (
	StatusDownloaded = "downloaded"
	StatusSaved      = "saved"
	StatusSkipped    = "skipped"
	StatusFailed     = "failed"
)

type Entry struct {
	Status        string           `json:"status"`
	UpdatedAt     string           `json:"updated_at"`
	Destination   string           `json:"destination,omitempty"`
	Spotify       map[string]any   `json:"spotify,omitempty"`
	Match         map[string]any   `json:"match,omitempty"`
	BestScore     *int             `json:"best_score,omitempty"`
	BestCandidate *model.Candidate `json:"best_candidate,omitempty"`
	Path          string           `json:"path,omitempty"`
	Filename      string           `json:"filename,omitempty"`
	Message       string           `json:"message,omitempty"`
	Skipped       bool             `json:"skipped,omitempty"`
}

type document struct {
	Version int              `json:"version"`
	Tracks  map[string]Entry `json:"tracks"`
}

type Store struct {
	path string
	data document
}

func Load(path string) (*Store, error) {
	store := &Store{
		path: path,
		data: document{Version: 1, Tracks: map[string]Entry{}},
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, err
	}
	var loaded document
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("解析状态文件 %s: %w", path, err)
	}
	if loaded.Tracks == nil {
		loaded.Tracks = map[string]Entry{}
	}
	if loaded.Version == 0 {
		loaded.Version = 1
	}
	store.data = loaded
	return store, nil
}

func (s *Store) Save() error {
	return util.AtomicWriteJSON(s.path, s.data)
}

func (s *Store) ShouldSkip(track model.SpotifyTrack, force, skipFailed bool, destination string) (bool, string) {
	if force {
		return false, ""
	}
	entry, ok := s.data.Tracks[track.ID]
	if !ok {
		return false, ""
	}
	switch entry.Status {
	case StatusDownloaded, StatusSaved, StatusSkipped:
		if entry.Destination != "" && entry.Destination != destination {
			return false, "目标位置已变化，重新处理"
		}
		if entry.Destination == "" {
			if destination == "local" && entry.Status == StatusSaved {
				return false, "此前保存到 NAS，当前需要本地下载"
			}
			if destination == "nas" && entry.Status == StatusDownloaded {
				return false, "此前仅本地下载，当前需要保存到 NAS"
			}
		}
		if destination == "local" && entry.Path != "" {
			if _, err := os.Stat(entry.Path); err != nil {
				return false, "本地文件不存在，重新下载"
			}
		}
		if entry.Message != "" {
			return true, entry.Message
		}
		return true, "状态文件中已成功"
	case StatusFailed:
		if skipFailed {
			message := entry.Message
			if message == "" {
				message = "此前失败，按参数跳过"
			}
			return true, message
		}
	}
	return false, ""
}

func (s *Store) RecordSuccess(track model.SpotifyTrack, match model.MatchResult, result model.DownloadResult, destination string) error {
	score := match.Score
	s.data.Tracks[track.ID] = Entry{
		Status:      result.Status,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		Destination: destination,
		Spotify: map[string]any{
			"name":     track.Name,
			"artists":  track.Artists,
			"album":    track.Album,
			"duration": track.DurationSeconds,
			"url":      track.URL,
		},
		Match: map[string]any{
			"score":      score,
			"confidence": match.Confidence(),
			"reasons":    match.Reasons,
			"candidate":  match.Candidate,
		},
		Path:     result.Path,
		Filename: result.Filename,
		Message:  result.Message,
		Skipped:  result.Skipped,
	}
	return s.Save()
}

func (s *Store) RecordFailure(track model.SpotifyTrack, message string, ranked []model.MatchResult) error {
	var bestScore *int
	var bestCandidate *model.Candidate
	if len(ranked) > 0 {
		score := ranked[0].Score
		candidate := ranked[0].Candidate
		bestScore = &score
		bestCandidate = &candidate
	}
	s.data.Tracks[track.ID] = Entry{
		Status:    StatusFailed,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Spotify: map[string]any{
			"name":     track.Name,
			"artists":  track.Artists,
			"album":    track.Album,
			"duration": track.DurationSeconds,
			"url":      track.URL,
		},
		BestScore:     bestScore,
		BestCandidate: bestCandidate,
		Message:       message,
	}
	return s.Save()
}
