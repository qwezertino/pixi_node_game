# Makefile for Pixi Node Game - 2D Multiplayer Game
# Client: TypeScript + PixiJS, Server: Go

.PHONY: all build build-client build-server run run-client run-server dev clean test

# Variables
SERVER_DIR=src/server
CLIENT_BUILD_DIR=dist
SERVER_BINARY=server
SERVER_OUTPUT_DIR=$(CLIENT_BUILD_DIR)

# Install dependencies for both client and server
deps:
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

# Run server in development mode
dev-server: build-server
	echo "🚀 Starting server in development mode..."
	cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY)
# 	@echo "🚀 Starting server in development mode..."
# 	cp src/shared/gameConfig.json src/server/internal/config/
# 	cd $(SERVER_DIR) && go run cmd/server/main.go
# 	@echo "🧹 Cleaning up temporary config file..."
# 	rm -f src/server/internal/config/gameConfig.json

# Run production build
run: build
	@echo "🚀 Starting production server..."
	cd $(CLIENT_BUILD_DIR) && ./$(SERVER_BINARY)

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


# Run server load tests
load-test:
	@echo "⚡ Running server load tests..."
	artillery run utils/testing/artillery/artillery-config.yml

# Help
help:
	@echo "Available commands:"
	@echo "  build           - Build client and server"
	@echo "  build-client    - Build only client (TypeScript + PixiJS)"
	@echo "  build-server    - Build only server (Go) with embedded config"
	@echo "  build-release   - Build only optimized server (Go) with embedded config"
	@echo "  dev-client      - Run client development server"
	@echo "  dev-server      - Run server development mode"
	@echo "  run             - Run production build"
	@echo "  clean           - Clean build artifacts"
	@echo "  load-test       - Run server load tests with Artillery"
	@echo "  deps            - Install dependencies"
