import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ConfirmDialog, { STORAGE_KEY } from "./ConfirmDialog";

function renderDialog(
  props: Partial<React.ComponentProps<typeof ConfirmDialog>> = {},
) {
  const defaultProps = {
    title: "Delete API Key",
    message: "Are you sure?",
    onConfirm: vi.fn(),
    onCancel: vi.fn(),
  };
  return { ...defaultProps, ...render(<ConfirmDialog {...defaultProps} {...props} />) };
}

describe("ConfirmDialog", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  // ── Rendering ──

  it("renders title and message", () => {
    renderDialog({ title: "Delete Key", message: "This is permanent." });
    expect(screen.getByText("Delete Key")).toBeInTheDocument();
    expect(screen.getByText("This is permanent.")).toBeInTheDocument();
  });

  it("renders default button labels (Delete / Cancel)", () => {
    renderDialog();
    expect(screen.getByTestId("confirm-dialog-confirm")).toHaveTextContent("Delete");
    expect(screen.getByTestId("confirm-dialog-cancel")).toHaveTextContent("Cancel");
  });

  it("renders custom button labels", () => {
    renderDialog({ confirmLabel: "Remove", cancelLabel: "Go Back" });
    expect(screen.getByTestId("confirm-dialog-confirm")).toHaveTextContent("Remove");
    expect(screen.getByTestId("confirm-dialog-cancel")).toHaveTextContent("Go Back");
  });

  it("renders red Delete button and gray Cancel button", () => {
    renderDialog();
    const confirmBtn = screen.getByTestId("confirm-dialog-confirm");
    const cancelBtn = screen.getByTestId("confirm-dialog-cancel");
    expect(confirmBtn.className).toContain("bg-red-600");
    expect(cancelBtn.className).toContain("border-gray-300");
    expect(cancelBtn.className).toContain("text-gray-700");
  });

  it("renders warning icon", () => {
    renderDialog();
    const icon = document.querySelector(".text-red-600");
    expect(icon).toBeInTheDocument();
  });

  // ── Callbacks ──

  it("calls onConfirm when Delete button is clicked", async () => {
    const onConfirm = vi.fn();
    renderDialog({ onConfirm });
    const user = userEvent.setup();
    await user.click(screen.getByTestId("confirm-dialog-confirm"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when Cancel button is clicked", async () => {
    const onCancel = vi.fn();
    renderDialog({ onCancel });
    const user = userEvent.setup();
    await user.click(screen.getByTestId("confirm-dialog-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when backdrop is clicked", async () => {
    const onCancel = vi.fn();
    renderDialog({ onCancel });
    const user = userEvent.setup();
    await user.click(screen.getByTestId("confirm-dialog-backdrop"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  // ── Keyboard ──

  it("calls onCancel when Escape key is pressed", async () => {
    const onCancel = vi.fn();
    renderDialog({ onCancel });
    const user = userEvent.setup();
    await user.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("focuses cancel button on mount", () => {
    renderDialog();
    expect(document.activeElement).toBe(screen.getByTestId("confirm-dialog-cancel"));
  });

  // ── Accessibility ──

  it("has role=dialog and aria-modal=true", () => {
    renderDialog();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
  });

  it("has aria-labelledby and aria-describedby", () => {
    renderDialog();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-labelledby", "confirm-dialog-title");
    expect(dialog).toHaveAttribute("aria-describedby", "confirm-dialog-message");
  });

  it("has focus ring classes on buttons", () => {
    renderDialog();
    const confirmBtn = screen.getByTestId("confirm-dialog-confirm");
    const cancelBtn = screen.getByTestId("confirm-dialog-cancel");
    expect(confirmBtn.className).toContain("focus:ring-2");
    expect(confirmBtn.className).toContain("focus:ring-blue-500");
    expect(cancelBtn.className).toContain("focus:ring-2");
    expect(cancelBtn.className).toContain("focus:ring-blue-500");
  });

  // ── Don't ask again ──

  it("does not show 'Don't ask again' checkbox when storageId is not provided", () => {
    renderDialog();
    expect(screen.queryByTestId("dont-ask-again-checkbox")).not.toBeInTheDocument();
  });

  it("shows 'Don't ask again' checkbox when storageId is provided", () => {
    renderDialog({ storageId: "test-dialog" });
    expect(screen.getByTestId("dont-ask-again-checkbox")).toBeInTheDocument();
    expect(screen.getByText("Don't ask again")).toBeInTheDocument();
  });

  it("stores suppression in localStorage when 'Don't ask again' is checked and confirmed", async () => {
    const onConfirm = vi.fn();
    renderDialog({ onConfirm, storageId: "test-dialog" });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("dont-ask-again-checkbox"));
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    expect(onConfirm).toHaveBeenCalledTimes(1);
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || "{}");
    expect(stored["test-dialog"]).toBe(true);
  });

  it("does not store suppression when 'Don't ask again' is not checked", async () => {
    const onConfirm = vi.fn();
    renderDialog({ onConfirm, storageId: "test-dialog" });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    expect(onConfirm).toHaveBeenCalledTimes(1);
    const stored = localStorage.getItem(STORAGE_KEY);
    expect(stored).toBeNull();
  });
});
