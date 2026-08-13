#!/usr/bin/env bash
set -euo pipefail

CONFIG_FILE="/etc/garnet/garnet.env"
INSTALL_DIR="/opt/garnet"
BINARY="garnet-server"
RUNNER_FILE="/usr/local/bin/garnet-server-run"
SERVICE_FILE=""

load_config() {
  if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Missing config file: $CONFIG_FILE" >&2
    exit 1
  fi

  set -a
  # shellcheck disable=SC1090
  . "$CONFIG_FILE"
  set +a

  required=(
    GARNET_SERVICE_NAME
    GARNET_VERSION
    GARNET_SHA256_X64
    GARNET_SHA256_ARM64
    GARNET_PORT
    GARNET_BIND_ADDRESS
    GARNET_LOG_MEMORY_SIZE
    GARNET_INDEX_MEMORY_SIZE
    GARNET_COMPACTION_FREQUENCY_SECS
    GARNET_COMPACTION_TYPE
    GARNET_DISABLE_OBJECTS
    GARNET_DISABLE_PUBSUB
    GARNET_AUTH_ENABLED
  )
  for name in "${required[@]}"; do
    if [[ -z "${!name:-}" ]]; then
      echo "Missing Garnet config: $name" >&2
      exit 1
    fi
  done

  # Defaults: persistent storage on, dir /var/lib/garnet. Set
  # GARNET_ENABLE_STORAGE=false for in-memory only.
  : "${GARNET_ENABLE_STORAGE:=true}"
  : "${GARNET_STORAGE_DIR:=/var/lib/garnet}"
  export GARNET_ENABLE_STORAGE GARNET_STORAGE_DIR

  SERVICE_FILE="/etc/systemd/system/${GARNET_SERVICE_NAME}.service"
}

require_systemd() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required on the target server." >&2
    exit 1
  fi
}

require_tools() {
  local missing=()
  for t in curl tar xz sha256sum; do
    if ! command -v "$t" >/dev/null 2>&1; then
      missing+=("$t")
    fi
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "Missing required tools: ${missing[*]}" >&2
    exit 1
  fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "x64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "Unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

write_runner() {
  install -d -m 0755 /usr/local/bin
  cat > "$RUNNER_FILE" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail

set -a
. /etc/garnet/garnet.env
set +a

args=(
  --port "$GARNET_PORT"
  --bind "$GARNET_BIND_ADDRESS"
  --memory "$GARNET_LOG_MEMORY_SIZE"
  --index "$GARNET_INDEX_MEMORY_SIZE"
  --compaction-freq "$GARNET_COMPACTION_FREQUENCY_SECS"
  --compaction-type "$GARNET_COMPACTION_TYPE"
)

if [[ "$GARNET_DISABLE_OBJECTS" == "true" ]]; then
  args+=(--no-obj)
fi

if [[ "$GARNET_DISABLE_PUBSUB" == "true" ]]; then
  args+=(--no-pubsub)
fi

if [[ "$GARNET_ENABLE_STORAGE" == "true" ]]; then
  # Persistent storage via write-ahead log. Garnet requires the logdir to exist.
  install -d -m 0755 "$GARNET_STORAGE_DIR"
  args+=(--aof --logdir "$GARNET_STORAGE_DIR")
fi

if [[ "$GARNET_AUTH_ENABLED" == "true" ]]; then
  if [[ -z "${GARNET_PASSWORD:-}" ]]; then
    echo "GARNET_PASSWORD is required when GARNET_AUTH_ENABLED=true" >&2
    exit 1
  fi
  args+=(--auth Password --password "$GARNET_PASSWORD")
fi

if [[ -n "${GARNET_EXTRA_ARGS:-}" && "${GARNET_EXTRA_ARGS:-}" != TODO_NONE ]]; then
  read -r -a extra_args <<< "$GARNET_EXTRA_ARGS"
  args+=("${extra_args[@]}")
fi

exec "$BINARY_PATH" "${args[@]}"
SCRIPT
  chmod 0755 "$RUNNER_FILE"
}

write_service() {
  # When persistent storage is enabled, the binary must be allowed to write to
  # GARNET_STORAGE_DIR (default /var/lib/garnet). The unit grants ReadWritePaths
  # for that dir; otherwise the path is left as the default ProtectSystem=strict
  # allows no writes outside /var and /etc.
  local rw_paths=""
  if [[ "${GARNET_ENABLE_STORAGE:-true}" == "true" ]]; then
    rw_paths="ReadWritePaths=${GARNET_STORAGE_DIR:-/var/lib/garnet}"
  fi

  cat > "$SERVICE_FILE" <<SERVICE
[Unit]
Description=Garnet Cache Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$CONFIG_FILE
Environment=BINARY_PATH=$INSTALL_DIR/$BINARY
ExecStart=$RUNNER_FILE
Restart=always
RestartSec=3
TimeoutStartSec=180
TimeoutStopSec=30
LimitNOFILE=1048576
$rw_paths

[Install]
WantedBy=multi-user.target
SERVICE
}

install_garnet() {
  require_systemd
  require_tools

  local arch expected_sha
  arch="$(detect_arch)"
  case "$arch" in
    x64)    expected_sha="$GARNET_SHA256_X64" ;;
    arm64)  expected_sha="$GARNET_SHA256_ARM64" ;;
  esac

  local asset="linux-${arch}-based.tar.xz"
  local url="https://github.com/microsoft/garnet/releases/download/v${GARNET_VERSION}/${asset}"
  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  echo "Downloading $url"
  curl -fsSL -o "$tmpdir/$asset" "$url"

  local actual_sha
  # sha256sum outputs "<hash>  <file>"; grab the first token.
  actual_sha="$(sha256sum "$tmpdir/$asset" | awk '{print $1}')"
  if [[ "$actual_sha" != "$expected_sha" ]]; then
    echo "Checksum mismatch for $asset" >&2
    echo "  expected: $expected_sha" >&2
    echo "  actual:   $actual_sha" >&2
    exit 1
  fi
  echo "Checksum OK ($actual_sha)"

  tar -xJf "$tmpdir/$asset" -C "$tmpdir"
  # The tarball extracts to a directory containing the garnet-server executable.
  local bin
  bin="$(find "$tmpdir" -type f -name "$BINARY" -path '*linux*based*' | head -n1)"
  if [[ -z "$bin" ]]; then
    # Fallback: any executable named garnet-server.
    bin="$(find "$tmpdir" -type f -name "$BINARY" | head -n1)"
  fi
  if [[ -z "$bin" ]]; then
    echo "Could not find $BINARY in extracted archive" >&2
    exit 1
  fi

  install -d -m 0755 "$INSTALL_DIR"
  install -m 0755 "$bin" "$INSTALL_DIR/$BINARY"

  write_runner
  write_service
  systemctl daemon-reload
  systemctl enable "$GARNET_SERVICE_NAME"
  systemctl status "$GARNET_SERVICE_NAME" --no-pager || true
}

main() {
  local command="${1:-status}"
  load_config

  case "$command" in
    install)
      install_garnet
      ;;
    start)
      systemctl start "$GARNET_SERVICE_NAME"
      systemctl is-active --quiet "$GARNET_SERVICE_NAME"
      ;;
    restart)
      systemctl restart "$GARNET_SERVICE_NAME"
      systemctl is-active --quiet "$GARNET_SERVICE_NAME"
      ;;
    stop)
      systemctl stop "$GARNET_SERVICE_NAME"
      ;;
    status)
      systemctl status "$GARNET_SERVICE_NAME" --no-pager
      ;;
    *)
      echo "Unsupported command: $command" >&2
      exit 1
      ;;
  esac
}

main "$@"
