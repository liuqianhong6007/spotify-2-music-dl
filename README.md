# spotify-to-musicdl

使用 Go 将 Spotify “已点赞歌曲”批量同步到自建 `go-music-dl`：

1. Spotify OAuth 2.0 PKCE 授权并读取全部 liked songs。
2. 使用“歌曲名 + 歌手”调用 NAS 上的 `go-music-dl` 搜索。
3. 根据歌名、歌手、时长和音源综合评分，选择可信候选。
4. 支持两种保存方式：
   - `--save-on-nas`：调用原生 `go-music-dl` 的 `save_local=1`，直接写入 NAS。
   - 默认本地下载：从下载接口读取音频并写入 `--output-dir`。
5. 状态逐首持久化，支持中断续跑、失败重试、重复下载检查和目标位置切换。

只使用 Go 标准库，不需要下载第三方 Go 模块。要求 Go 1.22+。

## 支持的接口

程序会自动识别三种 NAS 接口：

| 模式 | 搜索接口 | 下载接口 | 直接写入 NAS |
| --- | --- | --- | --- |
| `web` | 原生 `go-music-dl` 的 `/music/search` HTML | `/music/download` | `--save-on-nas` |
| `v1` | `go-music-api` 的 `/api/v1/music/search` | `/api/v1/music/stream` | 否 |
| `compat` | `go-music-api` 的 `/music/search` JSON | `/music/download` | 否 |

原生 `go-music-dl` 默认挂载在 `/music`。`--save-on-nas` 使用其
`POST /music/download?save_local=1` 接口，保存目录由 NAS 上的
`go-music-dl` 配置决定，不受本程序 `--output-dir` 控制。

## 构建

```bash
go build -trimpath -o bin/spotify-to-musicdl ./cmd/spotify-to-musicdl
```

也可以直接运行：

```bash
go run ./cmd/spotify-to-musicdl --help
```

给 NAS 交叉编译静态二进制：

```bash
# x86_64 NAS
CGO_ENABLED=0 GOOS=linux GOARCH=amd64   go build -trimpath -o bin/spotify-to-musicdl-linux-amd64 ./cmd/spotify-to-musicdl

# ARM64 NAS
CGO_ENABLED=0 GOOS=linux GOARCH=arm64   go build -trimpath -o bin/spotify-to-musicdl-linux-arm64 ./cmd/spotify-to-musicdl
```

## 1. 准备 Spotify 应用

1. 在 Spotify Developer Dashboard 创建一个 App。
2. 在 Redirect URIs 中加入：

   ```text
   http://127.0.0.1:8888/callback
   ```

3. 记录 Client ID。本程序使用 PKCE，不需要 Client Secret。

## 2. 配置

```bash
cp .env.example .env
```

编辑 `.env`：

```dotenv
SPOTIFY_CLIENT_ID=你的_client_id
SPOTIFY_REDIRECT_URI=http://127.0.0.1:8888/callback

MUSICDL_BASE_URL=http://192.168.1.10:8080
MUSICDL_MODE=auto
MUSICDL_PREFIX=music
MUSICDL_SOURCES=
```

`MUSICDL_BASE_URL` 只写服务根地址。例如 NAS 页面是
`http://192.168.1.10:8080/music`，则应使用：

```dotenv
MUSICDL_BASE_URL=http://192.168.1.10:8080
MUSICDL_PREFIX=music
```

## 3. 运行

直接保存到 NAS：

```bash
./bin/spotify-to-musicdl \
  --musicdl-url http://192.168.1.10:8080 \
  --save-on-nas
```

首次运行会在 `http://127.0.0.1:8888/callback` 等待 Spotify 回调，并自动打开
授权页面。授权成功后 token 写入 `.spotify-token.json`，后续会自动刷新。

下载到程序所在机器：

```bash
./bin/spotify-to-musicdl \
  --musicdl-url http://192.168.1.10:8080 \
  --output-dir ./downloads
```

只搜索不下载：

```bash
./bin/spotify-to-musicdl \
  --musicdl-url http://192.168.1.10:8080 \
  --dry-run
```

显示前 20 首的候选详情：

```bash
./bin/spotify-to-musicdl \
  --musicdl-url http://192.168.1.10:8080 \
  --limit 20 \
  --verbose
```

反向代理启用登录时，可传入 Cookie：

```bash
./bin/spotify-to-musicdl \
  --musicdl-url http://192.168.1.10:8080 \
  --save-on-nas \
  --musicdl-header 'Cookie=session=xxxxx'
```

## 常用参数

```text
--musicdl-mode auto|web|v1|compat   强制指定接口类型
--sources netease,qq,kugou          指定搜索音源
--min-score 70                      匹配阈值，越高越保守
--max-duration-diff 20              最大允许时长差，单位秒
--state-file .musicdl-state.json    进度和去重状态
--delay 500ms                       每次搜索后的等待时间
--force                             忽略成功状态，强制重新处理
--skip-failed                       跳过曾经匹配/下载失败的歌曲
--no-browser                        不自动打开 Spotify 授权页面
--quiet                             减少输出
```

完整参数：

```bash
./bin/spotify-to-musicdl --help
```

## 状态和重复处理

默认状态文件是 `.musicdl-state.json`，每首完成或失败后立即落盘：

- 已成功下载或保存到 NAS 的歌曲会跳过。
- 之前失败的歌曲默认重试。
- 本地文件不存在时会重新下载。
- 本地下载和 NAS 保存互相切换时会重新处理。
- `--force` 会忽略成功状态。

状态文件不保存 Spotify access token；token 单独保存在 `.spotify-token.json`。

## 匹配逻辑

每首歌曲按“歌名 + 全部歌手”搜索，然后综合评分：

- 规范化大小写、全半角和常见标点。
- 忽略 `feat.`、部分 remaster/live/version 后缀。
- 歌名精确匹配权重最高。
- 歌手使用完整字符串和拆分后的歌手集合匹配。
- 时长差越小得分越高。
- 常见音源和无损码率有少量加分。
- 默认最低分 70，且必须有合理歌名匹配；可通过 `--min-score` 调整。

达不到阈值时不会盲选第一条结果，而是记录失败并继续下一首。

## 定时同步示例

cron 中建议使用编译后的二进制绝对路径：

```cron
0 3 * * * cd /volume1/docker/spotify-to-musicdl && ./bin/spotify-to-musicdl --musicdl-url http://127.0.0.1:8080 --save-on-nas >> sync.log 2>&1
```

首次 OAuth 必须交互完成，之后定时任务会使用缓存 token 自动刷新。

## 开发

```bash
make fmt
make vet
make test
make build
```

Go 测试已覆盖：

- 原生 `go-music-dl` HTML 搜索结果解析。
- `go-music-api` v1 JSON 响应解析。
- 歌名/歌手/时长匹配。
- 目标位置切换和本地文件缺失状态。
- Cookie 请求头解析。

## 免责声明

本项目只读取你授权账号中的点赞列表，并通过你自建的服务处理音乐资源。
请遵守 Spotify、各音乐平台及当地法律的规定，仅处理你有权访问和保存的内容。
