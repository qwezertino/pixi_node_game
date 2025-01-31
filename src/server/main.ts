// import { handleRequest } from "./handlers/requestHandler";
import { handleWebSocket } from "./handlers/websocketHandler";

const PORT: number = 8108;

// Use Bun.serve directly (it's available globally)
const server = Bun.serve({
    port: PORT,
    fetch(req) {
        const url = new URL(req.url);
        if (url.pathname === "/") {
            return new Response(Bun.file("dist/index.html"), {
                headers: { "Content-Type": "text/html" },
            });
        }

        if (url.pathname === "/ws" && server.upgrade(req)) return;
        return new Response(Bun.file("dist" + url.pathname));
      },
    websocket: handleWebSocket(),
});
//
console.log(`Server running on http://localhost:${PORT}`, server);
