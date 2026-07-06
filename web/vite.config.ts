import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Build to dist/ (embedded by the Go binary). Relative base so assets resolve
// regardless of the path the SPA is served from.
export default defineConfig({
  plugins: [svelte()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8022",
        ws: true,
      },
    },
  },
});
