import { useEffect } from "react";
import { useBlocker, Blocker } from "react-router-dom";

// Returns the React Router blocker so callers can render a custom modal
// instead of using the browser's native confirm(). Also installs a
// beforeunload handler that catches refresh / tab-close / window-close
// (those *must* use the native browser dialog — no other choice).
export function useUnsavedGuard(dirty: boolean): Blocker {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (!dirty) return false;
    if (currentLocation.pathname === nextLocation.pathname) return false;
    return true;
  });

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  return blocker;
}
