package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucasnevespereira/nevinho/logger"
)

const maxRetries = 3

func doHTTP(ctx context.Context, url string, body interface{}, headers map[string]string) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := range maxRetries {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < maxRetries-1 {
				logger.Info(fmt.Sprintf("retry %d/%d: %v", attempt+1, maxRetries-1, err))
				if !sleepWithContext(ctx, backoffDelay(attempt)) {
					return nil, ctx.Err()
				}
			}
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == 200 {
			return respBody, nil
		}

		lastErr = fmt.Errorf("API %d: %s", resp.StatusCode, string(respBody))

		if !isRetryable(resp.StatusCode) || attempt == maxRetries-1 {
			return nil, lastErr
		}

		delay := backoffDelay(attempt)
		if resp.StatusCode == 429 {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil && secs > 0 && secs <= 60 {
					delay = time.Duration(secs) * time.Second
				}
			}
		}
		logger.Info(fmt.Sprintf("retry %d/%d: API %d", attempt+1, maxRetries-1, resp.StatusCode))
		if !sleepWithContext(ctx, delay) {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func isRetryable(code int) bool {
	return code == 429 || code >= 500
}

func backoffDelay(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func doSSE(ctx context.Context, url string, body interface{}, headers map[string]string, onData func([]byte) error) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var event strings.Builder
	flush := func() error {
		if event.Len() == 0 {
			return nil
		}
		data := strings.TrimSpace(event.String())
		event.Reset()
		if data == "" || data == "[DONE]" {
			return nil
		}
		return onData([]byte(data))
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		if event.Len() > 0 {
			event.WriteByte('\n')
		}
		event.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return flush()
}

func ensureSlice(msgs []json.RawMessage) []json.RawMessage {
	if msgs == nil {
		return []json.RawMessage{}
	}
	return msgs
}
