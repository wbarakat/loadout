/**
 * Static export config. Loadout's dashboard has no server data path: it
 * talks only to the user's own `loadoutd`, straight from the browser. The
 * built site is plain static HTML/CSS/JS with no Next.js server at all.
 *
 * @type {import("next").NextConfig}
 */
const nextConfig = {
  output: "export",
};

export default nextConfig;
