# Makefile for high-performance Go game server

.PHONY: all build run test clean load-test deps docker

# Variables
BINARY_NAME=server
LOADTEST_BINARY=loadtest
MAIN_PATH=cmd/server/main.go
LOADTEST_PATH=cmd/loadtest/main.go
ARTILLERY_DIR=../../utils/testing/artillery

# Build flags for optimization
BUILD_FLAGS=-ldflags="-s -w" -trimpath

all: deps build

# Install dependencies
deps:
	@echo "📦 Installing dependencies..."
	go mod tidy
	go mod download

# Build the server
build:
	@echo "🏗️  Building server..."
	go build $(BUILD_FLAGS) -o $(BINARY_NAME) $(MAIN_PATH)

# Build optimized release version
build-release:
	@echo "🚀 Building optimized release..."
	CGO_ENABLED=0 GOOS=linux go build $(BUILD_FLAGS) -o $(BINARY_NAME) $(MAIN_PATH)

# Run the server with optimized settings
run: build
	@echo "🚀 Starting server with performance optimizations..."
	@export GOGC=800 && \
	export GOMAXPROCS=0 && \
	export GOMEMLIMIT=4GiB && \
	export PORT=8108 && \
	export TICK_RATE=60 && \
	export MAX_CONNECTIONS=12000 && \
	export EVENT_CHANNEL_SIZE=100000 && \
	./$(BINARY_NAME)

# Run in development mode
dev:
	@echo "🛠️  Starting development server..."
	@export PORT=8108 && \
	export TICK_RATE=60 && \
	go run $(MAIN_PATH)

# Build load test
build-test:
	@echo "🧪 Building load test..."
	go build -o $(LOADTEST_BINARY) $(LOADTEST_PATH)

# Run load test
load-test: build-test
	@echo "🧪 Running load test..."
	@echo "📝 Make sure server is running on port 8108"
	./$(LOADTEST_BINARY)

# Run comprehensive test suite
test:
	@echo "🧪 Running tests..."
	go test -v ./...

# Run benchmark tests
bench:
	@echo "📊 Running benchmarks..."
	go test -bench=. -benchmem ./...

# Clean build artifacts
clean:
	@echo "🧹 Cleaning..."
	rm -f $(BINARY_NAME) $(LOADTEST_BINARY)
	go clean

# Format code
fmt:
	@echo "📝 Formatting code..."
	go fmt ./...

# Lint code
lint:
	@echo "🔍 Linting code..."
	golangci-lint run

# Profile CPU
profile-cpu: build
	@echo "🔬 Starting CPU profiling..."
	@export GOGC=800 && \
	export GOMAXPROCS=0 && \
	go tool pprof -http=:8080 $(BINARY_NAME) cpu.prof

# Profile memory
profile-mem: build
	@echo "🧠 Starting memory profiling..."
	@export GOGC=800 && \
	export GOMAXPROCS=0 && \
	go tool pprof -http=:8080 $(BINARY_NAME) mem.prof

# Build Docker image
docker:
	@echo "🐳 Building Docker image..."
	docker build -t pixi-game-server .

# Run with Docker
docker-run:
	@echo "🐳 Running Docker container..."
	docker run -p 8108:8108 -e GOGC=800 -e GOMAXPROCS=0 pixi-game-server

# Performance test with artillery
artillery-test:
	@echo "🎯 Running artillery performance test..."
	@echo "📝 Make sure Go server is running on port 8108"
	cd $(ARTILLERY_DIR) && artillery run artillery-config.yml

# Quick artillery test with lower load
artillery-quick:
	@echo "🎯 Running quick artillery test..."
	@echo "📝 Make sure Go server is running on port 8108"
	cd $(ARTILLERY_DIR) && artillery quick --count 100 --num 10 ws://localhost:8108/ws

# Test protocol compatibility
test-protocol:
	@echo "🔍 Testing protocol compatibility with artillery-processor.cjs..."
	@cd $(ARTILLERY_DIR) && node -e " \
		const proc = require('./artillery-processor.cjs'); \
		console.log('✅ Artillery processor loaded successfully'); \
		console.log('📊 Message types:', Object.keys(proc)); \
	"

# Monitor server resources
monitor:
	@echo "📊 Monitoring server resources..."
	@while true; do \
		echo "=== $(shell date) ==="; \
		echo "🔗 Connections: $(shell ss -t state established '( dport = :8108 )' | wc -l)"; \
		echo "💾 Memory: $(shell ps aux | grep './$(BINARY_NAME)' | grep -v grep | awk '{print $$6}') KB"; \
		echo "🔥 CPU: $(shell ps aux | grep './$(BINARY_NAME)' | grep -v grep | awk '{print $$3}')%"; \
		echo ""; \
		sleep 5; \
	done

# Health check
health:
	@echo "❤️  Checking server health..."
	@curl -s http://localhost:8108/health | jq .

# Get metrics
metrics:
	@echo "📈 Getting server metrics..."
	@curl -s http://localhost:8108/metrics | jq .

# Quick performance check
perf-check:
	@echo "⚡ Quick performance check..."
	@echo "1. Building optimized binary..."
	@make build-release > /dev/null
	@echo "2. Starting server in background..."
	@./$(BINARY_NAME) > server.log 2>&1 &
	@sleep 3
	@echo "3. Running 100 concurrent connections test..."
	@make build-test > /dev/null
	@timeout 10s ./$(LOADTEST_BINARY) || true
	@echo "4. Stopping server..."
	@pkill -f './$(BINARY_NAME)' || true
	@echo "5. Performance results:"
	@grep -E "(Connected|Performance)" server.log | tail -5
	@rm -f server.log

help:
	@echo "🎮 High-Performance Go Game Server"
	@echo ""
	@echo "Available commands:"
	@echo "  build         - Build the server binary"
	@echo "  run           - Run server with performance optimizations"
	@echo "  dev           - Run in development mode"
	@echo "  test          - Run test suite"
	@echo "  load-test     - Run load test (server must be running)"
	@echo "  clean         - Clean build artifacts"
	@echo "  docker        - Build Docker image"
	@echo "  health        - Check server health"
	@echo "  metrics       - Get server metrics"
	@echo "  monitor       - Monitor server resources"
	@echo "  perf-check    - Quick performance verification"
	@echo ""
	@echo "Performance commands:"
	@echo "  profile-cpu   - CPU profiling"
	@echo "  profile-mem   - Memory profiling"
	@echo "  artillery-test - Load test with artillery"
	@echo ""
	@echo "🎯 For 10K+ players: make run"
