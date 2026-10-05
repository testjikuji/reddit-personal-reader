package reddit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const listing = `{"data":{"children":[
 {"data":{"id":"abc","subreddit":"HarryPotter","title":"Genderbend Draco fic","permalink":"/r/HarryPotter/comments/abc/x/","author":"u1","score":12,"created_utc":1790000000}},
 {"data":{"id":"def","subreddit":"HarryPotter","title":"Another post","permalink":"/r/HarryPotter/comments/def/y/","author":"u2","score":3,"created_utc":1790000100}}
]}}`

func testServer(t *testing.T, tokenCalls *int32, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(tokenCalls, 1)
		id, secret, ok := r.BasicAuth()
		if !ok || id != "id" || secret != "secret" {
			t.Errorf("basic auth = %q %q %v", id, secret, ok)
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		fmt.Fprint(w, `{"access_token":"tok","expires_in":86400}`)
	})
	mux.HandleFunc("/r/", handler)
	srv := httptest.NewServer(mux)
	c := New("id", "secret", "test:reddit-personal-reader:v0 (by /u/test)")
	c.tokenURL = srv.URL + "/token"
	c.apiBase = srv.URL
	return c, srv.Close
}

func TestNewParsesListingAndReusesToken(t *testing.T) {
	var tokenCalls int32
	c, done := testServer(t, &tokenCalls, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "test:") {
			t.Errorf("user agent = %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Path != "/r/HarryPotter/new" || r.URL.Query().Get("limit") != "25" {
			t.Errorf("url = %s", r.URL)
		}
		fmt.Fprint(w, listing)
	})
	defer done()

	for i := 0; i < 2; i++ {
		posts, err := c.New(context.Background(), "r/HarryPotter", 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(posts) != 2 || posts[0].Title != "Genderbend Draco fic" ||
			posts[0].Permalink != "https://www.reddit.com/r/HarryPotter/comments/abc/x/" ||
			posts[0].CreatedAt.Unix() != 1790000000 {
			t.Fatalf("posts = %+v", posts)
		}
	}
	if tokenCalls != 1 {
		t.Fatalf("token fetched %d times, want 1", tokenCalls)
	}
}

func TestRateLimitStopsFurtherRequests(t *testing.T) {
	var tokenCalls, apiCalls int32
	c, done := testServer(t, &tokenCalls, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiCalls, 1)
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", "300")
		fmt.Fprint(w, listing)
	})
	defer done()

	if _, err := c.New(context.Background(), "HarryPotter", 25); err != nil {
		t.Fatal(err)
	}
	_, err := c.New(context.Background(), "HarryPotter", 25)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if apiCalls != 1 {
		t.Fatalf("api called %d times, want 1", apiCalls)
	}
}

func TestNormalizeSubreddit(t *testing.T) {
	for in, want := range map[string]string{"r/femboy": "femboy", "/r/HarryPotter": "HarryPotter", "Drarry": "Drarry"} {
		if got, err := NormalizeSubreddit(in); err != nil || got != want {
			t.Errorf("NormalizeSubreddit(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "r/", "a b", "../etc", strings.Repeat("x", 30)} {
		if _, err := NormalizeSubreddit(bad); err == nil {
			t.Errorf("NormalizeSubreddit(%q) should fail", bad)
		}
	}
}
