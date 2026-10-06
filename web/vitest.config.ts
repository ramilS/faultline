import { defineConfig, mergeConfig } from "vitest/config";

import viteConfig from "./vite.config";

/**
 * Separate from `vite.config.ts` so the production build never has to resolve
 * `vitest/config`: the image installs dev dependencies today, but a build that
 * fails without a test runner installed is a coupling worth not having.
 *
 * The Vite config is merged rather than restated — the `@` alias and the React
 * transform have to match the ones the app is built with, or a passing test
 * proves nothing about the shipped bundle.
 *
 * `.tsx` only. The `.ts` suites use `node:test` APIs and run under
 * `npm run test:logic`; Vitest neither provides those APIs nor needs to.
 */
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: "jsdom",
      include: ["src/**/*.test.tsx"],
      setupFiles: ["./src/test/setup.ts"]
    }
  })
);
