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
	googleDeviceURL = "https://oauth2.googleapis.com/device/code"
	googleTokenURL  = "https://oauth2.googleapis.com/token"
	googleUserURL   = "https://www.googleapis.com/oauth2/v2/userinfo"
)

var googleScopes = []string{
	"openid",
	"email",
	"profile",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/calendar.readonly",
}

// GoogleFlow handles Google's OAuth Device Flow.
type GoogleFlow struct {
	ClientID     string
	ClientSecret string
}

// StartDeviceFlow initiates the Google device authorization flow.
func (g *GoogleFlow) StartDeviceFlow(ctx context.Context) (*DeviceCode, error) {
	data := url.Values{
		"client_id": {g.ClientID},
		"scope":     {strings.Join(googleScopes, " ")},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", googleDeviceURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURL string `json:"verification_url"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("google: %s", result.ErrorDesc)
	}
	if result.DeviceCode == "" {
		return nil, fmt.Errorf("google returned empty device code")
	}

	return &DeviceCode{
		DeviceCode:      result.DeviceCode,
		UserCode:        result.UserCode,
		VerificationURI: result.VerificationURL,
		Interval:        max(result.Interval, 5),
		ExpiresIn:       result.ExpiresIn,
	}, nil
}

// PollForToken polls Google until the user authorizes or the code expires.
func (g *GoogleFlow) PollForToken(ctx context.Context, dc *DeviceCode) (*Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.After(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, fmt.Errorf("device code expired — try /connect google again")
		case <-time.After(interval):
		}

		data := url.Values{
			"client_id":     {g.ClientID},
			"client_secret": {g.ClientSecret},
			"device_code":   {dc.DeviceCode},
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
		}

		req, err := http.NewRequestWithContext(ctx, "POST", googleTokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}

		var result struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			Error        string `json:"error"`
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
				name, email, _ := fetchGoogleUser(ctx, result.AccessToken)
				return &Token{
					AccessToken:  result.AccessToken,
					RefreshToken: result.RefreshToken,
					Username:     name,
					Email:        email,
				}, nil
			}
		default:
			return nil, fmt.Errorf("google: %s", result.Error)
		}
	}
}

// RefreshToken gets a new access token using the stored refresh token.
func (g *GoogleFlow) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	data := url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"refresh_token": {refreshToken},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", googleTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, fmt.Errorf("refresh: %s", result.ErrorDesc)
	}

	name, email, _ := fetchGoogleUser(ctx, result.AccessToken)
	return &Token{
		AccessToken:  result.AccessToken,
		RefreshToken: refreshToken,
		Username:     name,
		Email:        email,
	}, nil
}

func fetchGoogleUser(ctx context.Context, token string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", googleUserURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var user struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	json.NewDecoder(resp.Body).Decode(&user)
	return user.Name, user.Email, nil
}
