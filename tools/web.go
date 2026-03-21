package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const httpTimeout = 15 * time.Second

type webReadInput struct {
	URL string `json:"url"`
}

func (r *Registry) webRead(input json.RawMessage) string {
	var in webReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if err := validateURL(in.URL); err != nil {
		return fmt.Sprintf("blocked: %v", err)
	}

	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(in.URL)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return fmt.Sprintf("failed to read body: %v", err)
	}

	text := extractText(string(body))
	if len(text) > maxResponseLen {
		text = text[:maxResponseLen] + "\n...(truncated)"
	}

	return text
}

type webSearchInput struct {
	Query string `json:"query"`
}

func (r *Registry) webSearch(input json.RawMessage) string {
	var in webSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	apiKey := os.Getenv("BRAVE_API_KEY")
	if apiKey == "" {
		return "web search not configured (BRAVE_API_KEY not set). Try web_read with a specific URL instead."
	}

	client := &http.Client{Timeout: httpTimeout}
	reqURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=5", url.QueryEscape(in.Query))

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Sprintf("failed to create request: %v", err)
	}
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("search failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("failed to read search response: %v", err)
	}

	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Sprintf("failed to parse results: %v", err)
	}

	var sb strings.Builder
	for i, r := range result.Web.Results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description))
	}

	if sb.Len() == 0 {
		return "no results found"
	}

	return sb.String()
}

// --- URL validation ---

func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https allowed")
	}

	host := u.Hostname()

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host")
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("internal addresses not allowed")
		}
	}

	return nil
}

// --- HTML text extraction ---

func extractText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}

	var sb strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "header" || n.Data == "footer") {
			return
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr":
				sb.WriteString("\n")
			}
		}
	}

	extract(doc)
	return strings.TrimSpace(sb.String())
}
