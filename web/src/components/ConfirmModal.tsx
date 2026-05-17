import { ReactNode, useEffect } from "react";

// Small, reusable confirm dialog. Replaces window.confirm() so prompts
// look like the rest of the app and stay theme-consistent.

export function ConfirmModal({
  title,
  message,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  danger = false,
  hideCancel = false,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  hideCancel?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  // Esc cancels, Enter confirms.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCancel();
      } else if (e.key === "Enter") {
        e.preventDefault();
        onConfirm();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onConfirm, onCancel]);

  return (
    <div
      onClick={onCancel}
      style={{
        position: "fixed",
        inset: 0,
        background: "#000c",
        zIndex: 300,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--panel)",
          border: "1px solid var(--border-strong)",
          borderRadius: 8,
          width: 440,
          padding: 20,
          boxShadow: "0 8px 32px #000c",
        }}
      >
        <h2 style={{ margin: "0 0 12px 0", fontSize: 16, fontWeight: 500 }}>{title}</h2>
        <div style={{ color: "var(--text-dim)", marginBottom: 20, fontSize: 13 }}>{message}</div>
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          {!hideCancel && <button className="btn" onClick={onCancel}>{cancelLabel}</button>}
          <button
            className={`btn ${danger ? "btn-danger" : "btn-primary"}`}
            onClick={onConfirm}
            autoFocus
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
