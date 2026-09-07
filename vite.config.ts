import { defineConfig } from "vite";

// https://vite.dev/config/
export default defineConfig({
  build: {
    // gameConfig.ts/units.ts use top-level await to fetch config from the
    // server at startup (see src/shared) — needs a target that supports it.
    target: "es2022",
  },
  server: {
    port: 8109,
    open: true,
    proxy: {
      "/ws": {
        target: "ws://127.0.0.1:8108",
        ws: true,
      },
      "/api": {
        target: "http://127.0.0.1:8108",
      },
    },
  },
});
