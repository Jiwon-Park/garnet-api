# GitHub Actions Configuration

This project uses GitHub-hosted runners and SSH to manage Garnet on the target server. Garnet runs as a native binary under systemd (no Docker required).

## Variables

Set these as repository or environment variables.

```bash
gh variable set DEPLOY_HOST --repo Jiwon-Park/garnet-api --body "your.server.example"
gh variable set DEPLOY_PORT --repo Jiwon-Park/garnet-api --body "22"
gh variable set DEPLOY_USER --repo Jiwon-Park/garnet-api --body "deploy"
gh variable set GARNET_SERVICE_NAME --repo Jiwon-Park/garnet-api --body "garnet"
gh variable set GARNET_VERSION --repo Jiwon-Park/garnet-api --body "2.1.4"
gh variable set GARNET_SHA256_X64 --repo Jiwon-Park/garnet-api --body "895fc61cb09c2403147c1186f8f370e9b45958dc5c50fa57de131e660bb6a27a"
gh variable set GARNET_SHA256_ARM64 --repo Jiwon-Park/garnet-api --body "fe418207587af45ec962908744487eabb28e0ef64823ea9fe46525dd14720778"
gh variable set GARNET_PORT --repo Jiwon-Park/garnet-api --body "6379"
gh variable set GARNET_BIND_ADDRESS --repo Jiwon-Park/garnet-api --body "127.0.0.1"
gh variable set GARNET_LOG_MEMORY_SIZE --repo Jiwon-Park/garnet-api --body "1g"
gh variable set GARNET_INDEX_MEMORY_SIZE --repo Jiwon-Park/garnet-api --body "256m"
gh variable set GARNET_COMPACTION_FREQUENCY_SECS --repo Jiwon-Park/garnet-api --body "60"
gh variable set GARNET_COMPACTION_TYPE --repo Jiwon-Park/garnet-api --body "Lookup"
gh variable set GARNET_DISABLE_OBJECTS --repo Jiwon-Park/garnet-api --body "true"
gh variable set GARNET_DISABLE_PUBSUB --repo Jiwon-Park/garnet-api --body "true"
gh variable set GARNET_AUTH_ENABLED --repo Jiwon-Park/garnet-api --body "false"
gh variable set GARNET_ENABLE_STORAGE --repo Jiwon-Park/garnet-api --body "true"
gh variable set GARNET_STORAGE_DIR --repo Jiwon-Park/garnet-api --body "/var/lib/garnet"
gh variable set GARNET_EXTRA_ARGS --repo Jiwon-Park/garnet-api --body ""
```

`GARNET_ENABLE_STORAGE` defaults to `true` and adds `--aof --logdir` to the Garnet command so data survives restarts. The lifecycle script auto-creates `GARNET_STORAGE_DIR` (default `/var/lib/garnet`) and the systemd unit grants `ReadWritePaths` for it. Set to `false` for in-memory only.

`GARNET_VERSION` is the Garnet release tag without the leading `v` (e.g. `2.1.4`). The workflow downloads `linux-<arch>-based.tar.xz` from `https://github.com/microsoft/garnet/releases/download/v<GARNET_VERSION>/` and verifies its SHA-256 against `GARNET_SHA256_X64` / `GARNET_SHA256_ARM64` depending on the target server's architecture. Look up the digests for a given release at the Garnet releases page.

`GARNET_EXTRA_ARGS` is passed through to the Garnet binary as extra command-line arguments. GitHub does not allow empty variable values, so set it to `TODO_NONE` when no extra args are needed (the lifecycle script treats `TODO_NONE` as "no extra args").

## Secrets

```bash
gh secret set DEPLOY_SSH_KEY --repo Jiwon-Park/garnet-api < ./deploy_key
gh secret set DEPLOY_SSH_KNOWN_HOSTS --repo Jiwon-Park/garnet-api --body "$(ssh-keyscan -p 22 your.server.example)"
gh secret set GARNET_PASSWORD --repo Jiwon-Park/garnet-api --body "replace_when_auth_enabled"
```

`DEPLOY_SSH_KNOWN_HOSTS` is recommended. If it is empty, the workflow falls back to `ssh-keyscan` during the run.

## Target Server Requirements

The target server must have:

- SSH access for `DEPLOY_USER`
- passwordless `sudo` for service installation and systemd commands
- `curl`, `tar`, `xz`, and `sha256sum` available
- systemd

The workflow installs Garnet as a systemd service running the native `garnet-server` binary at `/opt/garnet/garnet-server`.
