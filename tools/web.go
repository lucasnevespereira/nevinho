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

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"
)

const httpTimeout = 15 * time.Second

const userAgent = "Nevinho/1.0 (+https://github.com/lucasnevespereira/nevinho)"

// browserUA is sent only to search engines. DuckDuckGo's HTML/lite endpoints
// ship a leaner page (or an empty one) to non-browser user agents, which
// starves the parser. A realistic Firefox string gets richer, more reliable
// results. For fetching arbitrary sites we still send `userAgent` so operators
// know who is hitting them.
const browserUA = "Mozilla/5.0 (X11; Linux x86_64; rv:123.0) Gecko/20100101 Firefox/123.0"

// regionToDDG maps a 2-letter country code to DuckDuckGo's `kl` locale hint.
// The full kl list is long; this covers the countries most likely to matter.
// Unknown codes pass through as `{code}-en` which DDG tolerates.
var regionToDDG = map[string]string{
	"ch": "ch-fr",
	"us": "us-en",
	"uk": "uk-en",
	"gb": "uk-en",
	"fr": "fr-fr",
	"de": "de-de",
	"es": "es-es",
	"it": "it-it",
	"nl": "nl-nl",
	"be": "be-fr",
	"pt": "pt-pt",
	"br": "br-pt",
	"at": "at-de",
	"se": "se-sv",
	"no": "no-no",
	"dk": "dk-da",
	"fi": "fi-fi",
	"pl": "pl-pl",
	"ie": "ie-en",
	"ca": "ca-en",
	"au": "au-en",
	"nz": "nz-en",
	"jp": "jp-jp",
	"mx": "mx-es",
	"in": "in-en",
	"sg": "sg-en",
	"ae": "xa-ar",
}

// regionToCountry maps a 2-letter code to Tavily's country parameter (full
// lowercase English name). Tavily accepts this only when topic is "general".
var regionToCountry = map[string]string{
	"ch": "switzerland",
	"us": "united states",
	"uk": "united kingdom",
	"gb": "united kingdom",
	"fr": "france",
	"de": "germany",
	"es": "spain",
	"it": "italy",
	"nl": "netherlands",
	"be": "belgium",
	"pt": "portugal",
	"br": "brazil",
	"at": "austria",
	"se": "sweden",
	"no": "norway",
	"dk": "denmark",
	"fi": "finland",
	"pl": "poland",
	"ie": "ireland",
	"ca": "canada",
	"au": "australia",
	"nz": "new zealand",
	"jp": "japan",
	"mx": "mexico",
	"in": "india",
	"sg": "singapore",
	"ae": "united arab emirates",
}

// timeRangeToDays converts the shared "d"/"w"/"m"/"y" shorthand to a day
// count Tavily understands. Returns 0 when the range is empty or unknown.
func timeRangeToDays(r string) int {
	switch strings.ToLower(r) {
	case "d":
		return 1
	case "w":
		return 7
	case "m":
		return 30
	case "y":
		return 365
	}
	return 0
}

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

	return truncateText(htmlToMarkdown(string(body)))
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
	Query     string `json:"query"`
	Region    string `json:"region,omitempty"`
	TimeRange string `json:"time_range,omitempty"`
}

func (r *Registry) webSearch(input json.RawMessage) string {
	var in webSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}
	in.Region = strings.ToLower(strings.TrimSpace(in.Region))
	in.TimeRange = strings.ToLower(strings.TrimSpace(in.TimeRange))

	if r.cfg.TavilyAPIKey != "" {
		if out := searchTavily(in, r.cfg.TavilyAPIKey); !isSearchFailure(out) {
			return out
		}
	}
	return searchDuckDuckGo(in)
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

func searchTavily(in webSearchInput, apiKey string) string {
	client := &http.Client{Timeout: httpTimeout}

	payload := map[string]any{
		"api_key":        apiKey,
		"query":          in.Query,
		"max_results":    5,
		"search_depth":   "advanced",
		"include_answer": true,
	}
	if country, ok := regionToCountry[in.Region]; ok {
		payload["country"] = country
	}
	if days := timeRangeToDays(in.TimeRange); days > 0 {
		// `days` only applies when topic is "news"; switching topic is fine
		// here because the caller explicitly asked for time-filtered results.
		payload["topic"] = "news"
		payload["days"] = days
	}
	reqBody, err := json.Marshal(payload)
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

func searchDuckDuckGo(in webSearchInput) string {
	client := &http.Client{Timeout: httpTimeout}

	out := fetchDuckDuckGo(client, "https://html.duckduckgo.com/html/", in, parseDuckDuckGoHTML)
	if !isSearchFailure(out) {
		return out
	}
	// DDG's HTML endpoint silently returns an empty results list under some
	// conditions (bot-like UA heuristics, odd queries, transient layout
	// changes). The lite endpoint uses a simpler renderer that is more
	// resilient and often succeeds when /html/ does not.
	return fetchDuckDuckGo(client, "https://lite.duckduckgo.com/lite/", in, parseDuckDuckGoLite)
}

func fetchDuckDuckGo(client *http.Client, endpoint string, in webSearchInput, parse func(string) string) string {
	params := url.Values{}
	params.Set("q", in.Query)
	if kl, ok := regionToDDG[in.Region]; ok {
		params.Set("kl", kl)
	}
	if in.TimeRange != "" {
		params.Set("df", in.TimeRange)
	}
	reqURL := endpoint + "?" + params.Encode()

	body, status, err := fetchWithRetry(client, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, err
		}
		// DDG serves thin or empty pages to non-browser UAs. A realistic
		// browser string consistently returns the full result list.
		req.Header.Set("User-Agent", browserUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		return req, nil
	}, 500*1024)
	if err != nil {
		return fmt.Sprintf("search failed: %s", describeFetchErr(err))
	}
	if status != 200 {
		return fmt.Sprintf("search failed: %s", describeHTTPStatus(status))
	}

	return parse(string(body))
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

// parseDuckDuckGoLite parses DDG's lite HTML. The layout is a flat table of
// alternating rows: a link row (result-link anchor) followed by a snippet row
// (td with result-snippet). We collect them in document order then pair each
// link with the next snippet that follows it.
func parseDuckDuckGoLite(htmlContent string) string {
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
		if n.Type == html.ElementNode && n.Data == "a" && hasClass(n, "result-link") {
			r := result{Title: textContent(n)}
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					r.URL = extractDDGURL(attr.Val)
				}
			}
			if r.Title != "" && r.URL != "" {
				results = append(results, r)
			}
		} else if n.Type == html.ElementNode && n.Data == "td" && hasClass(n, "result-snippet") {
			if len(results) > 0 && results[len(results)-1].Description == "" {
				results[len(results)-1].Description = textContent(n)
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

// boilerplateTags are stripped before markdown conversion when the document
// has no <main> or <article> root. Leaving them in lets navigation, ads, and
// cookie banners leak into the output and waste tokens.
var boilerplateTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "svg": true,
	"iframe": true, "nav": true, "footer": true, "header": true,
	"aside": true, "form": true, "button": true, "select": true,
	"textarea": true, "input": true,
}

// htmlToMarkdown parses raw HTML, narrows to <main>/<article> when present,
// strips boilerplate otherwise, and converts the result to markdown. Markdown
// preserves links, lists, headings, and tables — structure the plain-text
// extractor threw away. On any parse or conversion failure, returns the raw
// input so the caller still has something to show.
func htmlToMarkdown(htmlContent string) string {
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
		stripBoilerplate(doc)
		root = doc
	}

	md, err := htmltomarkdown.ConvertNode(root)
	if err != nil {
		return htmlContent
	}
	return strings.TrimSpace(string(md))
}

// stripBoilerplate removes nav/header/footer/script/style and similar nodes
// in place. Walking with a pre-collected removal list avoids mutating the
// tree during traversal.
func stripBoilerplate(n *html.Node) {
	var remove []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && boilerplateTags[n.Data] {
			remove = append(remove, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	for _, node := range remove {
		if node.Parent != nil {
			node.Parent.RemoveChild(node)
		}
	}
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
