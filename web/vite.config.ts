/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The Admin SPA is served same-origin by the issuer in production (relative API
// paths). In dev, proxy the issuer API + JWKS to a local `make dev` on :8080.
const ISSUER = process.env.VITE_ISSUER_ORIGIN || "http://localhost:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": resolve(import.meta.dirname, "src") },
  },
  server: {
    proxy: {
      "/api": { target: ISSUER, changeOrigin: true },
      "/.well-known": { target: ISSUER, changeOrigin: true },
    },
  },
  build: { outDir: "dist", sourcemap: false },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
});
