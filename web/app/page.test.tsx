import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashConfig } from "../lib/dash/config.js";
import type { Vault } from "../lib/vault/model.js";
import { NotApprovedError } from "../lib/vault/sync.js";
import Home from "./page.js";

// Dummy test-only values — never a real key, token, or loadoutd URL.
const CONFIG: DashConfig = {
  baseUrl: "http://loadoutd.example.test:7777",
  token: "test-token-xyz",
  identity: "AGE-SECRET-KEY-1TESTDUMMYVALUEDONOTUSEXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
  deviceName: "dashboard",
  lastVersion: "",
};

const RECIPIENT =
  "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq";

const FIXTURE_VAULT: Vault = {
  items: [
    {
      address: "skill/widget-fixer",
      kind: "skill",
      hook: "Fixes widgets",
      body: "# Fixing widgets\n\nRun the fixer script.",
      frontmatter: {},
    },
    {
      address: "memory/alpha",
      kind: "memory",
      hook: "Alpha notes",
      body: "alpha body",
      frontmatter: {},
    },
    {
      address: "memory/beta",
      kind: "memory",
      hook: "Beta notes, still draft",
      body: "beta body",
      frontmatter: { review: "draft" },
      review: "draft",
    },
  ],
  secrets: [{ name: "stripe-key", frontmatter: { service: "stripe" } }],
  roster: [],
};

const loadConfigMock = vi.fn<() => DashConfig | null>();
const clearConfigMock = vi.fn();
vi.mock("../lib/dash/config.js", () => ({
  loadConfig: () => loadConfigMock(),
  clearConfig: () => clearConfigMock(),
  saveConfig: vi.fn(),
}));

const sessionFromMock = vi.fn();
vi.mock("../lib/dash/session.js", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/dash/session.js")>(
      "../lib/dash/session.js",
    );
  return {
    ...actual,
    sessionFrom: (cfg: unknown) => sessionFromMock(cfg),
  };
});

const pullMock = vi.fn();
vi.mock("../lib/vault/sync.js", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/vault/sync.js")>(
      "../lib/vault/sync.js",
    );
  return {
    ...actual,
    pull: (session: unknown) => pullMock(session),
  };
});

const recipientForMock = vi.fn();
vi.mock("../lib/vault/age.js", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/vault/age.js")>(
      "../lib/vault/age.js",
    );
  return {
    ...actual,
    recipientFor: (identity: string) => recipientForMock(identity),
  };
});

describe("Home (dashboard shell)", () => {
  beforeEach(() => {
    loadConfigMock.mockReset();
    clearConfigMock.mockReset();
    sessionFromMock.mockReset();
    pullMock.mockReset();
    recipientForMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders ConnectForm when there is no saved config", async () => {
    loadConfigMock.mockReturnValue(null);

    render(<Home />);

    expect(await screen.findByText(/connect to loadoutd/i)).toBeInTheDocument();
  });

  it("renders the workspace after a successful pull, with sections wired to the right items", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: [], version: "v1" });

    render(<Home />);

    // Default section is Skills.
    expect(await screen.findByText("widget-fixer")).toBeInTheDocument();
    expect(screen.queryByText("alpha")).not.toBeInTheDocument();

    // Review count is exactly the one draft item.
    expect(screen.getByRole("button", { name: "Review" })).toHaveTextContent("1");

    // Selecting Memory lists both memory items.
    fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    expect(await screen.findByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("beta")).toBeInTheDocument();
    expect(screen.queryByText("widget-fixer")).not.toBeInTheDocument();

    // Selecting Review lists only the draft item.
    fireEvent.click(screen.getByRole("button", { name: "Review" }));
    expect(await screen.findByText("beta")).toBeInTheDocument();
    expect(screen.queryByText("alpha")).not.toBeInTheDocument();

    // Selecting Secrets lists the secret by name only — no value anywhere.
    fireEvent.click(screen.getByRole("button", { name: "Secrets" }));
    expect(await screen.findByText("stripe-key")).toBeInTheDocument();
    expect(document.body.textContent ?? "").not.toMatch(/value/i);
  });

  it("selecting a skill/memory row shows its ItemDetail", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: [], version: "v1" });

    render(<Home />);
    fireEvent.click(await screen.findByText("widget-fixer"));

    // ItemDetail renders the body as Markdown: "# Fixing widgets" becomes
    // a heading.
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Fixing widgets" }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText(/kept/i)).toBeInTheDocument();
  });

  it("selecting a secret row shows its SecretDetail, metadata only", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: [], version: "v1" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Secrets" }));
    fireEvent.click(await screen.findByText("stripe-key"));

    await waitFor(() =>
      expect(screen.getByText(/cannot be read here/i)).toBeInTheDocument(),
    );
    expect(within(screen.getByRole("table")).getByText("stripe")).toBeInTheDocument();
  });

  it("renders NotApproved when pull throws NotApprovedError", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockRejectedValueOnce(new NotApprovedError("not approved yet"));
    recipientForMock.mockResolvedValueOnce(RECIPIENT);

    render(<Home />);

    expect(await screen.findByText(/not yet approved/i)).toBeInTheDocument();
    expect(screen.getByText(RECIPIENT)).toBeInTheDocument();
  });

  it("renders EmptyVault when the pulled version is empty", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({
      vault: { items: [], secrets: [], roster: [] },
      entries: [],
      version: "",
    });

    render(<Home />);

    expect(await screen.findByText(/the vault is empty/i)).toBeInTheDocument();
  });

  it("renders an error panel with a Reconnect / Settings affordance on any other error", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockRejectedValueOnce(new Error("boom"));

    render(<Home />);

    expect(await screen.findByText(/something went wrong/i)).toBeInTheDocument();
    const reconnectButton = screen.getByRole("button", { name: /reconnect.*settings/i });
    fireEvent.click(reconnectButton);
    expect(clearConfigMock).toHaveBeenCalledTimes(1);
  });
});
