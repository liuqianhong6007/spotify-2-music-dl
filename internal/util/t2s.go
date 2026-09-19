package util

import (
	_ "embed"
	"strings"
)

// OpenCC TSCharacters.txt, Apache-2.0. Keeping it embedded makes matching
// deterministic and avoids a runtime dependency on network or system data.
//
//go:embed t2s.txt
var traditionalSimplifiedData string

var traditionalSimplified = parseTraditionalSimplified(traditionalSimplifiedData)

func parseTraditionalSimplified(data string) map[rune]rune {
	result := make(map[rune]rune)
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		source := []rune(fields[0])
		target := []rune(fields[1])
		if len(source) != 1 || len(target) != 1 {
			continue
		}
		result[source[0]] = target[0]
	}
	return result
}

// ToSimplified converts common Traditional Chinese characters to their default
// Simplified form. Phrase disambiguation is intentionally not attempted here;
// the character-level mapping is sufficient for conservative title matching.
func ToSimplified(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if simplified, ok := traditionalSimplified[r]; ok {
			builder.WriteRune(simplified)
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
