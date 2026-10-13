import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// During development the dashboard runs on Vite (port 5173) and proxies API
// and capture requests to a Relaybox server on port 8787.
const backend = process.env.RELAYBOX_DEV_BACKEND ?? "http://127.0.0.1:8787";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // The Go binary embeds this directory (internal/webui).
    outDir: "../internal/webui/static/dist",
    emptyOutDir: true,
    assetsDir: "assets",
    target: "es2022",
    modulePreload: { polyfill: false },
    chunkSizeWarningLimit: 800,
  },
  server: {
    host: "localhost",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: backend },
      "/hooks": { target: backend },
      "/healthz": { target: backend },
      "/readyz": { target: backend },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    restoreMocks: true,
  },
});
