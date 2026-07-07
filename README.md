# code-guda-gateway

Go HTTP gateway that exposes a `code.guda.studio`-compatible facade for
GrokSearch MCP.

## Contract

The gateway is designed for GrokSearch configurations like:

```bash
GUDA_BASE_URL=http://127.0.0.1:8080
GUDA_API_KEY=dev
```

It exposes:

- `GET /grok/v1/models`
- `POST /grok/v1/chat/completions`
- `POST /tavily/search`
- `POST /tavily/extract`
- `POST /tavily/map`
- `POST /firecrawl/search`
- `POST /firecrawl/scrape`
- `GET /healthz`

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Listen address. |
| `GUDA_GATEWAY_KEYS` | none | Comma-separated inbound bearer keys. |
| `GROK_UPSTREAM_BASE_URL` | none | OpenAI-compatible upstream base, usually ending in `/v1`. |
| `GROK_UPSTREAM_API_KEYS` | none | Comma-separated upstream Grok/OpenAI-compatible keys. |
| `TAVILY_BASE_URL` | `https://api.tavily.com` | Tavily REST API base. |
| `TAVILY_API_KEYS` | none | Comma-separated official Tavily keys. |
| `FIRECRAWL_BASE_URL` | `https://api.firecrawl.dev/v2` | Firecrawl REST API base. |
| `FIRECRAWL_API_KEYS` | none | Comma-separated official Firecrawl keys. |

## Run

```bash
ADDR=:8080 \
GUDA_GATEWAY_KEYS=dev \
GROK_UPSTREAM_BASE_URL=https://api.x.ai/v1 \
GROK_UPSTREAM_API_KEYS=... \
TAVILY_API_KEYS=... \
FIRECRAWL_API_KEYS=... \
go run ./cmd/guda-gateway
```

## Verify

```bash
go test ./...
go test -race ./...
go build ./cmd/guda-gateway
```

Manual smoke:

```bash
curl -H 'Authorization: Bearer dev' http://127.0.0.1:8080/healthz
```
