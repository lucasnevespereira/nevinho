package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const httpTimeout = 15 * time.Second

const userAgent = "Nevinho/1.0 (+https://github.com/lucasnevespereira/nevinho)"

// fetchWithRetry performs an HTTP request with up to 3 attempts, retrying on
// 429/5xx and transient transport errors. Respects Retry-After on 429.
// Returns the response body (capped at maxBody), status code, and any error.
func fetchWithRetry(client *http.Client, makeReq func() (*http.Request, error), maxBody int64) ([]byte, int, error) {
	const attempts = 3
	var lastErr error
	for attempt := range attempts {
		req, err := makeReq()
		if err != nil {
			return nil, 0, err
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < attempts-1 {
				time.Sleep(backoffWeb(attempt))
				continue
			}
			return nil, 0, err
		}

		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if rerr != nil {
			return nil, resp.StatusCode, rerr
		}

		if !isRetryableStatus(resp.StatusCode) {
			return body, resp.StatusCode, nil
		}

		if attempt == attempts-1 {
			return body, resp.StatusCode, nil
		}

		delay := backoffWeb(attempt)
		if resp.StatusCode == 429 {
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, perr := strconv.Atoi(ra); perr == nil && secs > 0 && secs <= 60 {
					delay = time.Duration(secs) * time.Second
				}
			}
		}
		time.Sleep(delay)
	}
	return nil, 0, lastErr
}

func isRetryableStatus(code int) bool {
	return code == 429 || code >= 500
}

func backoffWeb(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

// describeFetchErr converts a transport error into a short actionable label.
// The model uses this to decide whether to retry, reformulate, or abandon.
func describeFetchErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Client.Timeout"):
		return "timed out"
	case strings.Contains(s, "no such host"):
		return "DNS lookup failed (host does not exist)"
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "connection reset"):
		return "connection reset"
	case strings.Contains(s, "TLS"), strings.Contains(s, "certificate"):
		return "TLS/certificate error"
	}
	return s
}

// describeHTTPStatus returns a short hint about what an HTTP status means for the agent.
func describeHTTPStatus(code int) string {
	switch code {
	case 401:
		return "HTTP 401 unauthorized (auth required)"
	case 403:
		return "HTTP 403 forbidden (site blocked the request, try another source)"
	case 404:
		return "HTTP 404 not found (URL is wrong or page was removed)"
	case 408:
		return "HTTP 408 request timeout"
	case 429:
		return "HTTP 429 rate limited (back off before retrying)"
	case 500, 502, 503, 504:
		return fmt.Sprintf("HTTP %d server error (transient, safe to retry)", code)
	}
	return fmt.Sprintf("HTTP %d", code)
}

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

	if r.cfg.TavilyAPIKey != "" {
		if out := extractTavily(in.URL, r.cfg.TavilyAPIKey); !isFetchFailure(out) {
			return truncateText(out)
		}
	}

	if out := fetchJinaReader(in.URL); !isFetchFailure(out) {
		return truncateText(out)
	}

	return fetchDirect(in.URL)
}

func fetchJinaReader(target string) string {
	client := &http.Client{Timeout: httpTimeout}

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", "https://r.jina.ai/"+target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/plain")
		return req, nil
	}, 1024*1024)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %s", describeFetchErr(err))
	}
	if status != 200 {
		return describeHTTPStatus(status)
	}

	out := strings.TrimSpace(string(body))
	if out == "" {
		return "no content extracted"
	}
	return out
}

func fetchDirect(target string) string {
	client := &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := validateURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		return req, nil
	}, 500*1024)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %s", describeFetchErr(err))
	}
	if status != 200 {
		return describeHTTPStatus(status)
	}

	return truncateText(extractText(string(body)))
}

func extractTavily(target, apiKey string) string {
	client := &http.Client{Timeout: httpTimeout}

	reqBody, err := json.Marshal(map[string]any{
		"api_key":        apiKey,
		"urls":           []string{target},
		"extract_depth":  "basic",
		"include_images": false,
	})
	if err != nil {
		return fmt.Sprintf("failed to build request: %v", err)
	}

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("POST", "https://api.tavily.com/extract", strings.NewReader(string(reqBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, 1024*1024)
	if err != nil {
		return fmt.Sprintf("failed to fetch: %s", describeFetchErr(err))
	}
	if status != 200 {
		return describeHTTPStatus(status)
	}

	var result struct {
		Results []struct {
			URL        string `json:"url"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Sprintf("failed to parse response: %v", err)
	}

	if len(result.Results) == 0 {
		if len(result.FailedResults) > 0 {
			return fmt.Sprintf("failed to fetch: %s", result.FailedResults[0].Error)
		}
		return "no content extracted"
	}

	return result.Results[0].RawContent
}

// isFetchFailure returns true when a fetch provider returned an error,
// so the caller can fall through to the next provider.
func isFetchFailure(out string) bool {
	if out == "" {
		return true
	}
	lower := strings.ToLower(out)
	return strings.HasPrefix(lower, "failed to") ||
		strings.HasPrefix(lower, "http ") ||
		strings.HasPrefix(lower, "no content") ||
		strings.HasPrefix(lower, "blocked:")
}

func truncateText(s string) string {
	if len(s) > maxResponseLen {
		return s[:maxResponseLen] + "\n...(truncated)"
	}
	return s
}

type webSearchInput struct {
	Query string `json:"query"`
}

func (r *Registry) webSearch(input json.RawMessage) string {
	var in webSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if r.cfg.TavilyAPIKey != "" {
		if out := searchTavily(in.Query, r.cfg.TavilyAPIKey); !isSearchFailure(out) {
			return out
		}
	}
	return searchDuckDuckGo(in.Query)
}

// isSearchFailure returns true when a provider returned an error or empty result,
// so the caller can fall through to the next provider.
func isSearchFailure(out string) bool {
	if out == "" {
		return true
	}
	lower := strings.ToLower(out)
	return strings.HasPrefix(lower, "search failed") ||
		strings.HasPrefix(lower, "no results") ||
		strings.HasPrefix(lower, "failed to")
}

func searchTavily(query, apiKey string) string {
	client := &http.Client{Timeout: httpTimeout}

	reqBody, err := json.Marshal(map[string]any{
		"api_key":        apiKey,
		"query":          query,
		"max_results":    5,
		"search_depth":   "advanced",
		"include_answer": true,
	})
	if err != nil {
		return fmt.Sprintf("failed to build request: %v", err)
	}

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("POST", "https://api.tavily.com/search", strings.NewReader(string(reqBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, 1024*1024)
	if err != nil {
		return fmt.Sprintf("search failed: %s", describeFetchErr(err))
	}
	if status != 200 {
		return fmt.Sprintf("search failed: %s", describeHTTPStatus(status))
	}

	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Sprintf("failed to parse results: %v", err)
	}

	if len(result.Results) == 0 && result.Answer == "" {
		return "no results found"
	}

	var sb strings.Builder
	if result.Answer != "" {
		sb.WriteString("**Answer:** ")
		sb.WriteString(result.Answer)
		sb.WriteString("\n\n")
	}
	sb.WriteString("**Sources:**\n")
	for i, r := range result.Results {
		fmt.Fprintf(&sb, "%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return sb.String()
}

func searchDuckDuckGo(query string) string {
	client := &http.Client{Timeout: httpTimeout}
	reqURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		return req, nil
	}, 500*1024)
	if err != nil {
		return fmt.Sprintf("search failed: %s", describeFetchErr(err))
	}
	if status != 200 {
		return fmt.Sprintf("search failed: %s", describeHTTPStatus(status))
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
		fmt.Fprintf(&sb, "%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description)
	}
	return sb.String()
}

func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			return slices.Contains(strings.Fields(attr.Val), class)
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
	if strings.Contains(raw, "uddg=") {
		if u, err := url.Parse(raw); err == nil {
			if decoded := u.Query().Get("uddg"); decoded != "" {
				return decoded
			}
		}
	}
	return raw
}

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

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true,
	"iframe": true, "nav": true, "footer": true, "header": true,
	"aside": true, "form": true, "button": true, "select": true,
	"textarea": true, "input": true,
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "tr": true, "dt": true, "dd": true,
	"blockquote": true, "pre": true, "article": true, "section": true,
}

func extractText(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}

	var root *html.Node
	if main := findTag(doc, "main"); main != nil {
		root = main
	} else if article := findTag(doc, "article"); article != nil {
		root = article
	} else {
		root = doc
	}

	var sb strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode && skipTags[n.Data] {
			return
		}
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key == "hidden" || (a.Key == "aria-hidden" && a.Val == "true") {
					return
				}
			}
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
		if n.Type == html.ElementNode && blockTags[n.Data] {
			sb.WriteString("\n")
		}
	}

	extract(root)

	text := sb.String()
	lines := strings.Split(text, "\n")
	var out []string
	blanks := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			blanks++
			if blanks <= 1 {
				out = append(out, "")
			}
		} else {
			blanks = 0
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func findTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findTag(c, tag); found != nil {
			return found
		}
	}
	return nil
}
