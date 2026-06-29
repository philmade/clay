#!/bin/bash
set -e
cd /app

# --- User-configured environment variables (from UI settings) ---
if [ -f /app/data/.env ]; then
    set -a
    source /app/data/.env
    set +a
fi

# --- Gather identity (optional — skip if no keys provided) ---
if [ -n "$GATHER_PRIVATE_KEY" ]; then
    mkdir -p /root/.gather/keys
    echo "$GATHER_PRIVATE_KEY" | base64 -d > /root/.gather/keys/private.pem
    echo "$GATHER_PUBLIC_KEY" | base64 -d > /root/.gather/keys/public.pem
    chmod 600 /root/.gather/keys/*.pem
fi

# --- Matterbridge config (optional — skip if no Telegram token) ---
if [ -n "$TELEGRAM_BOT" ]; then
    cat > /app/matterbridge.toml <<MBEOF
[telegram.claw]
Token="$TELEGRAM_BOT"
RemoteNickFormat=""

[api.claw]
BindAddress="127.0.0.1:4242"

[[gateway]]
name="clay"
enable=true

[[gateway.inout]]
account="telegram.claw"
channel="${TELEGRAM_CHAT_ID:-0}"

[[gateway.inout]]
account="api.claw"
channel="api"
MBEOF
    echo "Starting matterbridge..."
    matterbridge -conf /app/matterbridge.toml > /tmp/matterbridge.log 2>&1 &
fi

# --- First-boot init: copy defaults if volumes are empty ---
if [ ! -f /app/soul/SOUL.md ]; then
    cp /app/defaults/soul/* /app/soul/ 2>/dev/null || true
fi

if [ ! -f /app/public/index.html ]; then
    cp /app/defaults/public/* /app/public/ 2>/dev/null || true
fi

mkdir -p /app/data/extensions
if [ ! -f /app/data/extensions/hello.star ]; then
    cp /app/defaults/extensions/* /app/data/extensions/ 2>/dev/null || true
fi

# --- Port layout: proxy on :8080 (public), ADK on :8081 (debugger UI) ---
export ADK_PORT=8081

# --- Start clay (ADK orchestrator on :8081 with debugger UI) ---
# Tell the webui where the browser can reach the API (same origin to avoid CORS)
WEBUI_ADDRESS="${ADK_WEBUI_ADDRESS:-http://localhost:${ADK_PORT}/api}"
echo "Starting clay on :${ADK_PORT}..."
./clay web -port ${ADK_PORT} -write-timeout 10m api -sse-write-timeout 10m webui -api_server_address "${WEBUI_ADDRESS}" > /tmp/adk-go.log 2>&1 &

# --- Start proxy (public-facing on :8080 → ADK on :8081) ---
echo "Starting clay-proxy on :8080..."
PROXY_ADDR=":8080" ADK_INTERNAL="http://127.0.0.1:${ADK_PORT}" PUBLIC_DIR="/app/public" \
    ./clay-proxy > /tmp/proxy.log 2>&1 &

# --- Start bridge (Matterbridge ↔ ADK connector) ---
echo "Starting clay-bridge..."
ADK_URL="http://127.0.0.1:${ADK_PORT}" ./clay-bridge > /tmp/bridge.log 2>&1 &

# --- Medic as foreground supervisor (PID 1) ---
echo "Starting clay-medic..."
exec ./clay-medic
