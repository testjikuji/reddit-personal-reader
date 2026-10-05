# reddit-personal-reader

A minimal, **read-only** Reddit Data API client used by a private, single-user feed reader.

## What it does

- Authenticates with **application-only OAuth** (client credentials). No user login, no write scopes.
- Calls only `GET /r/{subreddit}/new` (default `limit=25`) for a short list of subreddits.
- Polls **once every 60 minutes** (the command refuses intervals shorter than 30 minutes),
  waits 2 seconds between subreddits, and stops when `X-Ratelimit-Remaining` reaches 0
  until `X-Ratelimit-Reset`.
- Keeps only: post id, subreddit, title, permalink, author name, score, created time.
- Sends a descriptive User-Agent: `server:reddit-personal-reader:v0.1 (by /u/<operator>)`.

Subreddits: r/VirtualYoutubers, r/vtubers, r/anime, r/feminineboys.
Expected volume: 4 subreddits × 24 polls/day ≈ 96 requests/day.

## What it does not do

- It never posts, comments, votes, sends messages, or moderates.
- Results are visible only to the operator; nothing is published or redistributed.
- Data is not sold and not used to train AI/ML models.

## How it is used

The operator runs a self-hosted personal dashboard that combines Japanese news RSS and
blog aggregators. This package is the Reddit part: matching post titles are shown there
with a link back to the original thread on Reddit.

## Run

```sh
export REDDIT_CLIENT_ID=... REDDIT_CLIENT_SECRET=... REDDIT_USERNAME=...
go run ./cmd/reddit-reader -subs VirtualYoutubers,vtubers,anime,feminineboys -once
```

Output is one JSON object per new post.

## Test

```sh
go test ./...
```

## License

MIT
