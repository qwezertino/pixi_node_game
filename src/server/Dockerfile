# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build optimized binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o server cmd/server/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata curl

# Create non-root user
RUN adduser -D -s /bin/sh gameserver

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/server .

# Copy static files if needed
COPY --from=builder /build/static ./static

# Change ownership
RUN chown -R gameserver:gameserver /app

# Switch to non-root user
USER gameserver

# Set environment variables for optimization
ENV GOGC=800
ENV GOMAXPROCS=0
ENV PORT=8108
ENV TICK_RATE=60
ENV MAX_CONNECTIONS=12000
ENV EVENT_CHANNEL_SIZE=100000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8108/health || exit 1

# Expose port
EXPOSE 8108

# Run the server
CMD ["./server"]
