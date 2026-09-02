import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Item } from "../lib/vault/model.js";
import { ItemDetail } from "./ItemDetail.js";

const BASE: Item = {
  address: "skill/widget-fixer",
  kind: "skill",
  hook: "Fixes widgets",
  body: "# Widget Fixer\n\n- step one\n- step two\n\nSee [the docs](https://example.com/docs).",
  frontmatter: {},
};

describe("ItemDetail", () => {
  it("renders the title from the address and the hook", () => {
    render(<ItemDetail item={BASE} />);

    expect(screen.getByText("widget-fixer")).toBeInTheDocument();
    expect(screen.getByText("Fixes widgets")).toBeInTheDocument();
  });

  it("renders the body as GitHub-flavored Markdown", () => {
    render(<ItemDetail item={BASE} />);

    expect(screen.getByRole("heading", { name: "Widget Fixer" })).toBeInTheDocument();
    expect(screen.getByText("step one").closest("li")).not.toBeNull();
    expect(screen.getByText("step two").closest("li")).not.toBeNull();
    const link = screen.getByRole("link", { name: "the docs" });
    expect(link).toHaveAttribute("href", "https://example.com/docs");
  });

  it("renders a GFM feature: a table", () => {
    const item: Item = {
      ...BASE,
      body: "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
    };
    render(<ItemDetail item={item} />);

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("1")).toBeInTheDocument();
  });

  it("shows the provenance line when present", () => {
    const item: Item = { ...BASE, provenance: "waleed · 2026-08-30" };
    render(<ItemDetail item={item} />);

    expect(screen.getByText("waleed · 2026-08-30")).toBeInTheDocument();
  });

  it("shows no provenance line when absent", () => {
    render(<ItemDetail item={BASE} />);

    expect(screen.queryByText(/·/)).not.toBeInTheDocument();
  });

  it("shows a kept badge when review is empty or undefined", () => {
    render(<ItemDetail item={BASE} />);
    expect(screen.getByText(/kept/i)).toBeInTheDocument();
    expect(screen.queryByText(/draft/i)).not.toBeInTheDocument();
  });

  it("shows a kept badge when review is the empty string", () => {
    const item: Item = { ...BASE, review: "" };
    render(<ItemDetail item={item} />);
    expect(screen.getByText(/kept/i)).toBeInTheDocument();
  });

  it("shows a draft badge, visually distinct, when review is draft", () => {
    const item: Item = { ...BASE, review: "draft" };
    render(<ItemDetail item={item} />);

    const draftBadge = screen.getByText(/draft/i);
    expect(draftBadge).toBeInTheDocument();
    const keptBadge = screen.queryByText(/^kept$/i);
    expect(keptBadge).not.toBeInTheDocument();
  });

  it("calls onEdit when Edit is clicked", () => {
    const onEdit = vi.fn();
    render(<ItemDetail item={BASE} onEdit={onEdit} />);

    fireEvent.click(screen.getByRole("button", { name: /edit/i }));
    expect(onEdit).toHaveBeenCalledWith(BASE);
  });

  it("omits the Edit button when onEdit is not given", () => {
    render(<ItemDetail item={BASE} />);
    expect(screen.queryByRole("button", { name: /edit/i })).not.toBeInTheDocument();
  });

  it("shows a Keep button only for a draft item, and calls onKeep", () => {
    const onKeep = vi.fn();
    const draftItem: Item = { ...BASE, review: "draft" };
    render(<ItemDetail item={draftItem} onKeep={onKeep} />);

    fireEvent.click(screen.getByRole("button", { name: /keep/i }));
    expect(onKeep).toHaveBeenCalledWith(draftItem);
  });

  it("shows no Keep button for a kept item, even with onKeep given", () => {
    const onKeep = vi.fn();
    render(<ItemDetail item={BASE} onKeep={onKeep} />);
    expect(screen.queryByRole("button", { name: /keep/i })).not.toBeInTheDocument();
  });

  it("omits the Keep button for a draft item when onKeep is not given", () => {
    const draftItem: Item = { ...BASE, review: "draft" };
    render(<ItemDetail item={draftItem} />);
    expect(screen.queryByRole("button", { name: /keep/i })).not.toBeInTheDocument();
  });
});
