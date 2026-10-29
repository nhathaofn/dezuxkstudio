import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    // Pre-transform the entry graph while Wails compiles Go, so the first window paint is fast.
    warmup: { clientFiles: ["./src/main.tsx", "./src/app/**/*.tsx"] },
  },
  build: {
    // Wails embeds frontend/dist; keep it deterministic.
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
