package model

type SpotifyTrack struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Artists         []string `json:"artists"`
	Album           string   `json:"album"`
	DurationSeconds int      `json:"duration_seconds"`
	AddedAt         string   `json:"added_at,omitempty"`
	URL             string   `json:"url,omitempty"`
	IsLocal         bool     `json:"is_local,omitempty"`
	Explicit        bool     `json:"explicit,omitempty"`
}

func (t SpotifyTrack) ArtistText() string {
	return joinArtists(t.Artists)
}

func (t SpotifyTrack) SearchQuery() string {
	if len(t.Artists) == 0 {
		return t.Name
	}
	return t.Name + " " + t.ArtistText()
}

func (t SpotifyTrack) DisplayName() string {
	if len(t.Artists) == 0 {
		return t.Name
	}
	return t.Name + " - " + t.ArtistText()
}

type Candidate struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	Name        string         `json:"name"`
	Artist      string         `json:"artist,omitempty"`
	Album       string         `json:"album,omitempty"`
	Duration    int            `json:"duration,omitempty"`
	Cover       string         `json:"cover,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	Link        string         `json:"link,omitempty"`
	DownloadURL string         `json:"download_url,omitempty"`
	Bitrate     int            `json:"bitrate,omitempty"`
	Size        int64          `json:"size,omitempty"`
}

func (c Candidate) DisplayName() string {
	if c.Artist == "" {
		return c.Name
	}
	return c.Name + " - " + c.Artist
}

type MatchResult struct {
	Candidate     Candidate
	Score         int
	TitleScore    int
	ArtistScore   int
	DurationScore int
	DurationDiff  int
	Reasons       []string
}

func (m MatchResult) Confidence() float64 {
	value := float64(m.Score) / 110.0
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

type DownloadResult struct {
	Status   string
	Path     string
	Filename string
	Message  string
	Skipped  bool
}

func joinArtists(artists []string) string {
	result := ""
	for i, artist := range artists {
		if i > 0 {
			result += ", "
		}
		result += artist
	}
	return result
}
