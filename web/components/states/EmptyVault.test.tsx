import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { EmptyVault } from "./EmptyVault.js";

describe("EmptyVault", () => {
  it("explains the vault is empty because the Mac has not synced yet", () => {
    render(<EmptyVault />);
    expect(screen.getByText(/the vault is empty/i)).toBeInTheDocument();
    expect(screen.getByText(/has not synced/i)).toBeInTheDocument();
  });

  it("calls onRetry when Retry is clicked", () => {
    const onRetry = vi.fn();
    render(<EmptyVault onRetry={onRetry} />);

    fireEvent.click(screen.getByRole("button", { name: /retry/i }));

    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("renders no Retry button when onRetry is not given", () => {
    render(<EmptyVault />);
    expect(screen.queryByRole("button", { name: /retry/i })).not.toBeInTheDocument();
  });
});
