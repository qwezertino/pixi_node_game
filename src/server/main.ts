import { handleOptimizedWebSocket, shutdownOptimizedWebSocket } from "./handlers/websocketHandler";

const PORT: number = 8108;

console.log(`🚀 Starting HIGH-PERFORMANCE game server on port ${PORT}...`);
console.log(`🎯 Target: 10,000+ concurrent players with full viewport visibility`);

// Optimized Bun.serve configuration for extreme scale
const server = Bun.serve({
    port: PORT,
    hostname: "0.0.0.0",

    // Use optimized WebSocket handler
    websocket: handleOptimizedWebSocket(),

    fetch(req, server) {
        const url = new URL(req.url);

        // Optimized WebSocket upgrade path
        if (url.pathname === "/ws") {
            const upgradeSuccess = server.upgrade(req, {
                data: {
                    playerId: "", // Will be set in open handler
                    lastActivity: Date.now(),
                    messageCount: 0,
                    joinTime: Date.now()
                }
            });

            if (upgradeSuccess) {
                return; // Connection upgraded successfully
            }

            return new Response("WebSocket upgrade failed", { status: 400 });
        }

        // Serve static files with optimized caching
        if (url.pathname === "/") {
            return new Response(Bun.file("dist/index.html"), {
                headers: {
                    "Content-Type": "text/html",
                    "Cache-Control": "public, max-age=3600",
                    "X-Server-Mode": "high-performance"
                },
            });
        }

        // Serve other static assets
        const filePath = "dist" + url.pathname;
        const file = Bun.file(filePath);

        return new Response(file, {
            headers: {
                "Cache-Control": "public, max-age=86400", // 24 hour cache for assets
                "X-Server-Mode": "high-performance"
            }
        });
    },
});

// Graceful shutdown handling
process.on("SIGINT", () => {
    console.log("\n🛑 Shutting down server gracefully...");
    shutdownOptimizedWebSocket();
    server.stop();
    process.exit(0);
});

process.on("SIGTERM", () => {
    console.log("\n🛑 Server terminated gracefully...");
    shutdownOptimizedWebSocket();
    server.stop();
    process.exit(0);
});

console.log(`✅ High-performance server ready at http://localhost:${PORT}`);
console.log(`📊 Monitoring: Performance stats will be logged every 10 seconds`);
console.log(`🔗 WebSocket endpoint: ws://localhost:${PORT}/ws`);
console.log(`🎮 Ready for 10,000+ concurrent players!`);
