package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"spotify-to-musicdl/internal/util"
)

type Options struct {
	SpotifyClientID    string
	SpotifyRedirectURI string
	SpotifyTokenFile   string
	SpotifyAccessToken string
	SpotifyPageSize    int
	OpenBrowser        bool

	MusicDLBaseURL   string
	MusicDLMode      string
	MusicDLPrefix    string
	MusicDLSources   []string
	MusicDLHeaders   map[string]string
	MusicDLOutputDir string
	MusicDLStateFile string
	MusicDLSaveOnNAS bool
	MusicDLDelay     time.Duration
	MusicDLTimeout   time.Duration
	MusicDLRetries   int

	MinScore        int
	MaxDurationDiff int
	Limit           int
	Force           bool
	DryRun          bool
	SkipFailed      bool
	Quiet           bool
	Verbose         bool
	Version         bool
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func Parse(args []string, version string) (Options, error) {
	envFile := preparseEnvFile(args)
	if err := util.LoadDotenv(envFile); err != nil {
		return Options{}, fmt.Errorf("读取 %s 失败: %w", envFile, err)
	}

	var opts Options
	var musicDLHeaders stringList
	var envTimeout = envDuration("MUSICDL_TIMEOUT", 45*time.Second)

	fs := flag.NewFlagSet("spotify-to-musicdl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&envFile, "env-file", envFile, "环境变量文件")
	fs.BoolVar(&opts.Version, "version", false, "显示版本")

	fs.StringVar(&opts.SpotifyClientID, "spotify-client-id", os.Getenv("SPOTIFY_CLIENT_ID"), "Spotify Developer App Client ID")
	fs.StringVar(&opts.SpotifyRedirectURI, "spotify-redirect-uri", envDefault("SPOTIFY_REDIRECT_URI", "http://127.0.0.1:8888/callback"), "Spotify OAuth Redirect URI")
	fs.StringVar(&opts.SpotifyTokenFile, "spotify-token-file", envDefault("SPOTIFY_TOKEN_FILE", ".spotify-token.json"), "OAuth token 缓存文件")
	fs.StringVar(&opts.SpotifyAccessToken, "spotify-access-token", os.Getenv("SPOTIFY_ACCESS_TOKEN"), "直接使用短期 access token")
	fs.IntVar(&opts.SpotifyPageSize, "spotify-page-size", envInt("SPOTIFY_PAGE_SIZE", 50), "Spotify 分页大小，最大 50")

	noBrowser := fs.Bool("no-browser", false, "不自动打开 OAuth 授权页面")

	fs.StringVar(&opts.MusicDLBaseURL, "musicdl-url", os.Getenv("MUSICDL_BASE_URL"), "NAS 服务根地址")
	fs.StringVar(&opts.MusicDLMode, "musicdl-mode", envDefault("MUSICDL_MODE", "auto"), "接口模式: auto/web/v1/compat")
	fs.StringVar(&opts.MusicDLPrefix, "musicdl-prefix", envDefault("MUSICDL_PREFIX", "music"), "原生 go-music-dl URL 前缀")
	fs.StringVar(&opts.MusicDLOutputDir, "output-dir", envDefault("MUSICDL_OUTPUT_DIR", "downloads"), "本地下载目录")
	fs.StringVar(&opts.MusicDLStateFile, "state-file", envDefault("MUSICDL_STATE_FILE", ".musicdl-state.json"), "状态文件")
	fs.BoolVar(&opts.MusicDLSaveOnNAS, "save-on-nas", util.EnvBool("MUSICDL_SAVE_ON_NAS", false), "直接保存到 NAS")
	fs.DurationVar(&opts.MusicDLDelay, "delay", envDuration("MUSICDL_DELAY", 500*time.Millisecond), "每次搜索后的等待时间")
	fs.DurationVar(&opts.MusicDLTimeout, "timeout", envTimeout, "HTTP 超时")
	fs.IntVar(&opts.MusicDLRetries, "retries", envInt("MUSICDL_RETRIES", 3), "瞬时网络错误重试次数")
	fs.Var(&musicDLHeaders, "musicdl-header", "额外请求头，可重复。格式 Key=Value")
	sources := fs.String("sources", os.Getenv("MUSICDL_SOURCES"), "指定音乐源，逗号分隔")

	fs.IntVar(&opts.MinScore, "min-score", envInt("MUSICDL_MIN_SCORE", 70), "匹配最低分")
	fs.IntVar(&opts.MaxDurationDiff, "max-duration-diff", envInt("MUSICDL_MAX_DURATION_DIFF", 20), "最大允许时长差（秒）")
	fs.IntVar(&opts.Limit, "limit", envInt("MUSICDL_LIMIT", 0), "只处理前 N 首")
	fs.BoolVar(&opts.Force, "force", false, "忽略成功状态重新处理")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "只搜索不下载")
	fs.BoolVar(&opts.SkipFailed, "skip-failed", false, "跳过此前失败项")
	fs.BoolVar(&opts.Quiet, "quiet", false, "减少输出")
	fs.BoolVar(&opts.Quiet, "q", false, "减少输出（简写）")
	fs.BoolVar(&opts.Verbose, "verbose", false, "显示候选详情")
	fs.BoolVar(&opts.Verbose, "v", false, "显示候选详情（简写）")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "spotify-to-musicdl %s\n\n", version)
		fmt.Fprintln(fs.Output(), "读取 Spotify 已点赞歌曲，通过 NAS 上的 go-music-dl 搜索并保存。")
		fmt.Fprintln(fs.Output(), "\n参数：")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	if opts.Version {
		return opts, nil
	}
	opts.OpenBrowser = !*noBrowser
	opts.MusicDLMode = strings.ToLower(strings.TrimSpace(opts.MusicDLMode))
	opts.MusicDLPrefix = strings.Trim(strings.TrimSpace(opts.MusicDLPrefix), "/")
	opts.MusicDLSources = util.SplitCSV(*sources)
	opts.MusicDLHeaders = util.ParseHeaders(musicDLHeaders, os.Getenv("MUSICDL_HEADERS"))

	if opts.MusicDLBaseURL == "" {
		return Options{}, fmt.Errorf("缺少 NAS 服务地址。请设置 MUSICDL_BASE_URL 或传入 --musicdl-url")
	}
	if opts.SpotifyPageSize < 1 || opts.SpotifyPageSize > 50 {
		return Options{}, fmt.Errorf("--spotify-page-size 必须在 1 到 50 之间")
	}
	switch opts.MusicDLMode {
	case "auto", "web", "v1", "compat":
	default:
		return Options{}, fmt.Errorf("--musicdl-mode 必须是 auto、web、v1 或 compat")
	}
	if opts.MusicDLSaveOnNAS && opts.MusicDLMode != "auto" && opts.MusicDLMode != "web" {
		return Options{}, fmt.Errorf("--save-on-nas 只能使用 --musicdl-mode web")
	}
	if opts.MinScore < 0 || opts.MinScore > 110 {
		return Options{}, fmt.Errorf("--min-score 必须在 0 到 110 之间")
	}
	if opts.MaxDurationDiff <= 0 {
		return Options{}, fmt.Errorf("--max-duration-diff 必须大于 0")
	}
	if opts.MusicDLRetries < 0 {
		return Options{}, fmt.Errorf("--retries 不能为负数")
	}
	if opts.MusicDLTimeout <= 0 {
		return Options{}, fmt.Errorf("--timeout 必须大于 0")
	}
	if opts.MusicDLDelay < 0 {
		return Options{}, fmt.Errorf("--delay 不能为负数")
	}
	return opts, nil
}

func preparseEnvFile(args []string) string {
	for i, arg := range args {
		if arg == "--env-file" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--env-file=") {
			return strings.TrimPrefix(arg, "--env-file=")
		}
	}
	return ".env"
}

func envDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if value, err := time.ParseDuration(raw); err == nil {
		return value
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		return time.Duration(seconds * float64(time.Second))
	}
	return fallback
}
