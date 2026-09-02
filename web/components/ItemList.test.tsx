import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ItemList, type ListRow } from "./ItemList.js";

const ROWS: ListRow[] = [
  { address: "skill/widget-fixer", title: "widget-fixer", hook: "Fixes widgets", draft: false },
  { address: "memory/banana-notes", title: "banana-notes", hook: "notes about bananas", draft: true },
];

describe("ItemList", () => {
  it("renders every row's title and hook, with a draft badge only when draft", () => {
    render(
      <ItemList rows={ROWS} query="" onQuery={() => {}} onSelect={() => {}} />,
    );

    const fixerRow = screen.getByText("widget-fixer").closest("button");
    expect(fixerRow).not.toBeNull();
    expect(within(fixerRow as HTMLElement).queryByText(/draft/i)).not.toBeInTheDocument();
    expect(screen.getByText("Fixes widgets")).toBeInTheDocument();

    const bananaRow = screen.getByText("banana-notes").closest("button");
    expect(bananaRow).not.toBeNull();
    expect(within(bananaRow as HTMLElement).getByText(/draft/i)).toBeInTheDocument();
  });

  it("filters rows case-insensitively across address, title, and hook", () => {
    render(
      <ItemList rows={ROWS} query="BANANA" onQuery={() => {}} onSelect={() => {}} />,
    );

    expect(screen.getByText("banana-notes")).toBeInTheDocument();
    expect(screen.queryByText("widget-fixer")).not.toBeInTheDocument();
  });

  it("filters by a hook substring too", () => {
    render(
      <ItemList rows={ROWS} query="fixes" onQuery={() => {}} onSelect={() => {}} />,
    );

    expect(screen.getByText("widget-fixer")).toBeInTheDocument();
    expect(screen.queryByText("banana-notes")).not.toBeInTheDocument();
  });

  it("calls onQuery when the search box changes, reflecting the query value", () => {
    const onQuery = vi.fn();
    render(
      <ItemList rows={ROWS} query="wid" onQuery={onQuery} onSelect={() => {}} />,
    );

    const input = screen.getByRole("searchbox") as HTMLInputElement;
    expect(input.value).toBe("wid");

    fireEvent.change(input, { target: { value: "widget" } });
    expect(onQuery).toHaveBeenCalledWith("widget");
  });

  it("marks the selected row and fires onSelect with its address", () => {
    const onSelect = vi.fn();
    const { rerender } = render(
      <ItemList
        rows={ROWS}
        query=""
        onQuery={() => {}}
        onSelect={onSelect}
        selectedAddress={undefined}
      />,
    );

    fireEvent.click(screen.getByText("widget-fixer"));
    expect(onSelect).toHaveBeenCalledWith("skill/widget-fixer");

    rerender(
      <ItemList
        rows={ROWS}
        query=""
        onQuery={() => {}}
        onSelect={onSelect}
        selectedAddress="skill/widget-fixer"
      />,
    );

    const selectedRow = screen.getByText("widget-fixer").closest("button");
    expect(selectedRow).toHaveAttribute("aria-current", "true");
    const otherRow = screen.getByText("banana-notes").closest("button");
    expect(otherRow).not.toHaveAttribute("aria-current");
  });
});
