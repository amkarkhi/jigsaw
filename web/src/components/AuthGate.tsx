import { ReactNode, useEffect, useState } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { api } from "../api/client";

// Wraps the protected app. Hits /api/me on mount; if not authenticated,
// redirects to /login while preserving the original target in router state.
// In local mode the server always reports authenticated=true.

export interface CurrentUser {
  label: string;
  role: "admin" | "viewer";
  access: string[];
}

export function AuthGate({ children }: { children: (user: CurrentUser) => ReactNode }) {
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [unauth, setUnauth] = useState(false);
  const location = useLocation();

  useEffect(() => {
    let cancelled = false;
    api.me()
      .then((res) => {
        if (cancelled) return;
        if (res.authenticated && res.label) {
          setUser({
            label: res.label,
            role: (res.role ?? "viewer") as "admin" | "viewer",
            access: res.access ?? [],
          });
        } else {
          setUnauth(true);
        }
      })
      .catch(() => {
        if (!cancelled) setUnauth(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, []);

  if (loading) {
    return <div className="loading" style={{ padding: 64, textAlign: "center" }}>Connecting…</div>;
  }
  if (unauth) {
    return <Navigate to="/login" state={{ from: location.pathname + location.search }} replace />;
  }
  if (!user) return null;
  return <>{children(user)}</>;
}
