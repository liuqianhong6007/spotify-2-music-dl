package musicdl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"spotify-to-musicdl/internal/httpx"
	"spotify-to-musicdl/internal/model"
	"spotify-to-musicdl/internal/util"
)

type Client struct {
	baseURL       string
	prefix        string
	requestedMode string
	activeMode    string
	sources       []string
	headers       http.Header
	http          *httpx.Client
	delay         time.Duration
}

func New(baseURL, mode, prefix string, sources []string, headers map[string]string, timeout time.Duration, retries int, delay time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("缺少 go-music-dl 地址")
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix != "" {
		baseURL = stripTrailingPath(baseURL, prefix)
	}
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "web", "v1", "compat":
	default:
		return nil, fmt.Errorf("不支持的 MusicDL 模式: %s", mode)
	}
	httpHeaders := http.Header{}
	for key, value := range headers {
		httpHeaders.Set(key, value)
	}
	client := &Client{
		baseURL:       baseURL,
		prefix:        prefix,
		requestedMode: mode,
		sources:       append([]string(nil), sources...),
		headers:       httpHeaders,
		http:          httpx.NewClient(timeout, retries, time.Second),
		delay:         delay,
	}
	if mode != "auto" {
		client.activeMode = mode
	}
	return client, nil
}

func (c *Client) ActiveMode() string {
	return c.activeMode
}

func (c *Client) Search(ctx context.Context, query string) ([]model.Candidate, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	modes := []string{c.requestedMode}
	if c.requestedMode == "auto" {
		modes = []string{"v1", "compat", "web"}
	}
	errorsFound := make([]string, 0, len(modes))
	for _, mode := range modes {
		candidates, err := c.searchMode(ctx, mode, query)
		if err != nil {
			var unsupported *UnsupportedModeError
			if errors.As(err, &unsupported) {
				errorsFound = append(errorsFound, mode+": "+unsupported.Error())
				continue
			}
			return nil, err
		}
		c.activeMode = mode
		if c.delay > 0 {
			if err := sleepContext(ctx, c.delay); err != nil {
				return nil, err
			}
		}
		return candidates, nil
	}
	detail := "没有可用接口"
	if len(errorsFound) > 0 {
		detail = strings.Join(errorsFound, "；")
	}
	return nil, fmt.Errorf("无法识别 go-music-dl / go-music-api 接口。请确认 NAS 地址、--musicdl-prefix，或用 --musicdl-mode web|v1|compat 指定接口。尝试结果：%s", detail)
}

type UnsupportedModeError struct {
	Message string
}

func (e *UnsupportedModeError) Error() string {
	return e.Message
}

func (c *Client) searchMode(ctx context.Context, mode, query string) ([]model.Candidate, error) {
	rawURL := c.searchURL(mode)
	params := url.Values{}
	params.Set("q", query)
	params.Set("type", "song")
	for _, source := range c.sources {
		params.Add("sources", source)
	}
	if mode == "web" {
		params.Set("page", "1")
		params.Set("page_size", "200")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.RawQuery = params.Encode()
	headers := c.headers.Clone()
	headers.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
	response, err := c.http.Do(ctx, http.MethodGet, parsed.String(), headers, nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return nil, &UnsupportedModeError{Message: fmt.Sprintf("接口不存在 (HTTP %d)", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("搜索失败 HTTP %d: %s", response.StatusCode, util.Truncate(response.Text(), 500))
	}
	if mode == "web" {
		return parseWebSearch(response.Text(), c.baseURL)
	}
	return parseJSONSearch(response)
}

func (c *Client) searchURL(mode string) string {
	if mode == "v1" {
		return joinURL(c.baseURL, "api", "v1", "music", "search")
	}
	if c.prefix != "" {
		return joinURL(c.baseURL, c.prefix, "search")
	}
	return joinURL(c.baseURL, "search")
}

func (c *Client) downloadURL(mode string) string {
	if mode == "v1" {
		return joinURL(c.baseURL, "api", "v1", "music", "stream")
	}
	if c.prefix != "" {
		return joinURL(c.baseURL, c.prefix, "download")
	}
	return joinURL(c.baseURL, "download")
}

func parseJSONSearch(response *httpx.Response) ([]model.Candidate, error) {
	decoder := json.NewDecoder(bytes.NewReader(response.Body))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		text := strings.TrimSpace(response.Text())
		if strings.Contains(contentType, "html") || strings.HasPrefix(text, "<") {
			return nil, &UnsupportedModeError{Message: "该地址是 go-music-dl 原始 Web 页面，不是 JSON API"}
		}
		return nil, &UnsupportedModeError{Message: "响应不是 JSON"}
	}

	var songs []any
	switch typed := payload.(type) {
	case []any:
		songs = typed
	case map[string]any:
		if code, exists := typed["code"]; exists && !successCode(code) {
			return nil, fmt.Errorf("搜索接口返回错误: %v", typed["msg"])
		}
		data := typed["data"]
		if data == nil {
			data = typed
		}
		switch dataValue := data.(type) {
		case map[string]any:
			if songsValue, ok := dataValue["songs"].([]any); ok {
				songs = songsValue
			} else if listValue, ok := dataValue["list"].([]any); ok {
				songs = listValue
			} else {
				return nil, &UnsupportedModeError{Message: "JSON 中没有 songs 数组"}
			}
		case []any:
			songs = dataValue
		default:
			return nil, &UnsupportedModeError{Message: "JSON 中没有 songs 数组"}
		}
	default:
		return nil, &UnsupportedModeError{Message: "JSON 顶层结构不支持"}
	}

	candidates := make([]model.Candidate, 0, len(songs))
	for _, item := range songs {
		if candidate, ok := candidateFromJSON(item); ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

func candidateFromJSON(value any) (model.Candidate, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return model.Candidate{}, false
	}
	id := firstString(item, "id", "song_id")
	source := firstString(item, "source")
	if id == "" || source == "" {
		return model.Candidate{}, false
	}
	extra := map[string]any{}
	switch rawExtra := item["extra"].(type) {
	case map[string]any:
		extra = rawExtra
	case string:
		_ = json.Unmarshal([]byte(rawExtra), &extra)
	}
	duration := util.ParseDurationSeconds(firstValue(item, "duration", "duration_seconds"))
	if duration <= 0 {
		if milliseconds, ok := numberValue(item["duration_ms"]); ok {
			duration = int(milliseconds/1000 + 0.5)
		}
	}
	artist := firstValue(item, "artist", "artists")
	artistText := artistString(artist)

	return model.Candidate{
		ID:          id,
		Source:      source,
		Name:        firstString(item, "name"),
		Artist:      artistText,
		Album:       firstString(item, "album"),
		Duration:    duration,
		Cover:       firstString(item, "cover"),
		Extra:       extra,
		Link:        firstString(item, "link"),
		DownloadURL: firstString(item, "download_url"),
		Bitrate:     firstNonZero(firstInt(item, "bitrate"), valueInt(extra["bitrate"])),
		Size:        int64(firstNonZero(firstInt(item, "size"), valueInt(extra["size"]))),
	}, true
}

var (
	cardPattern = regexp.MustCompile(`(?is)<li\b[^>]*>.*?</li>`)
	tagPattern  = regexp.MustCompile(`(?is)<[^>]+>`)
	attrPattern = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
)

func parseWebSearch(rawHTML, baseURL string) ([]model.Candidate, error) {
	cards := cardPattern.FindAllString(rawHTML, -1)
	candidates := make([]model.Candidate, 0, len(cards))
	for _, card := range cards {
		liTag := firstTag(card, "li")
		attributes := parseAttributes(liTag)
		if !hasClass(attributes["class"], "song-card") {
			continue
		}
		id := attributes["data-id"]
		source := attributes["data-source"]
		if id == "" || source == "" {
			continue
		}
		extra := map[string]any{}
		if rawExtra := html.UnescapeString(attributes["data-extra"]); rawExtra != "" {
			_ = json.Unmarshal([]byte(rawExtra), &extra)
		}
		downloadURL := ""
		for _, tag := range tagPattern.FindAllString(card, -1) {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), "<a") {
				continue
			}
			linkAttrs := parseAttributes(tag)
			if hasClass(linkAttrs["class"], "btn-download") {
				downloadURL = resolveURL(baseURL, html.UnescapeString(linkAttrs["href"]))
				break
			}
		}
		candidates = append(candidates, model.Candidate{
			ID:          html.UnescapeString(id),
			Source:      html.UnescapeString(source),
			Name:        html.UnescapeString(attributes["data-name"]),
			Artist:      html.UnescapeString(attributes["data-artist"]),
			Album:       html.UnescapeString(attributes["data-album"]),
			Duration:    util.ParseDurationSeconds(attributes["data-duration"]),
			Cover:       html.UnescapeString(attributes["data-cover"]),
			Extra:       extra,
			Link:        html.UnescapeString(attributes["data-link"]),
			DownloadURL: downloadURL,
			Bitrate:     firstInt(extra, "bitrate"),
			Size:        int64(firstInt(extra, "size")),
		})
	}
	if len(candidates) == 0 {
		lower := strings.ToLower(rawHTML)
		if !strings.Contains(lower, "song-card") && !strings.Contains(lower, "go-music-dl") {
			return nil, &UnsupportedModeError{Message: "响应既不是 JSON API，也不像 go-music-dl 搜索页面"}
		}
	}
	return candidates, nil
}

func (c *Client) DownloadLocal(ctx context.Context, candidate model.Candidate, outputDir string, overwrite bool) (model.DownloadResult, error) {
	downloadURL := c.buildDownloadURL(candidate)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return model.DownloadResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt <= c.http.Retries; attempt++ {
		result, retry, err := c.downloadOnce(ctx, downloadURL, outputDir, candidate, overwrite)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retry || attempt >= c.http.Retries {
			break
		}
		if err := sleepContext(ctx, backoff(c.http.RetryDelay, attempt)); err != nil {
			return model.DownloadResult{}, err
		}
	}
	return model.DownloadResult{}, fmt.Errorf("下载失败: %w", lastErr)
}

func (c *Client) downloadOnce(ctx context.Context, downloadURL, outputDir string, candidate model.Candidate, overwrite bool) (model.DownloadResult, bool, error) {
	response, err := c.openDownload(ctx, downloadURL)
	if err != nil {
		return model.DownloadResult{}, isRetryableError(err), err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 500))
		if retryableStatus(response.StatusCode) {
			return model.DownloadResult{}, true, fmt.Errorf("HTTP %d: %s", response.StatusCode, string(body))
		}
		return model.DownloadResult{}, false, fmt.Errorf("下载接口返回 HTTP %d: %s", response.StatusCode, string(body))
	}

	contentType := response.Header.Get("Content-Type")
	dispositionName := contentDispositionFilename(response.Header.Get("Content-Disposition"))
	extension := cleanExtension(extraString(candidate.Extra, "ext"))
	if extension == "" {
		extension = cleanExtension(strings.TrimPrefix(filepath.Ext(dispositionName), "."))
	}
	if extension == "" {
		extension = extensionFromContentType(contentType)
	}
	if extension == "" {
		extension = "mp3"
	}
	baseFilename := ""
	if dispositionName != "" {
		baseFilename = strings.TrimSuffix(filepath.Base(dispositionName), filepath.Ext(dispositionName))
	} else {
		baseFilename = strings.Trim(strings.TrimSpace(candidate.Name+" - "+candidate.Artist), " -")
	}
	filename := util.SanitizeFilename(baseFilename, "Unknown") + "." + extension
	finalPath := filepath.Join(outputDir, filename)
	if _, err := os.Stat(finalPath); err == nil && !overwrite {
		return model.DownloadResult{
			Status:   stateSkipped,
			Path:     finalPath,
			Filename: filename,
			Message:  "目标文件已存在: " + finalPath,
			Skipped:  true,
		}, false, nil
	}

	temp, err := os.CreateTemp(outputDir, ".download-*.part")
	if err != nil {
		return model.DownloadResult{}, false, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(temp, response.Body); err != nil {
		temp.Close()
		return model.DownloadResult{}, true, fmt.Errorf("下载数据中断: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return model.DownloadResult{}, true, fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return model.DownloadResult{}, true, fmt.Errorf("关闭临时文件失败: %w", err)
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return model.DownloadResult{}, false, err
	}
	if info.Size() <= 0 {
		return model.DownloadResult{}, false, fmt.Errorf("下载接口返回了空文件")
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return model.DownloadResult{}, false, fmt.Errorf("写入文件失败 %s: %w", finalPath, err)
	}
	return model.DownloadResult{
		Status:   stateDownloaded,
		Path:     finalPath,
		Filename: filename,
		Message:  "已下载到 " + finalPath,
	}, false, nil
}

func (c *Client) SaveOnNAS(ctx context.Context, candidate model.Candidate) (model.DownloadResult, error) {
	if c.activeMode != "web" {
		return model.DownloadResult{}, fmt.Errorf("--save-on-nas 仅支持原生 go-music-dl Web 模式；go-music-api 不提供 save_local")
	}
	rawURL, err := httpx.AddQuery(c.buildDownloadURL(candidate), map[string]string{"save_local": "1"})
	if err != nil {
		return model.DownloadResult{}, err
	}
	headers := c.headers.Clone()
	headers.Set("X-Requested-With", "XMLHttpRequest")
	headers.Set("Accept", "application/json")
	headers.Set("Content-Length", "0")
	response, err := c.http.Do(ctx, http.MethodPost, rawURL, headers, []byte{})
	if err != nil {
		return model.DownloadResult{}, err
	}
	switch response.StatusCode {
	case http.StatusMethodNotAllowed:
		return model.DownloadResult{}, fmt.Errorf("NAS 上的 go-music-dl 版本不支持 save_local=1，或反向代理拒绝了 POST")
	case http.StatusForbidden:
		return model.DownloadResult{}, fmt.Errorf("NAS 返回 403。若配置了反向代理鉴权，请用 --musicdl-header 传入 Cookie/Token")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return model.DownloadResult{}, fmt.Errorf("保存到 NAS 失败 HTTP %d: %s", response.StatusCode, util.Truncate(response.Text(), 500))
	}
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		return model.DownloadResult{}, fmt.Errorf("NAS 未返回 save_local JSON；可能不是原生 go-music-dl Web 模式")
	}
	var payload map[string]any
	if err := response.JSON(&payload); err != nil {
		return model.DownloadResult{}, err
	}
	if fmt.Sprint(payload["status"]) != "ok" && !boolValue(payload["saved"]) {
		return model.DownloadResult{}, fmt.Errorf("保存到 NAS 失败: %v", payload)
	}
	skipped := boolValue(payload["skipped"])
	filename := fmt.Sprint(payload["filename"])
	path := fmt.Sprint(payload["path"])
	if filename == "<nil>" {
		filename = ""
	}
	if path == "<nil>" {
		path = ""
	}
	message := "已保存到 NAS: " + firstNonEmpty(path, filename)
	if skipped {
		message = "NAS 已有相同歌曲，已跳过"
	}
	if warning := fmt.Sprint(payload["warning"]); warning != "" && warning != "<nil>" {
		message += "（warning: " + warning + "）"
	}
	status := stateSaved
	if skipped {
		status = stateSkipped
	}
	return model.DownloadResult{
		Status:   status,
		Path:     path,
		Filename: filename,
		Message:  message,
		Skipped:  skipped,
	}, nil
}

func (c *Client) buildDownloadURL(candidate model.Candidate) string {
	base := candidate.DownloadURL
	if base == "" {
		mode := c.activeMode
		if mode == "" {
			mode = "web"
		}
		base = c.downloadURL(mode)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	query.Set("id", candidate.ID)
	query.Set("source", candidate.Source)
	query.Set("name", candidate.Name)
	query.Set("artist", candidate.Artist)
	if candidate.Album != "" {
		query.Set("album", candidate.Album)
	}
	if candidate.Duration > 0 {
		query.Set("duration", strconv.Itoa(candidate.Duration))
	}
	if candidate.Cover != "" {
		query.Set("cover", candidate.Cover)
	}
	if len(candidate.Extra) > 0 {
		if extraJSON, err := json.Marshal(candidate.Extra); err == nil {
			query.Set("extra", string(extraJSON))
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (c *Client) openDownload(ctx context.Context, rawURL string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.http.Retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		for key, values := range c.headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		req.Header.Set("User-Agent", httpx.UserAgent)
		req.Header.Set("Accept", "audio/*, application/octet-stream;q=0.9, */*;q=0.8")
		response, err := c.http.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if attempt >= c.http.Retries {
				break
			}
			if err := sleepContext(ctx, backoff(c.http.RetryDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if retryableStatus(response.StatusCode) && attempt < c.http.Retries {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
			response.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			if err := sleepContext(ctx, backoff(c.http.RetryDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		return response, nil
	}
	return nil, lastErr
}

func stripTrailingPath(baseURL, suffix string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	path := strings.TrimRight(parsed.Path, "/")
	wanted := "/" + strings.Trim(suffix, "/")
	if strings.HasSuffix(strings.ToLower(path), strings.ToLower(wanted)) {
		parsed.Path = strings.TrimSuffix(path, path[len(path)-len(wanted):])
		return strings.TrimRight(parsed.String(), "/")
	}
	return baseURL
}

func joinURL(base string, parts ...string) string {
	clean := make([]string, 0, len(parts)+1)
	clean = append(clean, strings.TrimRight(base, "/"))
	for _, part := range parts {
		if trimmed := strings.Trim(part, "/"); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return strings.Join(clean, "/")
}

func successCode(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case float64:
		return typed == 0 || typed == 200
	case json.Number:
		number, err := typed.Int64()
		return err == nil && (number == 0 || number == 200)
	case int:
		return typed == 0 || typed == 200
	case string:
		return typed == "" || typed == "0" || typed == "200"
	default:
		return false
	}
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func firstInt(values map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if number, ok := numberValue(value); ok {
				return int(number + 0.5)
			}
		}
	}
	return 0
}

func valueInt(value any) int {
	if number, ok := numberValue(value); ok {
		return int(number + 0.5)
	}
	return 0
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func artistString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			switch artist := item.(type) {
			case string:
				if trimmed := strings.TrimSpace(artist); trimmed != "" {
					parts = append(parts, trimmed)
				}
			case map[string]any:
				if name := firstString(artist, "name"); name != "" {
					parts = append(parts, name)
				}
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		return firstString(typed, "name")
	default:
		return ""
	}
}

func parseAttributes(tag string) map[string]string {
	result := map[string]string{}
	for _, match := range attrPattern.FindAllStringSubmatch(tag, -1) {
		if len(match) < 5 {
			continue
		}
		key := strings.ToLower(match[1])
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if value == "" {
			value = match[4]
		}
		result[key] = html.UnescapeString(value)
	}
	return result
}

func firstTag(fragment, name string) string {
	lower := strings.ToLower(fragment)
	prefix := "<" + strings.ToLower(name)
	start := strings.Index(lower, prefix)
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(fragment[start:], '>')
	if end < 0 {
		return fragment[start:]
	}
	return fragment[start : start+end+1]
}

func hasClass(value, wanted string) bool {
	for _, class := range strings.Fields(value) {
		if strings.EqualFold(class, wanted) {
			return true
		}
	}
	return false
}

func resolveURL(baseURL, reference string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return reference
	}
	ref, err := url.Parse(reference)
	if err != nil {
		return reference
	}
	return base.ResolveReference(ref).String()
}

func contentDispositionFilename(value string) string {
	if value == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(value); err == nil {
		if filename := strings.TrimSpace(params["filename"]); filename != "" {
			return filename
		}
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)filename\*\s*=\s*UTF-8''([^;\r\n]+)`),
		regexp.MustCompile(`(?i)filename\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`(?i)filename\s*=\s*([^;\r\n]+)`),
	}
	for index, pattern := range patterns {
		match := pattern.FindStringSubmatch(value)
		if len(match) < 2 {
			continue
		}
		filename := strings.Trim(match[1], ` "'`)
		if index == 0 {
			if decoded, err := url.PathUnescape(filename); err == nil {
				filename = decoded
			}
		}
		if filename != "" {
			return filename
		}
	}
	return ""
}

func extensionFromContentType(contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	mapping := map[string]string{
		"audio/mpeg":   "mp3",
		"audio/mp3":    "mp3",
		"audio/flac":   "flac",
		"audio/x-flac": "flac",
		"audio/mp4":    "m4a",
		"audio/x-m4a":  "m4a",
		"audio/aac":    "aac",
		"audio/ogg":    "ogg",
		"audio/opus":   "opus",
		"audio/wav":    "wav",
		"audio/x-wav":  "wav",
	}
	if extension, ok := mapping[mediaType]; ok {
		return extension
	}
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
		return cleanExtension(extensions[0])
	}
	return ""
}

func cleanExtension(value string) string {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
	if regexp.MustCompile(`^[a-z0-9]{1,8}$`).MatchString(value) {
		return value
	}
	return ""
}

func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	return fmt.Sprint(extra[key])
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || typed == "1"
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if attempt > 6 {
		attempt = 6
	}
	return base * time.Duration(1<<attempt)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableError(err error) bool {
	var networkErr interface{ Timeout() bool }
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return true
	}
	return true
}

const (
	stateDownloaded = "downloaded"
	stateSaved      = "saved"
	stateSkipped    = "skipped"
)
