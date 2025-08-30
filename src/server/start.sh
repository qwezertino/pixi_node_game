#!/bin/bash

# Скрипт для запуска Go сервера с оптимизированными параметрами

# Установка переменных окружения для оптимизации
export GOGC=800                    # Reduce GC frequency for high throughput
export GOMAXPROCS=0               # Use all available CPU cores
export GOMEMLIMIT=4GiB            # Set memory limit

# Game configuration
export PORT=8108
export HOST=0.0.0.0
export TICK_RATE=60               # 60 FPS for high responsiveness
export WORKERS=0                  # Auto-detect based on CPU cores
export BROADCAST_WORKERS=0        # Auto-detect (CPU cores * 2)
export MAX_CONNECTIONS=12000      # Support for 10K+ players
export EVENT_CHANNEL_SIZE=100000  # Large event buffer

# Performance tuning
export READ_BUFFER_SIZE=4096
export WRITE_BUFFER_SIZE=4096
export RATE_LIMIT_MSG_SEC=60
export RATE_LIMIT_BURST=10

# Static files directory
export STATIC_DIR=../dist

echo "🚀 Starting high-performance Go game server..."
echo "🎯 Optimizations: GOGC=$GOGC, MaxProcs=$(nproc), MemLimit=$GOMEMLIMIT"
echo "⚙️  Config: Port=$PORT, TickRate=${TICK_RATE}Hz, MaxConns=$MAX_CONNECTIONS"

# Build and run
cd "$(dirname "$0")"
go build -o server cmd/server/main.go
./server
