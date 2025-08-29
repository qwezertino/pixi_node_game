import { handleWebSocket } from "./handlers/websocketHandler";

const PORT: number = 8108;

console.log(`🚀 Starting high-performance game server on port ${PORT}...`);

// Use Bun.serve directly with optimizations for high concurrent connections
const server = Bun.serve({
    port: PORT,
    hostname: "0.0.0.0", // Allow external connections

    // WebSocket handler
    websocket: handleWebSocket(),

    fetch(req, server) {
        const url = new URL(req.url);

        // Handle WebSocket upgrade with optimized path
        if (url.pathname === "/ws") {
            const upgradeSuccess = server.upgrade(req, {
                // WebSocket-specific data
                data: {
                    playerId: "", // Will be set in open handler
                    lastActivity: Date.now(),
                }
            });

            if (upgradeSuccess) {
                return; // Connection upgraded successfully
            }

            // Upgrade failed
            return new Response("WebSocket upgrade failed", { status: 400 });
        }

        // Serve static files
        if (url.pathname === "/") {
            return new Response(Bun.file("dist/index.html"), {
                headers: {
                    "Content-Type": "text/html",
                    "Cache-Control": "public, max-age=3600" // 1 hour cache
                },
            });
        }

        // Serve other static assets
        const filePath = "dist" + url.pathname;
        const file = Bun.file(filePath);

        return new Response(file, {
            headers: {
                "Cache-Control": "public, max-age=86400" // 24 hour cache for assets
            }
        });
    },

    // Error handling
    error(error) {
        console.error("Server error:", error);
        return new Response("Internal Server Error", { status: 500 });
    },
});

// Log server startup
console.log(`✅ Game server running on http://localhost:${PORT}`);
console.log(`🔌 WebSocket endpoint: ws://localhost:${PORT}/ws`);

// Graceful shutdown handling
process.on('SIGINT', () => {
    console.log('\n🛑 Shutting down server gracefully...');
    server.stop();
    process.exit(0);
});

process.on('SIGTERM', () => {
    console.log('\n🛑 Server terminated');
    server.stop();
    process.exit(0);
});
