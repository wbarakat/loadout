import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Sidebar, type Section } from "./Sidebar.js";

const COUNTS = { skills: 3, memory: 5, secrets: 2, review: 1 };

describe("Sidebar", () => {
  it("shows a nav entry with its count for each section", () => {
    render(<Sidebar active="skills" counts={COUNTS} onSelect={() => {}} />);

    expect(screen.getByRole("button", { name: "Skills" })).toHaveTextContent("3");
    expect(screen.getByRole("button", { name: "Memory" })).toHaveTextContent("5");
    expect(screen.getByRole("button", { name: "Secrets" })).toHaveTextContent("2");
    expect(screen.getByRole("button", { name: "Review" })).toHaveTextContent("1");
    expect(screen.getByRole("button", { name: "Settings" })).toBeInTheDocument();
  });

  it("marks the active section and leaves the others unmarked", () => {
    render(<Sidebar active="memory" counts={COUNTS} onSelect={() => {}} />);

    expect(screen.getByRole("button", { name: "Memory" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(screen.getByRole("button", { name: "Skills" })).not.toHaveAttribute(
      "aria-current",
    );
    expect(screen.getByRole("button", { name: "Settings" })).not.toHaveAttribute(
      "aria-current",
    );
  });

  it("fires onSelect with the clicked section", () => {
    const onSelect = vi.fn<(s: Section) => void>();
    render(<Sidebar active="skills" counts={COUNTS} onSelect={onSelect} />);

    fireEvent.click(screen.getByRole("button", { name: "Review" }));
    expect(onSelect).toHaveBeenCalledWith("review");

    fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(onSelect).toHaveBeenCalledWith("settings");
  });
});
