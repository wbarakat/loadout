/**
 * Minimal, hand-written ambient types for the handful of Node.js builtins
 * interop.test.ts needs to read/write test fixtures from disk.
 *
 * This project has no `@types/node` dependency (and none should be added
 * just for one test file, per the "no new TS deps" constraint) — these
 * declarations cover only the exact functions used, nothing more. Vitest
 * itself runs on Node, so the real implementations exist at test-run time;
 * this file only satisfies `tsc --noEmit`.
 */
declare module "node:fs" {
  export function existsSync(path: string): boolean;
  export function readFileSync(path: string): Uint8Array;
  export function readFileSync(path: string, encoding: "utf-8" | "utf8"): string;
  export function writeFileSync(path: string, data: Uint8Array): void;
}

declare module "node:path" {
  export function dirname(path: string): string;
  export function join(...segments: string[]): string;
}

declare module "node:url" {
  export function fileURLToPath(url: string): string;
}
