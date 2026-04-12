# ─────────────────────────────────────────────
# Stage 1: Build Pixi.js client with Bun + Vite
# ─────────────────────────────────────────────
FROM oven/bun:1.2-alpine AS client-builder

WORKDIR /build

# Install deps first (layer cache)
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

# Copy source needed for Vite build
COPY index.html vite.config.ts tsconfig.json ./
COPY public ./public
COPY src/client ./src/client
COPY src/shared ./src/shared

# Build → dist/
RUN bun run build:client

# ─────────────────────────────────────────────
# Stage 2: Build Go game server
# ─────────────────────────────────────────────
FROM golang:1.26.2-alpine AS server-builder

WORKDIR /build/src/server

# Download modules first (layer cache)
COPY src/server/go.mod src/server/go.sum ./
RUN go mod download

# Copy server source
COPY src/server/ ./

# Copy gameConfig.json for go:embed (must exist before go build)
COPY src/shared/gameConfig.json ./internal/config/gameConfig.json

# Build a static, trimmed binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /app/server \
    ./cmd/server/main.go

# ─────────────────────────────────────────────
# Stage 3: Minimal runtime image
# ─────────────────────────────────────────────
FROM alpine:3.23

# ca-certificates for potential TLS; wget is in busybox (healthcheck)
RUN apk --no-cache add ca-certificates \
    && adduser -D -s /bin/sh gameserver

WORKDIR /app

# Copy binary and client static files
COPY --from=server-builder /app/server ./server
COPY --from=client-builder /build/dist  ./static

RUN chown -R gameserver:gameserver /app

USER gameserver

ENV PORT=8108 \
    STATIC_DIR=/app/static

EXPOSE 8108

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8108/health || exit 1

CMD ["./server"]
