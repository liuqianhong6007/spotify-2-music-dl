package matcher

import (
	"regexp"
	"sort"
	"strings"

	"spotify-to-musicdl/internal/model"
	"spotify-to-musicdl/internal/util"
)

var (
	featPattern            = regexp.MustCompile(`(?i)\b(feat|featuring|ft)\.?\s+.*$`)
	cleanPattern           = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	bracketVersionPattern  = regexp.MustCompile(`(?i)\s*[\[\(（【]\s*(?:feat|featuring|ft|live|remaster(?:ed)?|deluxe|explicit|mono|stereo|version|版|现场|重制)[^\]\)）】]*[\]\)）】]\s*`)
	suffixVersionPattern   = regexp.MustCompile(`(?i)\s*-\s*(?:remaster(?:ed)?|live|radio edit|single version|album version|deluxe|explicit|mono|stereo)\b.*$`)
	artistSeparatorPattern = regexp.MustCompile(`\s*(?:/|、|,|;|；)\s*|\s+and\s+`)
)

var defaultSourcePriority = []string{
	"netease", "qq", "kugou", "kuwo", "migu", "qianqian", "joox", "bilibili",
}

func NormalizeText(value string) string {
	value = strings.Map(util.FoldRune, strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "&", " and ")
	value = featPattern.ReplaceAllString(value, " ")
	value = cleanPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func NormalizeTitle(value string) string {
	value = strings.Map(util.FoldRune, strings.TrimSpace(value))
	value = featPattern.ReplaceAllString(value, " ")
	value = bracketVersionPattern.ReplaceAllString(value, " ")
	value = suffixVersionPattern.ReplaceAllString(value, " ")
	return NormalizeText(value)
}

func Score(track model.SpotifyTrack, candidate model.Candidate, priorities []string) model.MatchResult {
	titleScore := scoreTitle(track.Name, candidate.Name)
	artistScore := scoreArtist(track, candidate.Artist)
	durationScore, durationDiff := scoreDuration(track.DurationSeconds, candidate.Duration)

	reasons := make([]string, 0, 5)
	if titleScore > 0 {
		reasons = append(reasons, "标题+"+itoa(titleScore))
	}
	if artistScore > 0 {
		reasons = append(reasons, "歌手+"+itoa(artistScore))
	}
	if durationScore != 0 {
		reasons = append(reasons, "时长"+signed(durationScore))
	}
	if len(priorities) == 0 {
		priorities = defaultSourcePriority
	}
	sourceBonus := 0
	for index, source := range priorities {
		if strings.EqualFold(strings.TrimSpace(source), strings.TrimSpace(candidate.Source)) {
			sourceBonus = 3 - index/3
			if sourceBonus < 0 {
				sourceBonus = 0
			}
			if sourceBonus > 0 {
				reasons = append(reasons, "音源+"+itoa(sourceBonus))
			}
			break
		}
	}
	bitrateBonus := 0
	if candidate.Bitrate >= 900 {
		bitrateBonus = 2
	} else if candidate.Bitrate >= 300 {
		bitrateBonus = 1
	}
	score := titleScore + artistScore + durationScore + sourceBonus + bitrateBonus
	if score < 0 {
		score = 0
	}
	if score > 110 {
		score = 110
	}
	return model.MatchResult{
		Candidate:     candidate,
		Score:         score,
		TitleScore:    titleScore,
		ArtistScore:   artistScore,
		DurationScore: durationScore,
		DurationDiff:  durationDiff,
		Reasons:       reasons,
	}
}

func Rank(track model.SpotifyTrack, candidates []model.Candidate, priorities []string) []model.MatchResult {
	results := make([]model.MatchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, Score(track, candidate, priorities))
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := results[i], results[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.TitleScore != right.TitleScore {
			return left.TitleScore > right.TitleScore
		}
		if left.ArtistScore != right.ArtistScore {
			return left.ArtistScore > right.ArtistScore
		}
		if left.DurationDiff != right.DurationDiff {
			return left.DurationDiff < right.DurationDiff
		}
		return left.Candidate.Bitrate > right.Candidate.Bitrate
	})
	return results
}

func ChooseBest(track model.SpotifyTrack, candidates []model.Candidate, minScore, maxDurationDiff int, priorities []string) (*model.MatchResult, []model.MatchResult) {
	ranked := Rank(track, candidates, priorities)
	if len(ranked) == 0 {
		return nil, ranked
	}
	best := ranked[0]
	if best.Score < minScore {
		return nil, ranked
	}
	if best.TitleScore < 25 {
		return nil, ranked
	}
	if best.DurationDiff > maxDurationDiff {
		return nil, ranked
	}
	if best.ArtistScore == 0 && best.TitleScore < 52 {
		return nil, ranked
	}
	return &best, ranked
}

func scoreTitle(spotifyTitle, candidateTitle string) int {
	left := NormalizeTitle(spotifyTitle)
	right := NormalizeTitle(candidateTitle)
	switch ratio := containsScore(left, right); {
	case ratio == 100:
		return 65
	case ratio >= 72:
		return 52
	case ratio >= 65:
		return 42
	case ratio >= 45:
		return 25
	default:
		return 0
	}
}

func scoreArtist(track model.SpotifyTrack, candidateArtist string) int {
	candidate := NormalizeText(candidateArtist)
	if candidate == "" {
		return 0
	}
	spotifyFull := NormalizeText(track.ArtistText())
	if spotifyFull != "" {
		if spotifyFull == candidate {
			return 25
		}
		switch ratio := containsScore(spotifyFull, candidate); {
		case ratio == 100:
			return 25
		case ratio >= 72:
			return 22
		case ratio >= 65:
			return 18
		}
	}
	requested := artistTokens(track.ArtistText())
	found := artistTokens(candidateArtist)
	if len(requested) == 0 || len(found) == 0 {
		return 0
	}
	if sameStringSet(requested, found) {
		return 25
	}
	for left := range requested {
		if _, ok := found[left]; ok {
			return 21
		}
	}
	for left := range requested {
		for right := range found {
			if containsScore(left, right) >= 72 {
				return 19
			}
		}
	}
	return 0
}

func scoreDuration(expected, actual int) (int, int) {
	if expected <= 0 || actual <= 0 {
		return 4, 0
	}
	diff := expected - actual
	if diff < 0 {
		diff = -diff
	}
	switch {
	case diff <= 2:
		return 20, diff
	case diff <= 5:
		return 15, diff
	case diff <= 10:
		return 9, diff
	case diff <= 20:
		return 3, diff
	case diff <= 30:
		return -8, diff
	default:
		return -25, diff
	}
}

func containsScore(left, right string) int {
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 100
	}
	shorter, longer := left, right
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	if len([]rune(shorter)) >= 2 && strings.Contains(longer, shorter) {
		return 72
	}
	ratio := int(levenshteinRatio(left, right)*100 + 0.5)
	switch {
	case ratio >= 90:
		return 65
	case ratio >= 80:
		return 45
	default:
		return 0
	}
}

func artistTokens(value string) map[string]bool {
	normalized := NormalizeText(value)
	result := map[string]bool{}
	for _, token := range artistSeparatorPattern.Split(normalized, -1) {
		token = strings.TrimSpace(token)
		if token != "" {
			result[token] = true
		}
	}
	return result
}

func sameStringSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if !right[key] {
			return false
		}
	}
	return true
}

func levenshteinRatio(left, right string) float64 {
	a := []rune(left)
	b := []rune(right)
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	distance := previous[len(b)]
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	return 1 - float64(distance)/float64(maxLen)
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [20]byte{}
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buf[index] = '-'
	}
	return string(buf[index:])
}

func signed(value int) string {
	if value > 0 {
		return "+" + itoa(value)
	}
	return itoa(value)
}
