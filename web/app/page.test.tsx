import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashConfig } from "../lib/dash/config.js";
import { withReviewKept } from "../lib/dash/review.js";
import type { Vault } from "../lib/vault/model.js";
import type { TarEntry } from "../lib/vault/tar.js";
import { NotApprovedError, SyncConflictError } from "../lib/vault/sync.js";
import Home from "./page.js";

function fileEntry(name: string, text: string): TarEntry {
  return { name, type: "file", mode: 0o644, bytes: new TextEncoder().encode(text) };
}

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

// Raw file text for each `FIXTURE_VAULT` item, as `pull` would return it in
// `entries` — the exact bytes `rawFileFor` (`../lib/dash/review.js`) reads
// for an Edit/Keep. `memory/beta`'s raw file carries a real frontmatter
// block (`review: draft`) so `withReviewKept` has something to splice.
const RAW_WIDGET_FIXER = "# Fixing widgets\n\nRun the fixer script.";
const RAW_ALPHA = "alpha body";
const RAW_BETA = "---\nreview: draft\n---\nbeta body";

const FIXTURE_ENTRIES: TarEntry[] = [
  fileEntry("skills/widget-fixer/SKILL.md", RAW_WIDGET_FIXER),
  fileEntry("memory/alpha.md", RAW_ALPHA),
  fileEntry("memory/beta.md", RAW_BETA),
];

const loadConfigMock = vi.fn<() => DashConfig | null>();
const clearConfigMock = vi.fn();
const setLastVersionMock = vi.fn();
vi.mock("../lib/dash/config.js", () => ({
  loadConfig: () => loadConfigMock(),
  clearConfig: () => clearConfigMock(),
  saveConfig: vi.fn(),
  setLastVersion: (v: string) => setLastVersionMock(v),
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
const commitEditMock = vi.fn();
vi.mock("../lib/vault/sync.js", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/vault/sync.js")>(
      "../lib/vault/sync.js",
    );
  return {
    ...actual,
    pull: (session: unknown) => pullMock(session),
    commitEdit: (session: unknown, address: string, newBody: string) =>
      commitEditMock(session, address, newBody),
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
    setLastVersionMock.mockReset();
    sessionFromMock.mockReset();
    pullMock.mockReset();
    commitEditMock.mockReset();
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
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });

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
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });

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
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });

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

  it("editing a memory: Save calls commitEdit with the full new file content, then re-pulls and shows the update", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });
    commitEditMock.mockResolvedValueOnce("v2");

    const updatedVault: Vault = {
      ...FIXTURE_VAULT,
      items: FIXTURE_VAULT.items.map((item) =>
        item.address === "memory/alpha" ? { ...item, body: "alpha body v2" } : item,
      ),
    };
    pullMock.mockResolvedValueOnce({ vault: updatedVault, entries: [], version: "v2" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Memory" }));
    fireEvent.click(await screen.findByText("alpha"));
    fireEvent.click(await screen.findByRole("button", { name: /edit/i }));

    const textarea = await screen.findByRole("textbox");
    expect(textarea).toHaveValue("alpha body");
    fireEvent.change(textarea, { target: { value: "alpha body v2" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(commitEditMock).toHaveBeenCalledTimes(1));
    // memory/alpha has no frontmatter, so the full file content is just the
    // edited prose, unchanged.
    expect(commitEditMock).toHaveBeenCalledWith(
      expect.anything(),
      "memory/alpha",
      "alpha body v2",
    );

    await waitFor(() => expect(pullMock).toHaveBeenCalledTimes(2));
    expect(setLastVersionMock).toHaveBeenCalledWith("v2");
    await waitFor(() =>
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument(),
    );
    expect(await screen.findByText("alpha body v2")).toBeInTheDocument();
  });

  it("a SyncConflictError from commitEdit shows the reload message and re-pulls", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });
    commitEditMock.mockRejectedValueOnce(new SyncConflictError("conflict"));
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Memory" }));
    fireEvent.click(await screen.findByText("alpha"));
    fireEvent.click(await screen.findByRole("button", { name: /edit/i }));

    const textarea = await screen.findByRole("textbox");
    fireEvent.change(textarea, { target: { value: "a local edit, about to conflict" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    expect(
      await screen.findByText(/the vault changed on another device\. reloading the latest\./i),
    ).toBeInTheDocument();
    await waitFor(() => expect(pullMock).toHaveBeenCalledTimes(2));
    // The editor closes; no data is silently applied on top of stale state.
    await waitFor(() =>
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument(),
    );
  });

  it("Keep from ItemDetail calls commitEdit with withReviewKept(rawFile) and re-pulls", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });
    commitEditMock.mockResolvedValueOnce("v2");

    const keptVault: Vault = {
      ...FIXTURE_VAULT,
      items: FIXTURE_VAULT.items.map((item) =>
        item.address === "memory/beta"
          ? { ...item, review: "kept", frontmatter: { review: "kept" } }
          : item,
      ),
    };
    pullMock.mockResolvedValueOnce({ vault: keptVault, entries: [], version: "v2" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Memory" }));
    fireEvent.click(await screen.findByText("beta"));
    fireEvent.click(await screen.findByRole("button", { name: /keep/i }));

    await waitFor(() => expect(commitEditMock).toHaveBeenCalledTimes(1));
    // Computed via the real, non-mocked `withReviewKept`, spliced from the
    // exact raw bytes `pull` returned in `entries` — not a hand-written
    // string, and not reserialized from the parsed `Item.frontmatter` map.
    expect(commitEditMock).toHaveBeenCalledWith(
      expect.anything(),
      "memory/beta",
      withReviewKept(RAW_BETA),
    );
    await waitFor(() => expect(pullMock).toHaveBeenCalledTimes(2));
  });

  it("Keep from ReviewQueue calls commitEdit and the kept item leaves the queue", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });
    commitEditMock.mockResolvedValueOnce("v2");

    const keptVault: Vault = {
      ...FIXTURE_VAULT,
      items: FIXTURE_VAULT.items.map((item) =>
        item.address === "memory/beta"
          ? { ...item, review: "kept", frontmatter: { review: "kept" } }
          : item,
      ),
    };
    pullMock.mockResolvedValueOnce({ vault: keptVault, entries: [], version: "v2" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Review" }));
    expect(await screen.findByText("beta")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /keep/i }));

    await waitFor(() => expect(commitEditMock).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(pullMock).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByText("beta")).not.toBeInTheDocument());
  });

  it("offers no Edit or Keep control for a secret, and never calls commitEdit for one", async () => {
    loadConfigMock.mockReturnValue(CONFIG);
    sessionFromMock.mockReturnValue({ client: {}, identity: CONFIG.identity });
    pullMock.mockResolvedValueOnce({ vault: FIXTURE_VAULT, entries: FIXTURE_ENTRIES, version: "v1" });

    render(<Home />);
    fireEvent.click(await screen.findByRole("button", { name: "Secrets" }));
    fireEvent.click(await screen.findByText("stripe-key"));

    await waitFor(() =>
      expect(screen.getByText(/cannot be read here/i)).toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: /edit/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /keep/i })).not.toBeInTheDocument();
    expect(commitEditMock).not.toHaveBeenCalled();
  });
});
