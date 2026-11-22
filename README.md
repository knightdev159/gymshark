# Pack Calculator

A Go HTTP API (with a small web UI) that works out which whole packs to ship for an order.

**Live demo:** https://pack-calculator-5koj.onrender.com
(free hosting: the first request after a period of inactivity can take up to a minute while the instance wakes up)

## The rules

1. Only whole packs can be sent. Packs cannot be broken open.
2. Within rule 1, send out no more items than necessary.
3. Within rules 1 and 2, send out as few packs as possible.

With the default pack sizes `250, 500, 1000, 2000, 5000`:

| Items ordered | Packs shipped                    |
|--------------:|----------------------------------|
| 1             | 1 × 250                          |
| 250           | 1 × 250                          |
| 251           | 1 × 500                          |
| 501           | 1 × 500, 1 × 250                 |
| 12001         | 2 × 5000, 1 × 2000, 1 × 250      |

## Quick start

Requires Go 1.22+. There are no third-party dependencies.

```sh
go run ./cmd/server          # http://localhost:8080
go test ./...                # unit, API and randomised tests
```

(`go test -race ./...` also works where cgo is available, as it is in CI.)

Or with Docker:

```sh
docker build -t pack-calculator .
docker run --rm -p 8080:8080 pack-calculator
```

### Configuration

| Variable          | Default                    | Description                                                          |
|-------------------|----------------------------|----------------------------------------------------------------------|
| `PORT`            | `8080`                     | Port to listen on                                                    |
| `PACK_SIZES`      | `250,500,1000,2000,5000`   | Pack sizes used at start-up                                          |
| `PACK_SIZES_FILE` | _(unset)_                  | JSON file that keeps pack-size changes across restarts (optional)    |

Pack sizes can be changed, added and removed **without code changes**: through the UI, through `PUT /api/v1/packs`, or through `PACK_SIZES` at start-up.

## API

All responses are JSON. Errors look like `{"error": "message"}` with a matching 4xx/5xx status.

### `POST /api/v1/calculate`

```sh
curl -X POST localhost:8080/api/v1/calculate \
  -H "Content-Type: application/json" \
  -d '{"items": 12001}'
```

```json
{
  "itemsOrdered": 12001,
  "totalItems": 12250,
  "totalPacks": 4,
  "surplus": 249,
  "packSizes": [250, 500, 1000, 2000, 5000],
  "packs": [
    {"size": 5000, "quantity": 2},
    {"size": 2000, "quantity": 1},
    {"size": 250,  "quantity": 1}
  ]
}
```

An optional `packSizes` field runs a one-off calculation with other sizes, without changing the saved ones:

```sh
curl -X POST localhost:8080/api/v1/calculate \
  -H "Content-Type: application/json" \
  -d '{"items": 500000, "packSizes": [23, 31, 53]}'
# -> 9429 × 53, 7 × 31, 2 × 23  (exactly 500,000 items)
```

### `GET /api/v1/calculate?items=12001`

Same result, handy from a browser or `curl`.

### `GET /api/v1/packs` / `PUT /api/v1/packs`

```sh
curl localhost:8080/api/v1/packs
curl -X PUT localhost:8080/api/v1/packs \
  -H "Content-Type: application/json" \
  -d '{"packSizes": [250, 500, 1000, 2000, 5000]}'
```

Sizes are validated (whole numbers from 1 to 1,000,000, at most 50), de-duplicated and sorted.

### `GET /healthz`

Liveness check for the hosting platform: `{"status":"ok"}`.

## How it works

A greedy "largest pack first" approach is wrong for many pack sets. With sizes `23, 31, 53` and an order of `500,000`, greedy can't land on 500,000 exactly, but `9429 × 53 + 7 × 31 + 2 × 23` can. So the solver uses **dynamic programming** (unbounded coin change):

1. For every total `t` up to a limit, compute the fewest packs that make exactly `t`.
2. Pick the smallest reachable `t ≥ order` (rule 2). Its pack count is already the minimum (rule 3).
3. Walk back through the table to recover which packs were used.

The answer always lies in `[order, order + smallestPack − 1]` (rounding the order up to the smallest pack always works), so that is as far as the table has to go.

A plain DP is linear in the order size, so two reductions make it fast and memory-safe for any order up to 10¹²:

- **GCD scaling.** Every reachable total is a multiple of `g = gcd(packSizes)`, so sizes and the order are divided by `g` first. The default sizes become `1, 2, 4, 8, 20`.
- **Largest-pack shifting.** In any fewest-packs solution, a smaller pack `d` appears fewer than `L / gcd(d, L)` times, where `L` is the largest pack. Otherwise that many `d` packs could be swapped for `d / gcd(d, L)` packs of size `L`: the same items in fewer packs. So the items carried by non-`L` packs are bounded by a constant `B`. For large orders the solver removes whole multiples of `L`, solves the small remainder above `B`, and adds the `L` packs back. The answer is unchanged, and the work no longer depends on the order size.

As a result, an order of 1,000,000,000,000 items solves in microseconds. Benchmarks on an Intel i5-12400:

| Case                                         | Time per calculation | Memory  |
|----------------------------------------------|---------------------:|--------:|
| 123,456,789 items, default sizes             | ~0.9 µs              | 672 B   |
| 500,000 items, sizes 23 / 31 / 53            | ~11 µs               | 15 KB   | Pathological pack sets (e.g. two huge coprime sizes) that would still need an oversized table are rejected with `422` rather than exhausting memory.

Ties (equal items and equal packs) are broken in favour of larger packs, so results are deterministic.

## Testing

```sh
go test -race ./...
go test -run=^$ -bench=. ./internal/packing
```

- **Challenge examples** and extra hand-checked cases for the default sizes.
- **The 23/31/53 edge case** where greedy algorithms fail.
- **Randomised check against a brute-force reference:** 3,000 random pack sets and orders, compared with an unoptimised DP. This verifies the GCD and shifting reductions.
- **API tests** using `httptest`: happy paths, pack-size updates, and every validation and error path (bad JSON, unknown fields, fractional or negative values, oversized bodies, wrong methods).
- **Store tests:** persistence, corrupt files, and concurrent access under the race detector.

CI (GitHub Actions) runs `gofmt`, `go vet`, the tests with the race detector, and a Docker build on every push.

## Project layout

```
cmd/server/          entry point: config, HTTP server, graceful shutdown
internal/packing/    the algorithm (pure, no I/O) and its tests
internal/store/      thread-safe pack-size store with optional JSON persistence
internal/api/        HTTP handlers, validation, middleware
web/static/          single-page UI, embedded into the binary with go:embed
```

The result is a single static binary. The UI is embedded, so there is nothing else to deploy.

## Deployment

The app is a single Docker image listening on `$PORT`, so it runs on any container host.

**Render (free tier):** push the repository to GitHub, then in Render choose **New → Blueprint** and select the repository. `render.yaml` sets up the service and its health check.

**Fly.io:** `fly launch` (it detects the Dockerfile), then `fly deploy`.

**Google Cloud Run:** `gcloud run deploy pack-calculator --source . --allow-unauthenticated`.

## Design decisions and trade-offs

- **Standard library only:** Go 1.22's `net/http` router supports method and path patterns, so no framework is needed.
- **Pure algorithm package:** `internal/packing` has no I/O, which makes it easy to test and reuse.
- **Runtime-editable pack sizes** live in memory, and can optionally go to a file through `PACK_SIZES_FILE`. With several replicas, a shared store such as Redis or Postgres would replace this file.
- **No authentication on `PUT /api/v1/packs`.** This keeps the demo easy to test. In production, this endpoint would sit behind authentication. Reviewers can use the per-request `packSizes` override so they don't affect each other.
- **Safety limits:** request bodies are capped at 64 KB; orders are capped at 10¹² and pack sizes at 1,000,000 (at most 50 sizes); the DP table is bounded; the server sets read/write timeouts and shuts down gracefully.
