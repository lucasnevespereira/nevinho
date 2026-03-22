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

	// Use Brave if configured, otherwise fall back to DuckDuckGo
	if apiKey := os.Getenv("BRAVE_API_KEY"); apiKey != "" {
		return searchBrave(in.Query, apiKey)
	}
	return searchDuckDuckGo(in.Query)
}

func searchBrave(query, apiKey string) string {
	client := &http.Client{Timeout: httpTimeout}
	reqURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=5", url.QueryEscape(query))

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

	return formatSearchResults(result.Web.Results)
}

func searchDuckDuckGo(query string) string {
	client := &http.Client{Timeout: httpTimeout}
	reqURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Sprintf("failed to create request: %v", err)
	}
	req.Header.Set("User-Agent", "Nevinho/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("search failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return fmt.Sprintf("failed to read response: %v", err)
	}

	return parseDuckDuckGoHTML(string(body))
}

func parseDuckDuckGoHTML(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "failed to parse search results"
	}

	type result struct {
		Title, URL, Description string
	}
	var results []result

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "result") {
			r := result{}
			// Extract link and title
			if a := findNode(n, "a", "result__a"); a != nil {
				r.Title = textContent(a)
				for _, attr := range a.Attr {
					if attr.Key == "href" {
						r.URL = extractDDGURL(attr.Val)
					}
				}
			}
			// Extract snippet
			if sn := findNode(n, "a", "result__snippet"); sn != nil {
				r.Description = textContent(sn)
			}
			if r.Title != "" && r.URL != "" {
				results = append(results, r)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(results) == 0 {
		return "no results found"
	}
	if len(results) > 5 {
		results = results[:5]
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description))
	}
	return sb.String()
}

func formatSearchResults(results []struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}) string {
	if len(results) == 0 {
		return "no results found"
	}
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description))
	}
	return sb.String()
}

// --- DuckDuckGo HTML helpers ---

func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func findNode(n *html.Node, tag, class string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag && hasClass(n, class) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, tag, class); found != nil {
			return found
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func extractDDGURL(raw string) string {
	// DDG wraps URLs like //duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com&...
	if strings.Contains(raw, "uddg=") {
		if u, err := url.Parse(raw); err == nil {
			if decoded := u.Query().Get("uddg"); decoded != "" {
				return decoded
			}
		}
	}
	return raw
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
