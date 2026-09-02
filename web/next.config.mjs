/**
 * Static export config. Loadout's dashboard has no server data path: it
 * talks only to the user's own `loadoutd`, straight from the browser. The
 * built site is plain static HTML/CSS/JS with no Next.js server at all.
 *
 * @type {import("next").NextConfig}
 */
const nextConfig = {
  output: "export",
  // The vault/dash library source uses explicit ".js" specifiers on
  // relative imports of ".ts"/".tsx" files (Node ESM style, needed by
  // vitest/tsc's own module resolution). Webpack needs this alias to find
  // the real source file for such a specifier.
  experimental: {
    extensionAlias: {
      ".js": [".ts", ".tsx", ".js"],
    },
  },
};

export default nextConfig;
