package spotify

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"spotify-to-musicdl/internal/model"
)

type exportColumns struct {
	id           int
	name         int
	artists      int
	album        int
	duration     int
	durationInMS bool
	addedAt      int
	url          int
	isLocal      int
	explicit     int
}

// LoadExportFile reads an Exportify-compatible CSV file. It intentionally
// accepts common header variants so exports from older Exportify versions keep
// working without another online API call.
func LoadExportFile(path string) ([]model.SpotifyTrack, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("Spotify 导出文件路径为空")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Spotify 导出文件失败: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	reader.LazyQuotes = true

	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("Spotify 导出文件为空: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Spotify 导出文件表头失败: %w", err)
	}

	columns, err := findExportColumns(header)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	tracks := make([]model.SpotifyTrack, 0)
	seen := make(map[string]struct{})
	for line := 2; ; line++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 Spotify 导出文件第 %d 行失败: %w", line, err)
		}

		track := parseExportRecord(record, columns)
		if strings.TrimSpace(track.Name) == "" {
			continue
		}
		key := exportTrackKey(track)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		tracks = append(tracks, track)
	}

	if len(tracks) == 0 {
		return nil, fmt.Errorf("Spotify 导出文件中没有可用歌曲: %s", path)
	}
	return tracks, nil
}

func findExportColumns(header []string) (exportColumns, error) {
	columns := exportColumns{
		id:       -1,
		name:     -1,
		artists:  -1,
		album:    -1,
		duration: -1,
		addedAt:  -1,
		url:      -1,
		isLocal:  -1,
		explicit: -1,
	}

	for index, raw := range header {
		name := normalizeExportHeader(raw)
		switch {
		case isExportHeader(name, "trackuri", "trackid", "spotifytrackid", "spotifyuri", "id", "uri"):
			columns.id = index
		case isExportHeader(name, "trackname", "name", "song", "title"):
			columns.name = index
		case isExportHeader(name, "artistnames", "artistname", "artists", "artist"):
			columns.artists = index
		case isExportHeader(name, "albumname", "album"):
			columns.album = index
		case name == "trackdurationms" || name == "durationms":
			columns.duration = index
			columns.durationInMS = true
		case name == "trackduration" || name == "duration" || name == "length":
			columns.duration = index
		case isExportHeader(name, "addedat", "addeddate", "dateadded"):
			columns.addedAt = index
		case isExportHeader(name, "trackurl", "spotifyurl", "url", "link"):
			columns.url = index
		case isExportHeader(name, "islocal", "local"):
			columns.isLocal = index
		case name == "explicit":
			columns.explicit = index
		}
	}

	if columns.name < 0 {
		return exportColumns{}, fmt.Errorf("无法识别歌曲名列表头，请使用 Exportify 导出的 CSV")
	}
	return columns, nil
}

func parseExportRecord(record []string, columns exportColumns) model.SpotifyTrack {
	track := model.SpotifyTrack{
		ID:       exportTrackID(exportCell(record, columns.id)),
		Name:     strings.TrimSpace(exportCell(record, columns.name)),
		Album:    strings.TrimSpace(exportCell(record, columns.album)),
		AddedAt:  strings.TrimSpace(exportCell(record, columns.addedAt)),
		IsLocal:  parseExportBool(exportCell(record, columns.isLocal)),
		Explicit: parseExportBool(exportCell(record, columns.explicit)),
		Artists:  splitExportArtists(exportCell(record, columns.artists)),
	}
	track.DurationSeconds = parseExportDuration(exportCell(record, columns.duration), columns.durationInMS)

	track.URL = strings.TrimSpace(exportCell(record, columns.url))
	if track.ID != "" && track.URL == "" {
		track.URL = "https://open.spotify.com/track/" + url.PathEscape(track.ID)
	}
	return track
}

func exportCell(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func normalizeExportHeader(value string) string {
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func isExportHeader(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func splitExportArtists(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == '|'
	})
	artists := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		artist := strings.TrimSpace(part)
		if artist == "" {
			continue
		}
		if _, exists := seen[artist]; exists {
			continue
		}
		seen[artist] = struct{}{}
		artists = append(artists, artist)
	}
	if len(artists) == 0 {
		return []string{value}
	}
	return artists
}

func parseExportDuration(value string, milliseconds bool) int {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 0
	}

	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) == 2 || len(parts) == 3 {
			total := 0
			valid := true
			for _, part := range parts {
				number, err := strconv.Atoi(strings.TrimSpace(part))
				if err != nil || number < 0 {
					valid = false
					break
				}
				total = total*60 + number
			}
			if valid {
				return total
			}
		}
	}

	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(value, "ms"), "s"))
	value = strings.ReplaceAll(value, ",", "")
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0
	}
	if milliseconds || number >= 10000 {
		number /= 1000
	}
	return int(number + 0.5)
}

func parseExportBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func exportTrackID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "spotify:track:") {
		return strings.TrimSpace(strings.TrimPrefix(value, "spotify:track:"))
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "track" {
			return strings.TrimSpace(parts[1])
		}
	}
	return value
}

func exportTrackKey(track model.SpotifyTrack) string {
	if track.ID != "" {
		return track.ID
	}
	return strings.ToLower(strings.Join([]string{
		track.Name,
		track.ArtistText(),
		strconv.Itoa(track.DurationSeconds),
	}, "\x00"))
}
