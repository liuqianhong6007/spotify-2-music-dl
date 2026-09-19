package spotify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"spotify-to-musicdl/internal/httpx"
	"spotify-to-musicdl/internal/util"
)

const (
	authorizeURL = "https://accounts.spotify.com/authorize"
	tokenURL     = "https://accounts.spotify.com/api/token"
	scopes       = "user-library-read"
)

type AuthConfig struct {
	ClientID          string
	RedirectURI       string
	TokenFile         string
	StaticAccessToken string
	OpenBrowser       bool
	Timeout           time.Duration
}

type TokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	ObtainedAt   int64  `json:"obtained_at,omitempty"`
}

type Auth struct {
	config AuthConfig
	http   *httpx.Client
	token  TokenData
}

func NewAuth(config AuthConfig) (*Auth, error) {
	auth := &Auth{
		config: config,
		http:   httpx.NewClient(45*time.Second, 3, time.Second),
	}
	if config.Timeout <= 0 {
		auth.config.Timeout = 5 * time.Minute
	}
	if err := auth.loadToken(); err != nil {
		return nil, err
	}
	return auth, nil
}

func (a *Auth) InvalidateAccessToken() {
	a.token.AccessToken = ""
	a.token.ExpiresAt = 0
}

func (a *Auth) AccessToken(ctx context.Context) (string, error) {
	if a.config.StaticAccessToken != "" {
		return a.config.StaticAccessToken, nil
	}
	if a.token.AccessToken != "" && a.token.ExpiresAt > time.Now().Add(30*time.Second).Unix() {
		return a.token.AccessToken, nil
	}
	if a.token.RefreshToken != "" {
		if err := a.refresh(ctx, a.token.RefreshToken); err != nil {
			return "", err
		}
		return a.token.AccessToken, nil
	}
	if a.token.AccessToken != "" {
		return a.token.AccessToken, nil
	}
	if err := a.authorize(ctx); err != nil {
		return "", err
	}
	return a.token.AccessToken, nil
}

func (a *Auth) loadToken() error {
	data, err := os.ReadFile(a.config.TokenFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := json.Unmarshal(data, &a.token); err != nil {
		return nil
	}
	return nil
}

func (a *Auth) saveToken() error {
	return util.AtomicWriteJSON(a.config.TokenFile, a.token)
}

func (a *Auth) tokenRequest(ctx context.Context, values url.Values) (TokenData, error) {
	if a.config.ClientID == "" {
		return TokenData{}, fmt.Errorf("缺少 Spotify Client ID，请设置 SPOTIFY_CLIENT_ID 或传入 --spotify-client-id")
	}
	response, err := a.http.Do(ctx, http.MethodPost, tokenURL, http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
		"Accept":       {"application/json"},
	}, []byte(values.Encode()))
	if err != nil {
		return TokenData{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TokenData{}, fmt.Errorf("Spotify token 请求失败 HTTP %d: %s", response.StatusCode, util.Truncate(response.Text(), 500))
	}
	var token TokenData
	if err := response.JSON(&token); err != nil {
		return TokenData{}, fmt.Errorf("Spotify token 响应异常: %w", err)
	}
	if token.AccessToken == "" {
		return TokenData{}, fmt.Errorf("Spotify token 响应缺少 access_token")
	}
	return token, nil
}

func (a *Auth) refresh(ctx context.Context, refreshToken string) error {
	token, err := a.tokenRequest(ctx, url.Values{
		"client_id":     {a.config.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	a.storeToken(token)
	return nil
}

func (a *Auth) storeToken(token TokenData) {
	if token.ExpiresIn <= 0 {
		token.ExpiresIn = 3600
	}
	token.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Unix()
	token.ObtainedAt = time.Now().Unix()
	a.token = token
	_ = a.saveToken()
}

type callbackResult struct {
	code  string
	state string
	err   error
}

func (a *Auth) authorize(ctx context.Context) error {
	parsed, err := url.Parse(a.config.RedirectURI)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return fmt.Errorf("无效的 Spotify Redirect URI: %s", a.config.RedirectURI)
	}
	if parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return fmt.Errorf("OAuth Redirect URI 必须使用 127.0.0.1 或 localhost")
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
	}
	callbackPath := parsed.Path
	if callbackPath == "" {
		callbackPath = "/callback"
	}

	verifier, challenge, err := pkcePair()
	if err != nil {
		return err
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}
	authURL := authorizeURL + "?" + url.Values{
		"client_id":             {a.config.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {a.config.RedirectURI},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge},
		"show_dialog":           {"false"},
	}.Encode()

	listener, err := net.Listen("tcp", net.JoinHostPort(parsed.Hostname(), port))
	if err != nil {
		return fmt.Errorf("无法监听 OAuth 回调端口 %s:%s: %w", parsed.Hostname(), port, err)
	}
	resultCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if callbackErr := query.Get("error"); callbackErr != "" {
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("Spotify OAuth 失败: %s", callbackErr)}:
			default:
			}
			writeOAuthPage(w, http.StatusBadRequest, "Spotify 授权失败: "+callbackErr)
			return
		}
		code := query.Get("code")
		if code == "" {
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("Spotify 回调中没有 code")}:
			default:
			}
			writeOAuthPage(w, http.StatusBadRequest, "Spotify 回调中没有 code")
			return
		}
		select {
		case resultCh <- callbackResult{code: code, state: query.Get("state")}:
		default:
		}
		writeOAuthPage(w, http.StatusOK, "授权成功，可以关闭此页面并返回终端。")
	})

	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Println("\n请在弹出的浏览器中授权 Spotify。")
	fmt.Printf("如果浏览器没有自动打开，请手动访问：\n%s\n\n", authURL)
	if a.config.OpenBrowser {
		_ = openBrowser(authURL)
	}

	waitCtx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()
	var callback callbackResult
	select {
	case <-waitCtx.Done():
		return fmt.Errorf("Spotify OAuth 超时或已取消: %w", waitCtx.Err())
	case callback = <-resultCh:
	}
	if callback.err != nil {
		return callback.err
	}
	if callback.state != state {
		return fmt.Errorf("Spotify OAuth state 校验失败")
	}

	token, err := a.tokenRequest(ctx, url.Values{
		"client_id":     {a.config.ClientID},
		"grant_type":    {"authorization_code"},
		"code":          {callback.code},
		"redirect_uri":  {a.config.RedirectURI},
		"code_verifier": {verifier},
	})
	if err != nil {
		return err
	}
	a.storeToken(token)
	return nil
}

func pkcePair() (string, string, error) {
	verifier, err := randomToken(64)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeOAuthPage(w http.ResponseWriter, status int, message string) {
	body := "<!doctype html><html lang='zh-CN'><meta charset='utf-8'><title>" + html.EscapeString(message) +
		"</title><style>body{font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;max-width:720px;margin:80px auto;padding:0 24px;line-height:1.7}</style><h2>" +
		html.EscapeString(message) + "</h2></body></html>"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func openBrowser(rawURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		command = exec.Command("xdg-open", rawURL)
	}
	return command.Start()
}
