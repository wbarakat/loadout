/**
 * A typed wrapper over the `loadoutd` HTTP API.
 *
 * This is the ONLY module in the vault library that calls `fetch` against
 * `loadoutd`. It matches the Go server's wire shapes exactly (see
 * `internal/server/api.go`, `internal/server/store.go`, and the interop
 * contract §1/§2): the bearer token on every `/v1/*` request, the
 * `X-Loadout-Parent` push header, and the `409` conflict body.
 *
 * The bearer token is a long-lived shared secret (see contract §2). This
 * module puts it only in the `Authorization` header, never in a URL or a
 * log line.
 */

/** Config for one `loadoutd` server: its base URL and bearer token. */
export interface LoadoutdConfig {
  /** The `loadoutd` base URL, with or without a trailing slash. */
  baseUrl: string;
  /** The bearer token. Held in the browser; never sent except as a header. */
  token: string;
}

/** The answer to `GET /v1/snapshots/latest`. Both fields are `""` when the
 * store has never received a snapshot. */
export interface LatestInfo {
  version: string;
  parent: string;
}

/** One entry of the bootstrap device roster (`GET`/`POST /v1/devices`). */
export interface DeviceInfo {
  name: string;
  recipient: string;
}

/**
 * Thrown when `postSnapshot`'s `parent` no longer matches the server's
 * current latest version (HTTP 409). `latest` is the server's current
 * version; the caller must pull it, merge, and retry with that version as
 * the new parent.
 */
export class ConflictError extends Error {
  latest: string;

  constructor(latest: string) {
    super(`the store's latest version is now ${JSON.stringify(latest)}`);
    this.name = "ConflictError";
    this.latest = latest;
  }
}

/** Strips a single trailing slash so `${baseUrl}/v1/...` never doubles it. */
function stripTrailingSlash(baseUrl: string): string {
  return baseUrl.endsWith("/") ? baseUrl.slice(0, -1) : baseUrl;
}

/**
 * Read a JSON error body's `error` field, if the response has one. Returns
 * `undefined` when the body is not JSON or carries no `error` field, so the
 * caller always has a usable fallback message.
 */
async function readErrorMessage(response: Response): Promise<string | undefined> {
  try {
    const body = (await response.json()) as { error?: unknown };
    return typeof body.error === "string" ? body.error : undefined;
  } catch {
    return undefined;
  }
}

/** Build the Error a non-2xx, non-409 response should throw. */
async function statusError(response: Response, action: string): Promise<Error> {
  const detail = await readErrorMessage(response);
  const suffix = detail ? `: ${detail}` : "";
  return new Error(`${action} failed with status ${response.status}${suffix}`);
}

/**
 * A typed client for one `loadoutd` server. Every method sends
 * `Authorization: Bearer <token>`; the token never appears in a request
 * URL or in any error message this module constructs.
 */
export class LoadoutdClient {
  private readonly baseUrl: string;
  private readonly token: string;

  constructor(cfg: LoadoutdConfig) {
    this.baseUrl = stripTrailingSlash(cfg.baseUrl);
    this.token = cfg.token;
  }

  private authHeaders(): HeadersInit {
    return { Authorization: `Bearer ${this.token}` };
  }

  /** `GET /v1/snapshots/latest` — poll the current head. */
  async getLatest(): Promise<LatestInfo> {
    const response = await fetch(`${this.baseUrl}/v1/snapshots/latest`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    if (!response.ok) {
      throw await statusError(response, "getLatest");
    }
    return (await response.json()) as LatestInfo;
  }

  /**
   * `GET /v1/snapshots/{version}` — fetch one snapshot's raw age blob.
   *
   * @throws an Error naming the status (for example 404 when the version
   * does not exist) with the server's message when it sent one.
   */
  async getSnapshot(version: string): Promise<Uint8Array> {
    const response = await fetch(
      `${this.baseUrl}/v1/snapshots/${encodeURIComponent(version)}`,
      { method: "GET", headers: this.authHeaders() },
    );
    if (!response.ok) {
      throw await statusError(response, "getSnapshot");
    }
    return new Uint8Array(await response.arrayBuffer());
  }

  /**
   * `POST /v1/snapshots` — push a new snapshot blob.
   *
   * @param blob the raw, already-encrypted snapshot bytes.
   * @param parent the version this push was built on, or `""` for a
   * brand-new store. Always sent, even when empty.
   * @returns the new version string on success.
   * @throws {ConflictError} on 409, with `.latest` set to the server's
   * current version.
   * @throws an Error naming the status (413/400/500) otherwise.
   */
  async postSnapshot(blob: Uint8Array, parent: string): Promise<string> {
    const response = await fetch(`${this.baseUrl}/v1/snapshots`, {
      method: "POST",
      headers: {
        ...this.authHeaders(),
        "Content-Type": "application/octet-stream",
        "X-Loadout-Parent": parent,
      },
      // Cast: TS's lib.dom.d.ts BodyInit type and the generic
      // Uint8Array<ArrayBufferLike> lib.es2022 emits do not line up in this
      // TypeScript version, though a Uint8Array is a valid BufferSource at
      // runtime and fetch accepts it unchanged.
      body: blob as BodyInit,
    });
    if (response.status === 409) {
      const body = (await response.json()) as { latest: string };
      throw new ConflictError(body.latest);
    }
    if (!response.ok) {
      throw await statusError(response, "postSnapshot");
    }
    const body = (await response.json()) as { version: string };
    return body.version;
  }

  /**
   * `POST /v1/devices` — register or update this device on the bootstrap
   * roster (the roster `GET /v1/devices` serves; not the vault's trust
   * roster — see interop contract §1/§6).
   *
   * @throws an Error naming the status (400/413) with the server's message.
   */
  async registerDevice(name: string, recipient: string): Promise<void> {
    const response = await fetch(`${this.baseUrl}/v1/devices`, {
      method: "POST",
      headers: {
        ...this.authHeaders(),
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ name, recipient }),
    });
    if (!response.ok) {
      throw await statusError(response, "registerDevice");
    }
  }

  /** `GET /v1/devices` — list the bootstrap roster. */
  async listDevices(): Promise<DeviceInfo[]> {
    const response = await fetch(`${this.baseUrl}/v1/devices`, {
      method: "GET",
      headers: this.authHeaders(),
    });
    if (!response.ok) {
      throw await statusError(response, "listDevices");
    }
    const body = (await response.json()) as { devices: DeviceInfo[] };
    return body.devices;
  }
}
