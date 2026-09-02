// Setup file for the "dash" Vitest project only (see vitest.config.ts).
// Extends `expect` with jest-dom's DOM matchers (toBeInTheDocument,
// toHaveTextContent, and so on) for React Testing Library component tests.
import "@testing-library/jest-dom/vitest";

// This project does not turn on Vitest's `test.globals`, so React Testing
// Library cannot find a global `afterEach` to auto-register its cleanup.
// Without this, a `render` from one test stays in the DOM for the next
// test in the same file. Register it here once for every component test.
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});
