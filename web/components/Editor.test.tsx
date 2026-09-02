import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Item } from "../lib/vault/model.js";
import { Editor } from "./Editor.js";

const ITEM: Item = {
  address: "memory/alpha",
  kind: "memory",
  hook: "Alpha notes",
  body: "the original body text",
  frontmatter: { description: "Alpha notes" },
};

describe("Editor", () => {
  it("seeds the textarea with the item's body", () => {
    render(<Editor item={ITEM} onSave={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByRole("textbox")).toHaveValue("the original body text");
  });

  it("calls onSave with the edited text when Save is clicked", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<Editor item={ITEM} onSave={onSave} onCancel={vi.fn()} />);

    fireEvent.change(screen.getByRole("textbox"), { target: { value: "edited text" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    expect(onSave).toHaveBeenCalledWith("edited text");
  });

  it("calls onCancel when Cancel is clicked, without calling onSave", () => {
    const onSave = vi.fn();
    const onCancel = vi.fn();
    render(<Editor item={ITEM} onSave={onSave} onCancel={onCancel} />);

    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onSave).not.toHaveBeenCalled();
  });

  it("shows a saving state that disables Save and Cancel", () => {
    render(<Editor item={ITEM} onSave={vi.fn()} onCancel={vi.fn()} saving />);

    expect(screen.getByRole("textbox")).toBeDisabled();
    expect(screen.getByRole("button", { name: /saving/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /cancel/i })).toBeDisabled();
  });
});
