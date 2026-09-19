package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"spotify-to-musicdl/internal/config"
	"spotify-to-musicdl/internal/matcher"
	"spotify-to-musicdl/internal/model"
	"spotify-to-musicdl/internal/musicdl"
	"spotify-to-musicdl/internal/spotify"
	"spotify-to-musicdl/internal/state"
)

type App struct {
	options config.Options
	out     io.Writer
	errOut  io.Writer
}

type Summary struct {
	Total       int
	Downloaded  int
	Saved       int
	Skipped     int
	AlreadyDone int
	Failed      int
	DryRun      int
}

const maxDownloadAttempts = 4

func New(options config.Options, out, errOut io.Writer) *App {
	return &App{options: options, out: out, errOut: errOut}
}

func (a *App) Run(ctx context.Context) (Summary, error) {
	tracks, err := a.loadTracks(ctx)
	if err != nil {
		return Summary{}, err
	}
	if !a.options.Quiet && a.options.SpotifyExportFile == "" {
		fmt.Fprintln(a.out)
	}
	if a.options.Limit > 0 && len(tracks) > a.options.Limit {
		tracks = tracks[:a.options.Limit]
	}
	if len(tracks) == 0 {
		fmt.Fprintln(a.out, "Spotify 已点赞歌曲为空。")
		return Summary{}, nil
	}

	store, err := state.Load(a.options.MusicDLStateFile)
	if err != nil {
		return Summary{}, err
	}
	musicClient, err := musicdl.New(
		a.options.MusicDLBaseURL,
		a.options.MusicDLMode,
		a.options.MusicDLPrefix,
		a.options.MusicDLSources,
		a.options.MusicDLHeaders,
		a.options.MusicDLTimeout,
		a.options.MusicDLRetries,
		a.options.MusicDLDelay,
	)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{Total: len(tracks)}
	if !a.options.Quiet {
		destination := a.options.MusicDLOutputDir
		if a.options.MusicDLSaveOnNAS {
			destination = "NAS"
		}
		fmt.Fprintf(a.out, "共 %d 首，目标：%s，模式：%s\n", len(tracks), destination, a.options.MusicDLMode)
	}

	for index, track := range tracks {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if track.IsLocal {
			summary.Failed++
			message := "Spotify 本地歌曲没有可搜索的在线 ID，已跳过"
			if !a.options.DryRun {
				if err := store.RecordFailure(track, message, nil); err != nil {
					return summary, err
				}
			}
			if !a.options.Quiet {
				a.printTrack(index+1, len(tracks), track)
				fmt.Fprintln(a.out, "  "+message)
			}
			continue
		}

		destination := "local"
		if a.options.MusicDLSaveOnNAS {
			destination = "nas"
		}
		skip, reason := store.ShouldSkip(track, a.options.Force, a.options.SkipFailed, destination)
		if skip {
			summary.AlreadyDone++
			if !a.options.Quiet {
				a.printTrack(index+1, len(tracks), track)
				fmt.Fprintln(a.out, "  已跳过："+reason)
			}
			continue
		}

		if !a.options.Quiet {
			a.printTrack(index+1, len(tracks), track)
		}
		candidates, err := musicClient.Search(ctx, track.SearchQuery())
		if err != nil {
			summary.Failed++
			if !a.options.DryRun {
				if saveErr := store.RecordFailure(track, err.Error(), nil); saveErr != nil {
					return summary, saveErr
				}
			}
			fmt.Fprintf(a.errOut, "  搜索失败：%v\n", err)
			continue
		}
		_, ranked := matcher.ChooseBest(
			track,
			candidates,
			a.options.MinScore,
			a.options.MaxDurationDiff,
			nil,
		)
		attempts := qualifiedCandidates(ranked, a.options.MinScore, a.options.MaxDurationDiff)
		if len(attempts) == 0 {
			summary.Failed++
			message := "没有搜索到候选歌曲"
			if len(ranked) > 0 {
				message = fmt.Sprintf("未达到匹配阈值：最高 %d 分，%s [%s]", ranked[0].Score, ranked[0].Candidate.DisplayName(), ranked[0].Candidate.Source)
			}
			if !a.options.DryRun {
				if saveErr := store.RecordFailure(track, message, ranked); saveErr != nil {
					return summary, saveErr
				}
			}
			fmt.Fprintf(a.errOut, "  %s\n", message)
			if a.options.Verbose {
				a.printRanked(ranked)
			}
			continue
		}

		if a.options.Verbose {
			a.printRanked(ranked)
		}
		if a.options.DryRun {
			a.printMatch(attempts[0])
			summary.DryRun++
			continue
		}

		var result model.DownloadResult
		var matched model.MatchResult
		var lastErr error
		for attemptIndex, candidate := range attempts {
			if attemptIndex > 0 {
				fmt.Fprintf(a.out, "  尝试备用候选 %d/%d\n", attemptIndex+1, len(attempts))
			}
			a.printMatch(candidate)
			if a.options.MusicDLSaveOnNAS {
				result, err = musicClient.SaveOnNAS(ctx, candidate.Candidate)
			} else {
				result, err = musicClient.DownloadLocal(ctx, candidate.Candidate, a.options.MusicDLOutputDir, a.options.Force)
			}
			if err == nil {
				matched = candidate
				lastErr = nil
				break
			}
			lastErr = err
			if errors.Is(err, context.Canceled) {
				return summary, err
			}
			fmt.Fprintf(a.errOut, "  候选失败：%v\n", err)
		}
		if lastErr != nil {
			summary.Failed++
			if saveErr := store.RecordFailure(track, lastErr.Error(), ranked); saveErr != nil {
				return summary, saveErr
			}
			fmt.Fprintf(a.errOut, "  全部候选均失败，最后错误：%v\n", lastErr)
			continue
		}
		if err := store.RecordSuccess(track, matched, result, destination); err != nil {
			return summary, err
		}
		switch result.Status {
		case "downloaded":
			summary.Downloaded++
		case "saved":
			summary.Saved++
		default:
			summary.Skipped++
		}
		fmt.Fprintln(a.out, "  "+result.Message)
	}

	fmt.Fprintln(a.out, "\n完成。")
	if a.options.DryRun {
		fmt.Fprintf(a.out, "可匹配：%d，失败：%d\n", summary.DryRun, summary.Failed)
	} else {
		fmt.Fprintf(
			a.out,
			"新下载：%d，保存到 NAS：%d，接口跳过：%d，状态跳过：%d，失败：%d\n",
			summary.Downloaded,
			summary.Saved,
			summary.Skipped,
			summary.AlreadyDone,
			summary.Failed,
		)
		fmt.Fprintf(a.out, "状态文件：%s\n", a.options.MusicDLStateFile)
	}
	return summary, nil
}

func qualifiedCandidates(ranked []model.MatchResult, minScore, maxDurationDiff int) []model.MatchResult {
	attempts := make([]model.MatchResult, 0, maxDownloadAttempts)
	seen := make(map[string]struct{}, maxDownloadAttempts)
	for _, match := range ranked {
		if !matcher.Eligible(match, minScore, maxDurationDiff) {
			continue
		}
		key := match.Candidate.Source + "\x00" + match.Candidate.ID
		if match.Candidate.ID == "" {
			key += "\x00" + match.Candidate.DisplayName()
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		attempts = append(attempts, match)
		if len(attempts) >= maxDownloadAttempts {
			break
		}
	}
	return attempts
}

func (a *App) loadTracks(ctx context.Context) ([]model.SpotifyTrack, error) {
	if exportFile := strings.TrimSpace(a.options.SpotifyExportFile); exportFile != "" {
		if !a.options.Quiet {
			fmt.Fprintf(a.out, "正在从 Spotify 导出文件导入点赞歌曲：%s\n", exportFile)
		}
		tracks, err := spotify.LoadExportFile(exportFile)
		if err != nil {
			return nil, err
		}
		if !a.options.Quiet {
			fmt.Fprintf(a.out, "已导入 %d 首歌曲。\n", len(tracks))
		}
		return tracks, nil
	}

	auth, err := spotify.NewAuth(spotify.AuthConfig{
		ClientID:          a.options.SpotifyClientID,
		RedirectURI:       a.options.SpotifyRedirectURI,
		TokenFile:         a.options.SpotifyTokenFile,
		StaticAccessToken: a.options.SpotifyAccessToken,
		OpenBrowser:       a.options.OpenBrowser,
		Timeout:           5 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	spotifyClient := spotify.NewClient(auth, 45*time.Second, 3)
	if !a.options.Quiet {
		fmt.Fprintln(a.out, "正在读取 Spotify 已点赞歌曲...")
		spotifyClient.SetProgress(func(count, page int) {
			fmt.Fprintf(a.out, "\r已读取 Spotify 点赞歌曲：%d 首（第 %d 页）", count, page)
		})
	}
	return spotifyClient.LikedTracks(ctx, a.options.SpotifyPageSize)
}

func (a *App) printTrack(index, total int, track model.SpotifyTrack) {
	fmt.Fprintf(
		a.out,
		"\n[%d/%d] %s (%s)\n",
		index,
		total,
		track.DisplayName(),
		formatDuration(track.DurationSeconds),
	)
}

func (a *App) printMatch(match model.MatchResult) {
	fmt.Fprintf(
		a.out,
		"  匹配 %d/110: %s [%s, %s]\n",
		match.Score,
		match.Candidate.DisplayName(),
		match.Candidate.Source,
		formatDuration(match.Candidate.Duration),
	)
}

func (a *App) printRanked(ranked []model.MatchResult) {
	if len(ranked) == 0 {
		fmt.Fprintln(a.out, "  没有搜索结果")
		return
	}
	fmt.Fprintln(a.out, "  候选：")
	limit := len(ranked)
	if limit > 3 {
		limit = 3
	}
	for _, match := range ranked[:limit] {
		reason := strings.Join(match.Reasons, ", ")
		fmt.Fprintf(
			a.out,
			"    %3d 分  %s [%s, %s]",
			match.Score,
			match.Candidate.DisplayName(),
			match.Candidate.Source,
			formatDuration(match.Candidate.Duration),
		)
		if reason != "" {
			fmt.Fprintf(a.out, " (%s)", reason)
		}
		fmt.Fprintln(a.out)
	}
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "?"
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
