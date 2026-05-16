import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

// Frontend lives in `web/`; the built output is dropped into
// `pkg/dashboard/dist/` so the Go server can pick it up via go:embed.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: resolve(__dirname, "../pkg/dashboard/dist"),
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:3300",
    },
  },
});
