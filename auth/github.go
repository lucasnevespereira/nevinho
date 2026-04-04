package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultGitHubClientID = "Ov23liB7dfadubUy1fmW"

	githubDeviceURL = "https://github.com/login/device/code"
	githubTokenURL  = "https://github.com/login/oauth/access_token"
	githubUserURL   = "https://api.github.com/user"
	githubScopes    = "repo read:user"
)

// GitHubFlow handles GitHub's OAuth Device Flow.
type GitHubFlow struct {
	ClientID string
}

// StartDeviceFlow initiates the GitHub device authorization flow.
func (g *GitHubFlow) StartDeviceFlow(ctx context.Context) (*DeviceCode, error) {
	data := url.Values{
		"client_id": {g.ClientID},
		"scope":     {githubScopes},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", githubDeviceURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("github: %s", result.ErrorDesc)
	}
	if result.DeviceCode == "" {
		return nil, fmt.Errorf("github returned empty device code")
	}

	return &DeviceCode{
		DeviceCode:      result.DeviceCode,
		UserCode:        result.UserCode,
		VerificationURI: result.VerificationURI,
		Interval:        max(result.Interval, 5),
		ExpiresIn:       result.ExpiresIn,
	}, nil
}

// PollForToken polls GitHub until the user authorizes or the code expires.
func (g *GitHubFlow) PollForToken(ctx context.Context, dc *DeviceCode) (*Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.After(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("device code expired — try /connect github again")
		case <-time.After(interval):
		}

		data := url.Values{
			"client_id":   {g.ClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}

		req, err := http.NewRequestWithContext(ctx, "POST", githubTokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue // network glitch, retry
		}

		var result struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		switch result.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "":
			if result.AccessToken != "" {
				username, _ := fetchGitHubUser(ctx, result.AccessToken)
				return &Token{
					AccessToken: result.AccessToken,
					Username:    username,
				}, nil
			}
		default:
			return nil, fmt.Errorf("github: %s", result.Error)
		}
	}
}

func fetchGitHubUser(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubUserURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var user struct {
		Login string `json:"login"`
	}
	json.NewDecoder(resp.Body).Decode(&user)
	return user.Login, nil
}
