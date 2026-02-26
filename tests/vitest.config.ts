import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    testTimeout: 30_000,
    hookTimeout: 960_000,
    globalSetup: "./agent/global-setup.ts",
  },
});
