# syntax=docker/dockerfile:1

FROM oven/bun:1 AS admin-builder
WORKDIR /src/web/admin
COPY web/admin/package.json web/admin/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/admin/ ./
RUN bun run build

FROM golang:1.25-bookworm AS go-builder
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=admin-builder /src/web/admin/dist/ internal/adminweb/assets/dist/
RUN printf '%s\n' 'placeholder so go:embed has a stable directory' > internal/adminweb/assets/dist/.keep
RUN CGO_ENABLED=0 go build -buildvcs=false -o /out/guda-gateway ./cmd/guda-gateway \
  && CGO_ENABLED=0 go build -buildvcs=false -o /out/guda-gateway-admin ./cmd/guda-gateway-admin

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates \
  && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /out/guda-gateway /usr/local/bin/guda-gateway
COPY --from=go-builder /out/guda-gateway-admin /usr/local/bin/guda-gateway-admin
ENV ADDR=0.0.0.0:8080 \
    DB_PATH=/var/lib/code-guda-gateway/gateway.db \
    GUDA_MASTER_KEY_PATH=/etc/code-guda-gateway/master.key \
    GUDA_ADMIN_COOKIE_SECURE=false
EXPOSE 8080
RUN mkdir -p /var/lib/code-guda-gateway /etc/code-guda-gateway
ENTRYPOINT ["/usr/local/bin/guda-gateway"]
