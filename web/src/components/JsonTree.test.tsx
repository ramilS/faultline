import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { JsonTree } from "./JsonTree";

describe("JsonTree", () => {
  it("shows primitives as JSON would write them, so a string is told from a number", () => {
    render(<JsonTree value={{ name: "gradle", count: 3, ok: true, none: null }} />);

    expect(screen.getByText('"gradle"')).toBeDefined();
    expect(screen.getByText("3")).toBeDefined();
    expect(screen.getByText("true")).toBeDefined();
    expect(screen.getByText("null")).toBeDefined();
  });

  it("opens the top level and folds what is nested below it", () => {
    render(<JsonTree value={{ cart: { items: 3, total: 129.9 } }} />);

    expect(screen.getByText("cart")).toBeDefined();
    expect(screen.queryByText("items")).toBeNull();
    expect(screen.getByRole("button", { name: /cart/ }).getAttribute("aria-expanded")).toBe("false");
  });

  it("opens a folded branch when it is clicked and folds it again on a second click", () => {
    render(<JsonTree value={{ cart: { items: 3 } }} />);
    const toggle = screen.getByRole("button", { name: /cart/ });

    fireEvent.click(toggle);
    expect(screen.getByText("items")).toBeDefined();
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    fireEvent.click(toggle);
    expect(screen.queryByText("items")).toBeNull();
  });

  it("does not build the rows of a folded branch, so a huge payload costs nothing until it is opened", () => {
    const wide = Object.fromEntries(Array.from({ length: 500 }, (_, index) => [`key${index}`, index]));

    const { container } = render(<JsonTree value={{ wide }} />);

    expect(container.querySelectorAll("li").length).toBeLessThan(5);
  });

  it("summarises a folded branch by its size", () => {
    render(<JsonTree value={{ items: [1, 2, 3], meta: { a: 1 } }} />);

    expect(screen.getByRole("button", { name: /items/ }).textContent).toContain("3 items");
    expect(screen.getByRole("button", { name: /meta/ }).textContent).toContain("1 key");
  });

  it("numbers array elements by position", () => {
    render(<JsonTree value={["first", "second"]} />);

    expect(screen.getByText("0")).toBeDefined();
    expect(screen.getByText('"second"')).toBeDefined();
  });

  it("shows an empty object and array as such rather than an openable nothing", () => {
    render(<JsonTree value={{ headers: {}, tags: [] }} />);

    expect(screen.getByText("{}")).toBeDefined();
    expect(screen.getByText("[]")).toBeDefined();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("renders a bare primitive at the top", () => {
    render(<JsonTree value="just text" />);

    expect(screen.getByText('"just text"')).toBeDefined();
  });

  it("keeps a hostile string as text", () => {
    const { container } = render(<JsonTree value={{ note: "<img src=x onerror=alert(1)>" }} />);

    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByText('"<img src=x onerror=alert(1)>"')).toBeDefined();
  });
});
