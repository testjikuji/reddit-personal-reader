// Command reddit-reader polls the newest posts of a few subreddits and prints
// them as JSON lines. It is the reading part of a private, single-user feed
// reader; in production the same package is called from that server instead.
//
// Environment:
//
//	REDDIT_CLIENT_ID, REDDIT_CLIENT_SECRET  application credentials
//	REDDIT_USERNAME                         operator's Reddit username (for the User-Agent)
//
// Usage:
//
//	reddit-reader -subs VirtualYoutubers,vtubers,anime,feminineboys -interval 60m
//	reddit-reader -subs vtubers -once
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/testjikuji/reddit-personal-reader/reddit"
)

const version = "v0.1"

func main() {
	subs := flag.String("subs", "", "comma-separated subreddit names")
	interval := flag.Duration("interval", 60*time.Minute, "polling interval (minimum 30m)")
	limit := flag.Int("limit", 25, "posts per subreddit per poll (max 100)")
	once := flag.Bool("once", false, "poll once and exit")
	flag.Parse()

	id, secret, user := os.Getenv("REDDIT_CLIENT_ID"), os.Getenv("REDDIT_CLIENT_SECRET"), os.Getenv("REDDIT_USERNAME")
	if id == "" || secret == "" || user == "" {
		log.Fatal("REDDIT_CLIENT_ID, REDDIT_CLIENT_SECRET and REDDIT_USERNAME must be set")
	}
	var names []string
	for _, s := range strings.Split(*subs, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		n, err := reddit.NormalizeSubreddit(s)
		if err != nil {
			log.Fatal(err)
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		log.Fatal("-subs is required")
	}
	if *interval < 30*time.Minute {
		*interval = 30 * time.Minute
	}

	c := reddit.New(id, secret, fmt.Sprintf("server:reddit-personal-reader:%s (by /u/%s)", version, user))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	enc := json.NewEncoder(os.Stdout)
	seen := map[string]bool{}
	for {
		for _, sub := range names {
			posts, err := c.New(ctx, sub, *limit)
			if err != nil {
				log.Printf("r/%s: %v", sub, err)
				continue
			}
			for _, p := range posts {
				if !seen[p.ID] {
					seen[p.ID] = true
					_ = enc.Encode(p)
				}
			}
			// Space out requests even within one poll.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		if *once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}
