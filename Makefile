# Makefile for Pixi Node Game - 2D Multiplayer Game
# Client: TypeScript + PixiJS, Server: Go

.PHONY: all build build-client build-server run run-client run-server dev clean test docker-init docker-up docker-build docker-test docker-monitoring docker-down

# Variables
SERVER_DIR=src/server
CLIENT_BUILD_DIR=dist
SERVER_BINARY=server
SERVER_OUTPUT_DIR=$(CLIENT_BUILD_DIR)
COMPOSE=docker compose -f docker/docker-compose.yml --project-name pixi_game --env-file .env

# Install dependencies for both client and server
install:
	@echo "📦 Installing client dependencies..."
	bun install
	@echo "📦 Installing server dependencies..."
	cd $(SERVER_DIR) && go mod tidy && go mod download

# Build everything
build: build-client build-server

# Build client (TypeScript + PixiJS)
build-client:
	@echo "🏗️  Building client..."
	bun run build:client

# Build server (Go) and output to dist directory
build-server:
	@echo "🏗️  Building server..."
	@echo "📋 Copying config for embedding..."
	cp src/shared/gameConfig.json src/server/internal/config/
	cd $(SERVER_DIR) && go build -ldflags="-s -w" -trimpath -o ../../$(SERVER_OUTPUT_DIR)/$(SERVER_BINARY) cmd/server/main.go
	@echo "🧹 Cleaning up temporary config file..."
	rm -f src/server/internal/config/gameConfig.json
	@echo "📋 Copying config files to dist..."

build-server-linux:
	@echo "🚀 Building linux server release..."
	@echo "📋 Copying config for embedding..."
	cp src/shared/gameConfig.json src/server/internal/config/
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o ../../$(SERVER_OUTPUT_DIR)/$(SERVER_BINARY) cmd/server/main.go
	@echo "🧹 Cleaning up temporary config file..."
	rm -f src/server/internal/config/gameConfig.json
	@echo "📋 Copying config files to dist..."

# Build optimized release version
build-release: build-client
	@echo "🚀 Building optimized server release..."
	@echo "📋 Copying config for embedding..."
	cp src/shared/gameConfig.json src/server/internal/config/
	cd $(SERVER_DIR) && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o ../../$(SERVER_OUTPUT_DIR)/$(SERVER_BINARY) cmd/server/main.go
	@echo "🧹 Cleaning up temporary config file..."
	rm -f src/server/internal/config/gameConfig.json
	@echo "📋 Copying config files to dist..."

# Run client development server
dev-client:
	@echo "🌐 Starting client development server..."
	bun run dev:client

# Запустить клиент и сервер параллельно (dev режим, без Docker)
dev:
	@echo "🚀 Starting dev: client (Vite :8109) + server (:8108)..."
	@$(MAKE) build-server
	@set -a && . ./.env && set +a && \
		(cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY) &) && \
		bun run dev:client

# Run server in development mode
dev-server: build-server
	@echo "🚀 Starting server in development mode..."
	@set -a && . ./.env && set +a && cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY)


dev-server-linux: build-server-linux
	@echo "🚀 Starting server in development mode..."
	@set -a && . ./.env && set +a && cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY)

# Run production build
run: build
	@echo "🚀 Starting production server..."
	@set -a && . ./.env && set +a && cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY)

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -rf $(CLIENT_BUILD_DIR)
	rm -f $(SERVER_DIR)/$(SERVER_BINARY)
	rm -f src/server/internal/config/gameConfig.json


# Lint code
lint:
	@echo "🔍 Linting code..."
	golangci-lint run


# Docker: выставить правильные права на директории данных
# Prometheus=65534 (nobody), Grafana=472, Loki=0 (root, задан в compose)
docker-chown:
	@echo "🔐 Setting data directory permissions..."
	@mkdir -p docker/data/prometheus docker/data/grafana docker/data/loki
	@sudo chown -R 65534:65534 docker/data/prometheus
	@sudo chown -R 472:472 docker/data/grafana
	@sudo chmod -R 755 docker/data/prometheus docker/data/grafana docker/data/loki
	@echo "✅ Permissions set"

# Docker: создать директории для хранения данных (только при первом запуске)
docker/data/.initialized:
	@touch docker/data/.initialized

docker-init: docker-chown docker/data/.initialized

# Docker: запустить без пересборки
docker-up: docker-init
	@echo "🐳 Starting containers (no rebuild)..."
	$(COMPOSE) up -d

# Docker: запустить с пересборкой
docker-upbuild: docker-init
	@echo "🐳 Building and starting containers..."
	$(COMPOSE) up --build -d

# Docker: запустить нагрузочный тест через artillery
docker-test:
	@echo "⚡ Running artillery load test in Docker..."
	$(COMPOSE) --profile test run --rm artillery

# Docker: открыть ссылки на Prometheus и Grafana
docker-monitoring:
	@set -a && . ./.env && set +a && \
		echo "📊 Prometheus: http://localhost:$${PROMETHEUS_PORT:-9090}" && \
		echo "📈 Grafana:    http://localhost:$${GRAFANA_PORT:-3000}  (admin / $${GRAFANA_ADMIN_PASSWORD:-admin})"

# Docker: запустить без пересборки
docker-down:
	@echo "🐳 Stopping containers..."
	$(COMPOSE) down

docker-ps:
	@echo "🐳 Stopping containers..."
	$(COMPOSE) ps

# Run server load tests (локально, artillery должен быть установлен)
load-test:
	@echo "⚡ Running server load tests..."
	artillery run utils/testing/artillery/artillery-config.yml

# Help
help:
	@echo "Available commands:"
	@echo "  build           - Build client and server"
	@echo "  build-client    - Build only client (TypeScript + PixiJS)"
	@echo "  build-server    - Build only server (Go) with embedded config"
	@echo "  build-server-linux - Build only server (Go) with embedded config (Linux)"
	@echo "  build-release   - Build only optimized server (Go) with embedded config"
	@echo "  dev-client      - Run client development server"
	@echo "  dev-server      - Run server development mode"
	@echo "  dev-server-linux - Run server development mode (Linux)"
	@echo "  run             - Run production build"
	@echo "  clean           - Clean build artifacts"
	@echo "  load-test       - Run server load tests with Artillery"
	@echo "  deps            - Install dependencies"
