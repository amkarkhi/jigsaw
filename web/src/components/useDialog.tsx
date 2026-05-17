import { ReactNode, useState } from "react";
import { ConfirmModal } from "./ConfirmModal";

// useConfirmDialog / useAlertDialog — hooks that return a promise-shaped
// `ask()` and the JSX to render. They replace window.confirm()/alert() so
// prompts use the app's theme instead of the browser chrome.
//
// Usage:
//   const { confirm, ui } = useConfirmDialog();
//   async function onDelete() {
//     if (!(await confirm({ title: "Delete?", message: "..." }))) return;
//     // proceed
//   }
//   return <>{...} {ui}</>;

interface ConfirmOpts {
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
}

interface ConfirmState extends ConfirmOpts {
  resolve: (value: boolean) => void;
}

export function useConfirmDialog() {
  const [state, setState] = useState<ConfirmState | null>(null);

  function confirm(opts: ConfirmOpts): Promise<boolean> {
    return new Promise<boolean>((resolve) => {
      setState({ ...opts, resolve });
    });
  }
  function handle(value: boolean) {
    state?.resolve(value);
    setState(null);
  }

  const ui = state ? (
    <ConfirmModal
      title={state.title}
      message={state.message}
      confirmLabel={state.confirmLabel}
      cancelLabel={state.cancelLabel}
      danger={state.danger}
      onConfirm={() => handle(true)}
      onCancel={() => handle(false)}
    />
  ) : null;
  return { confirm, ui };
}

interface AlertOpts {
  title?: string;
  message: ReactNode;
  okLabel?: string;
  tone?: "info" | "error";
}

interface AlertState extends AlertOpts {
  resolve: () => void;
}

export function useAlertDialog() {
  const [state, setState] = useState<AlertState | null>(null);

  function alert(opts: AlertOpts): Promise<void> {
    return new Promise<void>((resolve) => {
      setState({ ...opts, resolve });
    });
  }
  function close() {
    state?.resolve();
    setState(null);
  }

  const ui = state ? (
    <ConfirmModal
      title={state.title ?? (state.tone === "error" ? "Error" : "Notice")}
      message={state.message}
      confirmLabel={state.okLabel ?? "OK"}
      danger={state.tone === "error"}
      hideCancel
      onConfirm={close}
      onCancel={close}
    />
  ) : null;
  return { alert, ui };
}
