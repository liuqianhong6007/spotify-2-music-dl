package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const UserAgent = "spotify-to-musicdl-go/0.1"

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (r *Response) Text() string {
	return string(r.Body)
}

func (r *Response) JSON(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("响应不是有效 JSON: %w; body=%q", err, truncate(r.Text(), 300))
	}
	return nil
}

type Client struct {
	HTTP       *http.Client
	Retries    int
	RetryDelay time.Duration
}

func NewClient(timeout time.Duration, retries int, retryDelay time.Duration) *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: timeout},
		Retries:    retries,
		RetryDelay: retryDelay,
	}
}

func (c *Client) Do(
	ctx context.Context,
	method string,
	rawURL string,
	headers http.Header,
	body []byte,
) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", UserAgent)
		}
		if len(body) > 0 && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if body != nil {
			req.ContentLength = int64(len(body))
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if attempt >= c.Retries {
				break
			}
			if err := sleepContext(ctx, backoff(c.RetryDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt >= c.Retries {
				break
			}
			if err := sleepContext(ctx, backoff(c.RetryDelay, attempt)); err != nil {
				return nil, err
			}
			continue
		}

		result := &Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       data,
		}
		if !retryableStatus(resp.StatusCode) || attempt >= c.Retries {
			return result, nil
		}

		delay := backoff(c.RetryDelay, attempt)
		if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
				serverDelay := time.Duration(seconds) * time.Second
				if serverDelay > delay {
					delay = serverDelay
				}
			}
		}
		if err := sleepContext(ctx, delay); err != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("request failed")
	}
	return nil, lastErr
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
	shift := attempt
	if shift > 6 {
		shift = 6
	}
	return base * time.Duration(1<<shift)
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

func AddQuery(rawURL string, params map[string]string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}
