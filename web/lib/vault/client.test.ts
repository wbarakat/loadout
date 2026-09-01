import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConflictError, LoadoutdClient } from "./client.js";

const TOKEN = "test-bearer-token-do-not-use";
const BASE_URL = "http://loadoutd.example.test:7777";

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function octetResponse(status: number, bytes: Uint8Array): Response {
  // Cast: see the matching comment in client.ts on postSnapshot's fetch call.
  return new Response(bytes as BodyInit, {
    status,
    headers: { "Content-Type": "application/octet-stream" },
  });
}

describe("LoadoutdClient", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  function client(): LoadoutdClient {
    return new LoadoutdClient({ baseUrl: BASE_URL, token: TOKEN });
  }

  describe("getLatest", () => {
    it("parses {version, parent} from GET /v1/snapshots/latest", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(200, { version: "v3-deadbeef", parent: "v2-abcd1234" }),
      );

      const info = await client().getLatest();

      expect(info).toEqual({ version: "v3-deadbeef", parent: "v2-abcd1234" });
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/snapshots/latest`);
      expect(new Headers(init.headers).get("Authorization")).toBe(
        `Bearer ${TOKEN}`,
      );
      expect(url).not.toContain(TOKEN);
    });

    it("returns empty strings when the store is empty", async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "", parent: "" }));

      const info = await client().getLatest();

      expect(info).toEqual({ version: "", parent: "" });
    });
  });

  describe("getSnapshot", () => {
    it("returns the raw blob bytes and sends the bearer header", async () => {
      const blob = new Uint8Array([1, 2, 3, 4, 250, 251]);
      fetchMock.mockResolvedValueOnce(octetResponse(200, blob));

      const result = await client().getSnapshot("v3-deadbeef");

      expect(result).toEqual(blob);
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/snapshots/v3-deadbeef`);
      expect(new Headers(init.headers).get("Authorization")).toBe(
        `Bearer ${TOKEN}`,
      );
      expect(url).not.toContain(TOKEN);
    });

    it("throws a clear Error on 404", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(404, { error: "no such snapshot version" }),
      );

      await expect(client().getSnapshot("v99-ffffffff")).rejects.toThrow(
        /no such snapshot version|404/,
      );
    });
  });

  describe("postSnapshot", () => {
    it("sends X-Loadout-Parent, octet-stream, and the blob; returns the new version on 200", async () => {
      const blob = new Uint8Array([9, 8, 7]);
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "v4-11112222" }));

      const version = await client().postSnapshot(blob, "v3-deadbeef");

      expect(version).toBe("v4-11112222");
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/snapshots`);
      const headers = new Headers(init.headers);
      expect(headers.get("Authorization")).toBe(`Bearer ${TOKEN}`);
      expect(headers.get("X-Loadout-Parent")).toBe("v3-deadbeef");
      expect(headers.get("Content-Type")).toBe("application/octet-stream");
      expect(new Uint8Array(init.body as ArrayBuffer)).toEqual(blob);
      expect(url).not.toContain(TOKEN);
    });

    it("sends an empty X-Loadout-Parent header for a brand-new store", async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "v1-aaaa0000" }));

      await client().postSnapshot(new Uint8Array([1]), "");

      const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      const headers = new Headers(init.headers);
      expect(headers.get("X-Loadout-Parent")).toBe("");
      expect(headers.has("X-Loadout-Parent")).toBe(true);
    });

    it("throws ConflictError with .latest set on 409", async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(409, { latest: "v9-cafebabe" }));

      let caught: unknown;
      try {
        await client().postSnapshot(new Uint8Array([1]), "v3-deadbeef");
      } catch (err) {
        caught = err;
      }

      expect(caught).toBeInstanceOf(ConflictError);
      expect((caught as ConflictError).latest).toBe("v9-cafebabe");
    });

    it("throws an Error naming the status and server message on 413", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(413, { error: "the snapshot exceeds the maximum allowed size" }),
      );

      await expect(
        client().postSnapshot(new Uint8Array([1]), "v3-deadbeef"),
      ).rejects.toThrow(/413|the snapshot exceeds the maximum allowed size/);
    });

    it("throws an Error naming the status and server message on 400", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(400, { error: "the request body cannot be read" }),
      );

      await expect(
        client().postSnapshot(new Uint8Array([1]), "v3-deadbeef"),
      ).rejects.toThrow(/400|the request body cannot be read/);
    });

    it("throws an Error naming the status on 500", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(500, { error: "the snapshot cannot be stored" }),
      );

      await expect(
        client().postSnapshot(new Uint8Array([1]), "v3-deadbeef"),
      ).rejects.toThrow(/500|the snapshot cannot be stored/);
    });
  });

  describe("registerDevice", () => {
    it("POSTs {name, recipient} as JSON with the bearer header", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(200, { name: "dashboard", recipient: "age1qqqtest" }),
      );

      await client().registerDevice("dashboard", "age1qqqtest");

      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/devices`);
      const headers = new Headers(init.headers);
      expect(headers.get("Authorization")).toBe(`Bearer ${TOKEN}`);
      expect(headers.get("Content-Type")).toBe("application/json");
      expect(JSON.parse(init.body as string)).toEqual({
        name: "dashboard",
        recipient: "age1qqqtest",
      });
      expect(url).not.toContain(TOKEN);
    });

    it("throws an Error carrying the server message on 400", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(400, { error: "name and recipient are required" }),
      );

      await expect(client().registerDevice("", "")).rejects.toThrow(
        /400|name and recipient are required/,
      );
    });

    it("throws an Error carrying the server message on 413", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(413, { error: "the request body exceeds the maximum allowed size" }),
      );

      await expect(
        client().registerDevice("dashboard", "age1qqqtest"),
      ).rejects.toThrow(/413|the request body exceeds the maximum allowed size/);
    });
  });

  describe("listDevices", () => {
    it("parses {devices:[...]} into an array", async () => {
      fetchMock.mockResolvedValueOnce(
        jsonResponse(200, {
          devices: [
            { name: "dashboard", recipient: "age1qqqtest" },
            { name: "mac", recipient: "age1zzztest" },
          ],
        }),
      );

      const devices = await client().listDevices();

      expect(devices).toEqual([
        { name: "dashboard", recipient: "age1qqqtest" },
        { name: "mac", recipient: "age1zzztest" },
      ]);
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/devices`);
      expect(new Headers(init.headers).get("Authorization")).toBe(
        `Bearer ${TOKEN}`,
      );
      expect(url).not.toContain(TOKEN);
    });
  });

  describe("baseUrl normalization", () => {
    it("avoids a double slash when baseUrl ends with a slash", async () => {
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "", parent: "" }));

      const c = new LoadoutdClient({ baseUrl: `${BASE_URL}/`, token: TOKEN });
      await c.getLatest();

      const [url] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toBe(`${BASE_URL}/v1/snapshots/latest`);
    });
  });

  describe("token never appears in a URL, always in the Authorization header", () => {
    it("holds across every /v1/* call this client makes", async () => {
      fetchMock.mockResolvedValue(
        jsonResponse(200, { version: "", parent: "" }),
      );
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "", parent: "" }));
      fetchMock.mockResolvedValueOnce(octetResponse(200, new Uint8Array([1])));
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { version: "v1-00000000" }));
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { name: "d", recipient: "age1qqqtest" }));
      fetchMock.mockResolvedValueOnce(jsonResponse(200, { devices: [] }));

      const c = client();
      await c.getLatest();
      await c.getSnapshot("v1-00000000");
      await c.postSnapshot(new Uint8Array([1]), "");
      await c.registerDevice("d", "age1qqqtest");
      await c.listDevices();

      expect(fetchMock).toHaveBeenCalledTimes(5);
      for (const call of fetchMock.mock.calls) {
        const [url, init] = call as [string, RequestInit];
        expect(url).not.toContain(TOKEN);
        expect(new Headers(init.headers).get("Authorization")).toBe(
          `Bearer ${TOKEN}`,
        );
      }
    });
  });
});
