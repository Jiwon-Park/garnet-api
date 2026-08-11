# garnet-api

Lightweight GoFiber API server for controlling Garnet with only get, set, and remove operations.

## API

```http
GET /keys/{key}
PUT /keys/{key}
DELETE /keys/{key}
```

Set request body:

```json
{
  "value": "hello",
  "ttl_seconds": 3600
}
```

`ttl_seconds` is optional. If `LRU_IDLE_TTL_SECONDS` is set and the body omits `ttl_seconds`, set uses that idle TTL. Get also refreshes the same TTL with Garnet `GETEX`, so recently used keys stay alive and idle keys are removed by Garnet expiration.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listen port |
| `GARNET_ADDR` | `127.0.0.1:6379` | Garnet RESP endpoint |
| `GARNET_PASSWORD` | empty | Garnet password |
| `GARNET_DB` | `0` | Garnet database index |
| `REQUEST_TIMEOUT_MS` | `500` | Per-request Garnet timeout |
| `LRU_IDLE_TTL_SECONDS` | `0` | Access-refreshed idle TTL. `0` disables LRU-style expiration |
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

## systemd Bootstrap

Use `deploy/garnet-api.service` as the initial service unit. Copy `deploy/garnet-api.env.example` to `/etc/garnet-api/garnet-api.env` and adjust values on the server.

The Garnet config example in `deploy/garnet.conf.example` sets memory-related options. Garnet's documented Redis config compatibility maps `maxmemory` to `LogMemorySize`; it does not expose Redis `maxmemory-policy allkeys-lru`, so this API uses access-refreshed TTL for LRU-style key removal.
