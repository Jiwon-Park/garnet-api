# garnet-api

Lightweight GoFiber API server for controlling Garnet with get, set, remove, and translation-aware (atomic source → translation + last-used) operations.

## API

```http
GET /healthz
GET /keys/{key}
PUT /keys/{key}
DELETE /keys/{key}
POST /translate
```

### Key-value operations

Set request body:

```json
{
  "value": "hello",
  "ttl_seconds": 3600
}
```

`ttl_seconds` is optional. If `LRU_IDLE_TTL_SECONDS` is set and the body omits `ttl_seconds`, set uses that idle TTL. Get also refreshes the same TTL with Garnet `GETEX`, so recently used keys stay alive and idle keys are removed by Garnet expiration.

TTL refresh on read is on by default: even when `LRU_IDLE_TTL_SECONDS=0`, `GET` refreshes the key's TTL using a default idle TTL (1 hour). Set `LRU_IDLE_TTL_SECONDS` to a non-zero value to override.

### Translation operations

```http
POST /translate
```

Write (store translation + last-used timestamp atomically):

```json
{
  "source": "hello",
  "translation": "hola",
  "ttl_seconds": 3600
}
```

Read (fetch translation + bump last-used; omit `translation`):

```json
{
  "source": "hello"
}
```

Response:

```json
{
  "source": "hello",
  "translation": "hola",
  "found": true
}
```

The endpoint stores `source → translation` and a `last_used` timestamp as two distinct keys (`trans:<source>` and `used:<source>`) in one atomic pipeline, so each TTL is refreshed independently — bumping last-used does not rewrite the translation payload.

### Key validation

Keys may only contain `A-Za-z0-9._:/@-`, be at most 512 bytes, and set values must be non-empty and at most 1 MiB.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listen port |
| `GARNET_ADDR` | `127.0.0.1:6379` | Garnet RESP endpoint |
| `GARNET_PASSWORD` | empty | Garnet password |
| `GARNET_DB` | `0` | Garnet database index |
| `REQUEST_TIMEOUT_MS` | `500` | Per-request Garnet timeout |
| `LRU_IDLE_TTL_SECONDS` | `0` | Access-refreshed idle TTL. `0` uses a 1h default; TTL refresh on read is always on |
| `REDIS_POOL_SIZE` | `GOMAXPROCS * 32` | Garnet connection pool size |
| `HTTP_CONCURRENCY` | `262144` | Fiber concurrent connection limit |

## Local Run

```bash
go mod tidy
go test ./...
go run .
```

## GitHub Actions Release

Release runs on GitHub-hosted Ubuntu runners. Build and release artifacts are created on GitHub; only copying the binary and restarting systemd happen on your server.

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

Use `deploy/garnet-api.service` as the initial service unit. Copy `deploy/garnet-api.env.example` to `/etc/garnet-api/garnet-api.env` and adjust values on the server.

The Garnet config example in `deploy/garnet.conf.example` sets memory-related options. Garnet's documented Redis config compatibility maps `maxmemory` to `LogMemorySize`; it does not expose Redis `maxmemory-policy allkeys-lru`, so this API uses access-refreshed TTL for LRU-style key removal.
