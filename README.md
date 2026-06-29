# Clay

<p align="center">
  <img src="https://philmade.github.io/images/portfolio/clay.png" alt="Clay" width="480">
</p>

An autonomous AI agent that lives in a container. You talk to it, it changes shape.

Clay is a multi-agent orchestrator built on [Google ADK](https://google.github.io/adk-docs/) that runs in an Alpine Linux container with persistent memory, identity, a public web page, and the ability to modify its own source code.

## What it does

- **Persistent memory** — SQLite-backed with full-text search. Survives restarts.
- **Identity** — SOUL.md and IDENTITY.md files define who the agent is.
- **Self-modification** — The agent can edit its own Go source, recompile via an external build service, and hot-swap itself while running.
- **Starlark extensions** — Write Python-like scripts that run embedded in the Go binary. No restart needed.
- **Web presence** — Each container serves a public page at its subdomain.
- **Messaging** — Connects to Telegram via Matterbridge. Also accepts HTTP messages.
- **Autonomous heartbeat** — Keeps working when nobody is watching.

## Architecture

```
clay              — ADK multi-agent orchestrator (port 8081 internal)
clay-proxy        — Public HTTP proxy (port 8080 → ADK + static files)
clay-bridge       — Matterbridge ↔ ADK connector
clay-medic        — Supervisor with crash recovery + binary hot-swap
matterbridge      — Telegram bot bridge (optional)
```

The agent has two sub-agents: **claude** (coding — files, bash, builds) and **research** (web search + page fetching). The coordinator handles memory, identity, tasks, Starlark extensions, and platform integration.

## Quick start

```bash
# 1. Clone and configure
git clone https://github.com/philmade/clay.git
cd clay
cp .env.example .env
# Edit .env with your API key and model

# 2. Build and run (docker-compose.yml wires up ports + persistent volumes)
docker compose up -d --build

# 3. Talk to it
curl -X POST http://localhost:8080/msg \
  -H 'Content-Type: application/json' \
  -d '{"text": "hello, who are you?"}'
```

The proxy serves the public page and API on port `8080`; the ADK debugger UI is on `8081`.

## Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | Yes | — | API key for the LLM backend |
| `ANTHROPIC_API_BASE` | No | `https://api.anthropic.com` | Base URL (supports any Anthropic-compatible API) |
| `ANTHROPIC_MODEL` | No | `claude-sonnet-4-20250514` | Model name |
| `MODEL_PROVIDER` | No | `anthropic` | `anthropic` or `gemini` |
| `TELEGRAM_BOT` | No | — | Telegram bot token (enables Telegram messaging) |
| `TELEGRAM_CHAT_ID` | No | — | Telegram chat ID for the bot |
| `CLAY_ROOT` | No | `/app` | Application root directory |
| `CLAY_DB` | No | `/app/data/messages.db` | SQLite database path |
| `BUILD_SERVICE_URL` | No | `http://claw-build-service:9090` | External build service for self-modification |

## Container filesystem

```
/app/
├── clay                  # Main agent binary
├── clay-medic            # Supervisor binary
├── clay-bridge           # Matterbridge connector
├── clay-proxy            # Public HTTP proxy
├── src/                  # Full Go source (agent can read + modify)
├── data/                 # PERSISTENT — memory, extensions, logs
│   ├── messages.db       # SQLite memory database
│   └── extensions/       # Starlark .star scripts
├── soul/                 # PERSISTENT — identity files
│   ├── SOUL.md
│   └── IDENTITY.md
├── public/               # PERSISTENT — web page
│   └── index.html
└── builds/               # Hot-swap staging area
```

## Volumes

All three must be persistent for the agent to retain its state:

| Volume | Container path | Contents |
|--------|---------------|----------|
| `clay-data` | `/app/data` | Memory DB, Starlark extensions, build failure logs |
| `clay-soul` | `/app/soul` | SOUL.md, IDENTITY.md (agent personality) |
| `clay-public` | `/app/public` | Public web page, blog posts |

## Self-modification

Clay can modify its own Go source and recompile:

1. Agent edits files in `/app/src/`
2. Calls `build_check()` to compile without deploying
3. Fixes errors, repeats until clean
4. Calls `build_and_deploy()` — sends source to build service, receives binary
5. Medic detects new binary, hot-swaps, watches for 30s
6. If it crashes → automatic rollback to previous binary

This requires the external build service (see `cmd/buildservice`).

## Development

```bash
# Build locally
cd clay && go build ./...

# Cross-compile for Linux (hot-swap into running container)
cd clay && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ../dev-builds/clay.new .
```

## License

MIT
