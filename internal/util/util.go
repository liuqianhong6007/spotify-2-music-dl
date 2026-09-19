package util

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func LoadDotenv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			quote := value[0]
			if end := strings.LastIndexByte(value[1:], quote); end >= 0 {
				value = value[1 : end+1]
			} else {
				value = value[1:]
			}
		} else if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func EnvBool(name string, fallback bool) bool {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func SplitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := regexp.MustCompile(`[,;\n]+`).Split(value, -1)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func ParseHeaders(values []string, envValue string) map[string]string {
	result := map[string]string{}
	rawValues := append([]string(nil), values...)
	if envValue != "" {
		rawValues = append(rawValues, regexp.MustCompile(`\n|\|\|`).Split(envValue, -1)...)
	}
	for _, raw := range rawValues {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		var key, value string
		if before, after, ok := strings.Cut(item, ":"); ok {
			key, value = before, after
		} else if before, after, ok := strings.Cut(item, "="); ok {
			key, value = before, after
		} else {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			result[key] = value
		}
	}
	return result
}

func AtomicWriteJSON(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func SanitizeFilename(value, fallback string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r < 0x20 || r == 0x7f:
			builder.WriteRune('_')
		case strings.ContainsRune(`/\:*?"<>|`, r):
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	result := strings.Trim(builder.String(), " .")
	if result == "" {
		return fallback
	}
	return result
}

func ParseDurationSeconds(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return int(typed + 0.5)
	case float32:
		return int(typed + 0.5)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0
		}
		return int(number + 0.5)
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0
		}
		return int(number + 0.5)
	default:
		return 0
	}
}

func IsFullwidthASCII(r rune) bool {
	return r >= 0xFF01 && r <= 0xFF5E
}

func FoldRune(r rune) rune {
	if IsFullwidthASCII(r) {
		return r - 0xFEE0
	}
	if r == 0x3000 {
		return ' '
	}
	return unicode.ToLower(r)
}

func Truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
