import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { NotApproved } from "./NotApproved.js";

const RECIPIENT =
  "age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq";

describe("NotApproved", () => {
  it("shows the recipient and the exact approve command", () => {
    render(<NotApproved recipient={RECIPIENT} deviceName="dashboard" onRetry={() => {}} />);

    expect(screen.getByText(RECIPIENT)).toBeInTheDocument();
    expect(
      screen.getByText("loadout devices approve dashboard --no-secrets"),
    ).toBeInTheDocument();
  });

  it("builds the approve command from a custom device name", () => {
    render(<NotApproved recipient={RECIPIENT} deviceName="my-laptop" onRetry={() => {}} />);

    expect(
      screen.getByText("loadout devices approve my-laptop --no-secrets"),
    ).toBeInTheDocument();
  });

  it("calls onRetry when Retry connection is clicked", () => {
    const onRetry = vi.fn();
    render(<NotApproved recipient={RECIPIENT} deviceName="dashboard" onRetry={onRetry} />);

    fireEvent.click(screen.getByRole("button", { name: /retry connection/i }));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
