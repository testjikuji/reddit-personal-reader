// Package reddit is a minimal, read-only Reddit Data API client for a single
// personal user. It uses application-only OAuth (client credentials), reads
// only /r/{subreddit}/new, and keeps only a few fields of each post.
//
// It never posts, comments, votes, or sends messages.
package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tokenURL = "https://www.reddit.com/api/v1/access_token"
	apiBase  = "https://oauth.reddit.com"
)

// Post is the only data kept from a Reddit post.
type Post struct {
	ID        string    `json:"id"`
	Subreddit string    `json:"subreddit"`
	Title     string    `json:"title"`
	Permalink string    `json:"permalink"` // full https://www.reddit.com/... URL
	Author    string    `json:"author"`
	Score     int       `json:"score"`
	CreatedAt time.Time `json:"created_at"`
}

// Client reads new posts from subreddits. It is safe for concurrent use, but
// requests are serialized so that the per-client rate limit is respected.
type Client struct {
	ClientID     string
	ClientSecret string
	// UserAgent must identify the app and the operator, as Reddit requires,
	// e.g. "server:reddit-personal-reader:v0.1 (by /u/yourname)".
	UserAgent string
	HTTP      *http.Client

	// base URLs (overridden in tests)
	tokenURL string
	apiBase  string

	mu        sync.Mutex
	token     string
	tokenExp  time.Time
	waitUntil time.Time // set from X-Ratelimit-* headers when the budget runs out
}

// New returns a client for the given credentials.
func New(clientID, clientSecret, userAgent string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		UserAgent:    userAgent,
		HTTP:         &http.Client{Timeout: 20 * time.Second},
		tokenURL:     tokenURL,
		apiBase:      apiBase,
	}
}

var subredditRe = regexp.MustCompile(`^[A-Za-z0-9_]{2,21}$`)

// NormalizeSubreddit accepts "r/name", "/r/name" or "name" and returns "name".
func NormalizeSubreddit(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "r/")
	if !subredditRe.MatchString(s) {
		return "", fmt.Errorf("invalid subreddit name %q", s)
	}
	return s, nil
}

// ensureToken fetches an application-only token if the cached one is missing
// or about to expire. Caller must hold c.mu.
func (c *Client) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Until(c.tokenExp) > time.Minute {
		return nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.UserAgent)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("token request: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("token response: %w", err)
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("token response has no access_token (error=%q)", tok.Error)
	}
	c.token = tok.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return nil
}

// ErrRateLimited is returned when Reddit says the request budget is used up.
var ErrRateLimited = errors.New("rate limited by Reddit")

// New fetches the newest posts (at most limit, max 100) of one subreddit.
func (c *Client) New(ctx context.Context, subreddit string, limit int) ([]Post, error) {
	sub, err := NormalizeSubreddit(subreddit)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if wait := time.Until(c.waitUntil); wait > 0 {
		return nil, fmt.Errorf("%w: retry after %s", ErrRateLimited, wait.Round(time.Second))
	}
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/r/%s/new?limit=%d&raw_json=1", c.apiBase, sub, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.UserAgent)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	c.noteRateLimit(res.Header)

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		c.token = "" // fetch a new token next time
		return nil, fmt.Errorf("r/%s: HTTP 401 (token rejected)", sub)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("r/%s: %w", sub, ErrRateLimited)
	default:
		return nil, fmt.Errorf("r/%s: HTTP %d", sub, res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseListing(body)
}

// noteRateLimit stops further requests until the reset time when the
// remaining budget reported by Reddit drops below 1. Caller must hold c.mu.
func (c *Client) noteRateLimit(h http.Header) {
	remaining, err1 := strconv.ParseFloat(h.Get("X-Ratelimit-Remaining"), 64)
	reset, err2 := strconv.ParseFloat(h.Get("X-Ratelimit-Reset"), 64)
	if err1 == nil && err2 == nil && remaining < 1 {
		c.waitUntil = time.Now().Add(time.Duration(reset * float64(time.Second)))
	}
}

func parseListing(body []byte) ([]Post, error) {
	var l struct {
		Data struct {
			Children []struct {
				Data struct {
					ID         string  `json:"id"`
					Subreddit  string  `json:"subreddit"`
					Title      string  `json:"title"`
					Permalink  string  `json:"permalink"`
					Author     string  `json:"author"`
					Score      int     `json:"score"`
					CreatedUTC float64 `json:"created_utc"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, fmt.Errorf("listing: %w", err)
	}
	posts := make([]Post, 0, len(l.Data.Children))
	for _, ch := range l.Data.Children {
		d := ch.Data
		posts = append(posts, Post{
			ID:        d.ID,
			Subreddit: d.Subreddit,
			Title:     d.Title,
			Permalink: "https://www.reddit.com" + d.Permalink,
			Author:    d.Author,
			Score:     d.Score,
			CreatedAt: time.Unix(int64(d.CreatedUTC), 0).UTC(),
		})
	}
	return posts, nil
}
