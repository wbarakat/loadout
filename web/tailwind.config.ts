import type { Config } from "tailwindcss";

// Scans the App Router pages and the shared components directory for
// class names. Both are the only source of dashboard markup.
const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {},
  },
  plugins: [],
};

export default config;
