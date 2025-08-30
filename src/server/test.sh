#!/bin/bash

# Build и запуск нагрузочного тестирования

echo "🏗️  Building load test..."
cd "$(dirname "$0")"
go build -o loadtest cmd/loadtest/main.go

if [ $? -ne 0 ]; then
    echo "❌ Build failed"
    exit 1
fi

echo "🧪 Starting load test..."
echo "📝 Make sure the Go server is running on port 8108"
echo ""

./loadtest
