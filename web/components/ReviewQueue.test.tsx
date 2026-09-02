import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Item } from "../lib/vault/model.js";
import { ReviewQueue } from "./ReviewQueue.js";

const BETA: Item = {
  address: "memory/beta",
  kind: "memory",
  hook: "Beta notes, still draft",
  body: "beta body",
  frontmatter: { review: "draft" },
  review: "draft",
};

const GAMMA: Item = {
  address: "skill/gamma-fixer",
  kind: "skill",
  hook: "Fixes gammas",
  body: "gamma body",
  frontmatter: { review: "draft" },
  review: "draft",
};

describe("ReviewQueue", () => {
  it("lists every draft item with its name and hook", () => {
    render(<ReviewQueue drafts={[BETA, GAMMA]} onKeep={vi.fn()} />);

    expect(screen.getByText("beta")).toBeInTheDocument();
    expect(screen.getByText("Beta notes, still draft")).toBeInTheDocument();
    expect(screen.getByText("gamma-fixer")).toBeInTheDocument();
  });

  it("calls onKeep with the item when its Keep button is clicked", () => {
    const onKeep = vi.fn().mockResolvedValue(undefined);
    render(<ReviewQueue drafts={[BETA, GAMMA]} onKeep={onKeep} />);

    const betaRow = screen.getByText("beta").closest("li");
    expect(betaRow).not.toBeNull();
    fireEvent.click(
      // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
      screen.getAllByRole("button", { name: /keep/i })[0]!,
    );

    expect(onKeep).toHaveBeenCalledWith(BETA);
    expect(betaRow).not.toBeNull();
  });

  it("shows nothing to review when the drafts list is empty", () => {
    render(<ReviewQueue drafts={[]} onKeep={vi.fn()} />);
    expect(screen.queryByRole("button", { name: /keep/i })).not.toBeInTheDocument();
  });

  it("no longer renders an item once it is removed from drafts (re-pulled away)", () => {
    const { rerender } = render(<ReviewQueue drafts={[BETA, GAMMA]} onKeep={vi.fn()} />);
    expect(screen.getByText("beta")).toBeInTheDocument();

    rerender(<ReviewQueue drafts={[GAMMA]} onKeep={vi.fn()} />);
    expect(screen.queryByText("beta")).not.toBeInTheDocument();
    expect(screen.getByText("gamma-fixer")).toBeInTheDocument();
  });
});
