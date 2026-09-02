import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Vitest 4 runs two projects from one config (the successor to the older,
// now-removed `environmentMatchGlobs` option):
//   - "lib": the Part 1 vault-client library. Node environment, unchanged.
//     These 71 tests must keep passing exactly as they did before Part 2.
//   - "dash": the dashboard's browser-config store and React components.
//     jsdom environment, with React Testing Library and jest-dom matchers
//     wired in through a setup file.
export default defineConfig({
  test: {
    projects: [
      {
        extends: true,
        test: {
          name: "lib",
          environment: "node",
          include: ["lib/vault/**/*.test.ts"],
        },
      },
      {
        extends: true,
        plugins: [react()],
        test: {
          name: "dash",
          environment: "jsdom",
          include: ["lib/dash/**/*.test.ts", "**/*.test.tsx"],
          setupFiles: ["./vitest.setup.ts"],
        },
      },
    ],
  },
});
