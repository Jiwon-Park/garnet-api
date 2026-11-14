# garnet-api

Lightweight GoFiber API server: a key-value API over Garnet, plus `POST /translate`, a Japanese→Korean translation endpoint backed by the hdTrans engine (`libhdTransEngine`) loaded once per process via cgo.

## API

```http
PUT /keys/{key}
DELETE /keys/{key}
POST /keys
POST /translate
POST /v1/chat/completions
```

### OpenAI-compatible batch translation

**`POST /v1/chat/completions`** takes an OpenAI chat request. Only the last `user` message is read, and it holds a JSON object `{"0": "...", "1": "..."}` (an array of strings also works). Other fields are ignored. The reply is a normal chat completion whose `choices[0].message.content` is `{"translations": [...]}` in index order.

If `TRANSLATE_API_KEY` is set, the request must send it in `Authorization`, either bare or as `Bearer <key>`.

### Translate

**`POST /translate`** — translate Japanese text. Body:

```json
{
  "text": "今日は本を読みます。\n肉がジュージューと焼ける",
  "context": "optional preceding lines of the same document"
}
```

Response: `{"text": "오늘은 책을 읽습니다.\n고기가 지글지글 구워진다"}`

- `text` is required, at most 64 KiB, and may hold several `\n`-separated lines; they are translated as one document and the line count is kept.
- `context` is optional (at most 64 KiB). The lines before the text, so names, speech and quotes carry over. It is not included in the response.
- The engine is loaded once at startup. Requests run concurrently, limited to `TRANSLATE_WORKERS` engine calls at a time.
- Returns `503` when the server was built without cgo or the engine failed to load; key-value routes keep working.

### Operations

**Reads** go through `POST /keys` with `{"type": "get", "keys": ["k1", "k2"]}`. Each item returns `key`, `value` and `found`. When `LRU_IDLE_TTL_SECONDS` is non-zero, the idle TTL of each read key is refreshed (GETEX); when `0`, no TTL is touched. The API has no GET routes: intermediaries in front of the service drop GET requests.

**`PUT /keys/{key}`** — write. Replaces the value. Body:

```json
{
  "value": "hello",
  "ttl_seconds": 3600
}
```

`ttl_seconds` is optional. If omitted, the configured `LRU_IDLE_TTL_SECONDS` is used; if both are `0`, no TTL is applied (key lives until explicitly deleted or until Garnet's persistent storage is wiped).

> The API supports `DELETE /keys/{key}` for direct key removal. You can also remove a key by writing it with a 1-second `ttl_seconds` on SET.

### Key validation

Keys may only contain `A-Za-z0-9._:/@-`, be at most 512 bytes. Set values must be non-empty and at most 1 MiB. These limits are enforced by the API regardless of how the client reaches Garnet, but a Garnet admin connecting directly bypasses them.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listen port |
| `GARNET_ADDR` | `127.0.0.1:6379` | Garnet RESP endpoint |
| `GARNET_PASSWORD` | empty | Garnet password |
| `GARNET_DB` | `0` | Garnet database index |
| `REQUEST_TIMEOUT_MS` | `500` | Per-request Garnet timeout |
| `LRU_IDLE_TTL_SECONDS` | `0` | Access-refreshed idle TTL. `0` means no TTL — keys live until explicitly deleted |
| `REDIS_POOL_SIZE` | `GOMAXPROCS * 32` | Garnet connection pool size |
| `HTTP_CONCURRENCY` | `262144` | Fiber concurrent connection limit |
| `TRANSLATE_WORKERS` | `GOMAXPROCS` | Max concurrent hdTrans engine calls |

## Local Run

`/translate` needs cgo and `libhdTransEngine`. Build hdTrans first (`cmake --preset linux && cmake --build --preset linux`, see hdTrans `docs/build.md`), then point cgo at its output directory. That directory must also contain `hdtrans-data/dicts.hdsnap` (`hdtrans --dicts dicts --compile-snapshot "$PWD/hdtrans-data/dicts.hdsnap"`).

```bash
export CGO_LDFLAGS="-L/path/to/hdTrans/build/linux"
export LD_LIBRARY_PATH=/path/to/hdTrans/build/linux
go mod tidy
go test ./...
go run .
```

At runtime the binary looks for `libhdTransEngine.so` next to itself (`-rpath $ORIGIN`), so a deployed binary needs `libhdTransEngine.so` and `hdtrans-data/` beside it. Without cgo (`CGO_ENABLED=0`) the server still builds and runs, but `/translate` returns 503.

## GitHub Actions Release

Release runs on GitHub-hosted Ubuntu runners (amd64 and arm64). Each release package `garnet-api-linux-<arch>.tar.gz` contains the binary, `libhdTransEngine.so` and `hdtrans-data/` (base dictionary, plus `userdict/userdict.txt` as the user dictionary when that file is in the repository); the deploy step copies all three into `DEPLOY_PATH`. The hdTrans version comes from the `HDTRANS_REF` repository variable (default `v0.9.5`). Build and release artifacts are created on GitHub; only copying the binary and restarting systemd happen on your server.

Required GitHub repository or environment variables:

```text
DEPLOY_HOST
DEPLOY_USER
DEPLOY_PATH
DEPLOY_SERVICE
```

Required secret:

```text
DEPLOY_SSH_KEY
```

Create a release by pushing a tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## Garnet Lifecycle Workflow

`.github/workflows/garnet-lifecycle.yml` manages Garnet itself from a GitHub-hosted `ubuntu-latest` runner through SSH. Garnet runs as a native binary (downloaded from the official GitHub release, SHA-256 verified), managed by systemd — no Docker required on the target.

Supported manual commands:

```text
install
start
restart
stop
status
```

The workflow installs Garnet as a systemd service on the target server. All target, Garnet, and credential settings are read from GitHub Variables and Secrets. See `docs/github-actions-config.md`.

## systemd Bootstrap

The systemd unit is generated by `.github/workflows/release.yml` from `$DEPLOY_SERVICE` (unit name) and `$DEPLOY_PATH` (binary install dir), then installed to `/etc/systemd/system/${DEPLOY_SERVICE}.service` on the target. The workflow also creates the `garnet-api` system user on first deploy.

Before the first release, copy `deploy/garnet-api.env.example` to `/etc/garnet-api/garnet-api.env` on the target and adjust values. The unit loads that file via `EnvironmentFile=-/etc/garnet-api/garnet-api.env` (the leading `-` makes it optional — missing file is not fatal).

The Garnet config example in `deploy/garnet.conf.example` sets memory-related options. Garnet's documented Redis config compatibility maps `maxmemory` to `LogMemorySize`; it does not expose Redis `maxmemory-policy allkeys-lru`, so this API uses access-refreshed TTL for LRU-style key removal.
