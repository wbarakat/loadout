import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { generateKeypair } from "../lib/vault/age.js";
import type { Vault } from "../lib/vault/model.js";
import { NotApprovedError } from "../lib/vault/sync.js";
import { ConnectForm } from "./ConnectForm.js";

// This device's key never decrypts a secret value; it is a "no-secrets"
// browser key. See lib/dash/config.ts for why storing it is an accepted
// tradeoff. These are dummy test values, never a real key or token.
const FAKE_DEVICE = {
  identity: "AGE-SECRET-KEY-1TESTDUMMYVALUEDONOTUSEXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
  recipient: "age1testfakerecipientdonotusexxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
};

const EMPTY_VAULT: Vault = { items: [], secrets: [], roster: [] };
const TEST_TOKEN = "super-secret-token-xyz";

const saveConfigMock = vi.fn();
vi.mock("../lib/dash/config.js", () => ({
  saveConfig: (cfg: unknown) => saveConfigMock(cfg),
}));

const newDeviceMock = vi.fn();
const registerForApprovalMock = vi.fn();
const sessionFromMock = vi.fn();
// approveCommand itself stays real (it's what we're testing the guard
// around), but wrapped in a spy so a test can assert it is never called
// with a bad device name. Declared via vi.hoisted so it exists before the
// vi.mock factory below runs (that factory executes at import time, ahead
// of any ordinary top-level const in this file).
const approveCommandSpy = vi.hoisted(() => vi.fn<(deviceName: string) => string>());
vi.mock("../lib/dash/session.js", async () => {
  const actual =
    await vi.importActual<typeof import("../lib/dash/session.js")>(
      "../lib/dash/session.js",
    );
  approveCommandSpy.mockImplementation(actual.approveCommand);
  return {
    ...actual,
    newDevice: () => newDeviceMock(),
    registerForApproval: (cfg: unknown, recipient: unknown) =>
      registerForApprovalMock(cfg, recipient),
    sessionFrom: (cfg: unknown) => sessionFromMock(cfg),
    approveCommand: (deviceName: string) => approveCommandSpy(deviceName),
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

function fillConnectionFields(token = "s3cr3t-token"): void {
  fireEvent.change(screen.getByLabelText(/loadoutd url/i), {
    target: { value: "http://loadoutd.example.test:7777" },
  });
  fireEvent.change(screen.getByLabelText(/bearer token/i), {
    target: { value: token },
  });
}

async function generateKey(): Promise<void> {
  newDeviceMock.mockResolvedValueOnce(FAKE_DEVICE);
  fireEvent.click(screen.getByRole("button", { name: /generate key/i }));
  await screen.findByText(FAKE_DEVICE.recipient);
}

describe("ConnectForm", () => {
  beforeEach(() => {
    saveConfigMock.mockReset();
    newDeviceMock.mockReset();
    registerForApprovalMock.mockReset();
    sessionFromMock.mockReset();
    pullMock.mockReset();
    approveCommandSpy.mockClear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows the recipient and the exact approve command after Generate key", async () => {
    render(<ConnectForm onConnected={() => {}} />);
    fillConnectionFields();

    await generateKey();

    expect(screen.getByText(FAKE_DEVICE.recipient)).toBeInTheDocument();
    expect(
      screen.getByText("loadout devices approve dashboard --no-secrets"),
    ).toBeInTheDocument();
  });

  it("imports a pasted identity and derives its recipient", async () => {
    const real = await generateKeypair();
    render(<ConnectForm onConnected={() => {}} />);

    fireEvent.change(screen.getByLabelText(/paste an existing identity/i), {
      target: { value: real.identity },
    });
    fireEvent.click(screen.getByRole("button", { name: /^import identity$/i }));

    await screen.findByText(real.recipient);
  });

  it("shows an inline error for a malformed pasted identity", async () => {
    render(<ConnectForm onConnected={() => {}} />);

    fireEvent.change(screen.getByLabelText(/paste an existing identity/i), {
      target: { value: "not-a-real-identity" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^import identity$/i }));

    await screen.findByText(/not an x25519 age identity/i);
  });

  it("registers, pulls, and fires onConnected on success, having saved the config", async () => {
    const onConnected = vi.fn();
    registerForApprovalMock.mockResolvedValueOnce(undefined);
    sessionFromMock.mockReturnValueOnce({ client: {}, identity: FAKE_DEVICE.identity });
    pullMock.mockResolvedValueOnce({ vault: EMPTY_VAULT, entries: [], version: "v1" });

    render(<ConnectForm onConnected={onConnected} />);
    fillConnectionFields();
    await generateKey();

    fireEvent.click(screen.getByRole("button", { name: /register \+ connect/i }));

    await waitFor(() => expect(onConnected).toHaveBeenCalledTimes(1));
    // The FULL pulled result — vault, entries, AND version — is forwarded,
    // not just {vault, version}: the page needs `entries` to make an Edit
    // or Keep work in this same session (see page.tsx's `handleConnected`).
    expect(onConnected).toHaveBeenCalledWith({ vault: EMPTY_VAULT, entries: [], version: "v1" });

    expect(registerForApprovalMock).toHaveBeenCalledWith(
      expect.objectContaining({
        baseUrl: "http://loadoutd.example.test:7777",
        token: "s3cr3t-token",
        deviceName: "dashboard",
        identity: FAKE_DEVICE.identity,
      }),
      FAKE_DEVICE.recipient,
    );
    expect(saveConfigMock).toHaveBeenCalledWith(
      expect.objectContaining({
        baseUrl: "http://loadoutd.example.test:7777",
        token: "s3cr3t-token",
        deviceName: "dashboard",
        identity: FAKE_DEVICE.identity,
      }),
    );
  });

  it("renders NotApproved (with the approve command) when pull throws NotApprovedError, and Retry re-invokes pull", async () => {
    const onConnected = vi.fn();
    registerForApprovalMock.mockResolvedValue(undefined);
    sessionFromMock.mockReturnValue({ client: {}, identity: FAKE_DEVICE.identity });
    pullMock.mockRejectedValueOnce(new NotApprovedError("not approved yet"));

    render(<ConnectForm onConnected={onConnected} />);
    fillConnectionFields();
    await generateKey();
    fireEvent.click(screen.getByRole("button", { name: /register \+ connect/i }));

    await screen.findByText(/not yet approved/i);
    expect(
      screen.getByText("loadout devices approve dashboard --no-secrets"),
    ).toBeInTheDocument();
    expect(pullMock).toHaveBeenCalledTimes(1);

    pullMock.mockResolvedValueOnce({ vault: EMPTY_VAULT, entries: [], version: "v1" });
    fireEvent.click(screen.getByRole("button", { name: /retry connection/i }));

    await waitFor(() => expect(pullMock).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(onConnected).toHaveBeenCalledTimes(1));
  });

  it("shows a fix-naming message on a generic connect error", async () => {
    registerForApprovalMock.mockResolvedValueOnce(undefined);
    sessionFromMock.mockReturnValueOnce({ client: {}, identity: FAKE_DEVICE.identity });
    pullMock.mockRejectedValueOnce(new Error("fetch failed"));

    render(<ConnectForm onConnected={() => {}} />);
    fillConnectionFields();
    await generateKey();
    fireEvent.click(screen.getByRole("button", { name: /register \+ connect/i }));

    await screen.findByText(/could not reach loadoutd/i);
    expect(screen.getByText(/check the url and token/i)).toBeInTheDocument();
    expect(screen.getByText(/cors-origin/i)).toBeInTheDocument();
  });

  it("shows the same fix-naming message when registerForApproval itself fails", async () => {
    registerForApprovalMock.mockRejectedValueOnce(new Error("network down"));

    render(<ConnectForm onConnected={() => {}} />);
    fillConnectionFields();
    await generateKey();
    fireEvent.click(screen.getByRole("button", { name: /register \+ connect/i }));

    await screen.findByText(/could not reach loadoutd/i);
    expect(pullMock).not.toHaveBeenCalled();
  });

  it("keeps the token in a password input, never in a link href or as visible text", async () => {
    render(<ConnectForm onConnected={() => {}} />);
    const tokenInput = screen.getByLabelText(/bearer token/i) as HTMLInputElement;
    expect(tokenInput.type).toBe("password");

    fillConnectionFields(TEST_TOKEN);
    await generateKey();

    for (const link of Array.from(document.querySelectorAll("a[href]"))) {
      expect(link.getAttribute("href")).not.toContain(TEST_TOKEN);
    }
    // <input> values are not part of textContent, so this also proves the
    // token was never copied out into a visible text node.
    expect(document.body.textContent ?? "").not.toContain(TEST_TOKEN);
  });

  it("rejects a device name with an internal space instead of crashing", async () => {
    render(<ConnectForm onConnected={() => {}} />);
    fillConnectionFields();
    await generateKey();
    approveCommandSpy.mockClear(); // clear the call made for the default "dashboard" name

    // This must not throw: approveCommand("front desk") throws on internal
    // whitespace, and the component must never call it with this value.
    expect(() => {
      fireEvent.change(screen.getByLabelText(/device name/i), {
        target: { value: "front desk" },
      });
    }).not.toThrow();

    await screen.findByText(/can contain only/i);
    expect(screen.getByRole("button", { name: /register \+ connect/i })).toBeDisabled();
    expect(
      approveCommandSpy.mock.calls.some(([name]) => name === "front desk"),
    ).toBe(false);
  });
});
