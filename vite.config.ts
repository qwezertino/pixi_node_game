import { defineConfig } from "vite";

// https://vite.dev/config/
export default defineConfig({
  server: {
    port: 8109,
    open: true,
    proxy: {
      "/ws": {
        target: "ws://127.0.0.1:8108",
        ws: true,
      },
    },
  },
});
