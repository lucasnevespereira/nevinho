package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

func ensureSlice(msgs []json.RawMessage) []json.RawMessage {
	if msgs == nil {
		return []json.RawMessage{}
	}
	return msgs
}
