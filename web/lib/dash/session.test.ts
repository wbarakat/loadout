import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { recipientFor } from "../vault/age.js";
import type { DashConfig } from "./config.js";
import {
  approveCommand,
  newDevice,
  registerForApproval,
  sessionFrom,
} from "./session.js";

const BASE_URL = "http://loadoutd.example.test:7777";
const TOKEN = "test-bearer-token-do-not-use";

const CONFIG: DashConfig = {
  baseUrl: BASE_URL,
  token: TOKEN,
  identity:
    "AGE-SECRET-KEY-1TESTDUMMYVALUEDONOTUSEXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
  deviceName: "test-browser",
  lastVersion: "v0",
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("sessionFrom", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("builds a Session whose client sends requests to the config baseUrl with the config token", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { version: "", parent: "" }),
    );

    const session = sessionFrom(CONFIG);
    await session.client.getLatest();

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`${BASE_URL}/v1/snapshots/latest`);
    expect(new Headers(init.headers).get("Authorization")).toBe(
      `Bearer ${TOKEN}`,
    );
  });

  it("carries the config identity through unchanged", () => {
    const session = sessionFrom(CONFIG);
    expect(session.identity).toBe(CONFIG.identity);
  });
});

describe("newDevice", () => {
  it("returns a real AGE-SECRET-KEY-1... identity paired with its age1... recipient", async () => {
    const device = await newDevice();

    expect(device.identity).toMatch(
      /^AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}$/,
    );
    expect(device.recipient).toMatch(
      /^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$/,
    );
    await expect(recipientFor(device.identity)).resolves.toBe(
      device.recipient,
    );
  });

  it("returns a fresh pair on every call", async () => {
    const first = await newDevice();
    const second = await newDevice();

    expect(first.identity).not.toBe(second.identity);
  });
});

describe("approveCommand", () => {
  it("returns the exact CLI approve command for a given device name", () => {
    expect(approveCommand("dashboard")).toBe(
      "loadout devices approve dashboard --no-secrets",
    );
  });

  it("defaults to dashboard when the name is empty", () => {
    expect(approveCommand("")).toBe(
      "loadout devices approve dashboard --no-secrets",
    );
  });

  it("defaults to dashboard when the name is blank (whitespace only)", () => {
    expect(approveCommand("   ")).toBe(
      "loadout devices approve dashboard --no-secrets",
    );
  });

  it("trims surrounding whitespace from a real name", () => {
    expect(approveCommand("  my-laptop  ")).toBe(
      "loadout devices approve my-laptop --no-secrets",
    );
  });

  it("rejects a name with internal whitespace instead of emitting a broken command", () => {
    expect(() => approveCommand("my device")).toThrow(/whitespace/);
  });
});

describe("registerForApproval", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("POSTs the device name and recipient to /v1/devices with the bearer token", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { name: CONFIG.deviceName, recipient: "age1qqqtest" }),
    );

    await registerForApproval(CONFIG, "age1qqqtest");

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(`${BASE_URL}/v1/devices`);
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe(`Bearer ${TOKEN}`);
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(JSON.parse(init.body as string)).toEqual({
      name: CONFIG.deviceName,
      recipient: "age1qqqtest",
    });
  });
});
