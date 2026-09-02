import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SecretMeta } from "../lib/vault/model.js";
import { SecretDetail } from "./SecretDetail.js";

// A sentinel that stands in for a decrypted secret value. It is NOT part
// of any SecretMeta below, and NEVER a valid frontmatter value. If this
// string ever shows up in the rendered DOM, a secret value has leaked.
const FORBIDDEN_VALUE = "sk-live-forbidden-secret-value-must-never-render";

const SECRET: SecretMeta = {
  name: "stripe-api-key",
  frontmatter: {
    service: "stripe",
    hook: "Stripe API key for billing",
    rotate_after: "90d",
    allowed_hosts: "api.stripe.com",
    by: "waleed",
    at: "2026-08-30",
  },
};

describe("SecretDetail", () => {
  it("renders the secret name", () => {
    render(<SecretDetail secret={SECRET} />);
    expect(screen.getByText("stripe-api-key")).toBeInTheDocument();
  });

  it("renders every present frontmatter field as a label/value row", () => {
    render(<SecretDetail secret={SECRET} />);

    expect(screen.getByText("stripe")).toBeInTheDocument();
    expect(screen.getByText("Stripe API key for billing")).toBeInTheDocument();
    expect(screen.getByText("90d")).toBeInTheDocument();
    expect(screen.getByText("api.stripe.com")).toBeInTheDocument();
    expect(screen.getByText("waleed")).toBeInTheDocument();
    expect(screen.getByText("2026-08-30")).toBeInTheDocument();
  });

  it("renders any other frontmatter key not on the known list", () => {
    const secret: SecretMeta = {
      name: "custom-secret",
      frontmatter: { custom_field: "custom-value" },
    };
    render(<SecretDetail secret={secret} />);
    expect(screen.getByText("custom_field")).toBeInTheDocument();
    expect(screen.getByText("custom-value")).toBeInTheDocument();
  });

  it("shows the not-readable notice", () => {
    render(<SecretDetail secret={SECRET} />);
    expect(
      screen.getByText(
        /the value is stored encrypted and cannot be read here/i,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText(/loadout secret show/i)).toBeInTheDocument();
  });

  it("CRITICAL: never renders a secret value, and has no reveal/show-value control", () => {
    const { container } = render(<SecretDetail secret={SECRET} />);

    // No sentinel value ever appears — proves the component does not
    // fabricate, fetch, or otherwise surface a decrypted value.
    expect(container.textContent).not.toContain(FORBIDDEN_VALUE);

    // No control exists that could reveal or fetch a value.
    expect(screen.queryByRole("button", { name: /reveal/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /show value/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^show$/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/value\.age/i)).not.toBeInTheDocument();

    // No field/label named "value" appears anywhere in the metadata table.
    expect(screen.queryByText(/^value$/i)).not.toBeInTheDocument();
  });

  it("renders no metadata table rows beyond what frontmatter provides", () => {
    const secret: SecretMeta = { name: "bare-secret", frontmatter: {} };
    render(<SecretDetail secret={secret} />);
    expect(screen.getByText("bare-secret")).toBeInTheDocument();
    expect(
      screen.getByText(/the value is stored encrypted and cannot be read here/i),
    ).toBeInTheDocument();
  });
});
