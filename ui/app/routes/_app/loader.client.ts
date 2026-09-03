import { redirect } from "react-router";

import { apiFetch } from "~/lib/api";
import { clearToken, getToken, setAuthState } from "~/components/auth-provider";
import type { CollectionConfig } from "~/lib/types";

export type { CollectionConfig };

/**
 * Authorizes access to the protected app shell before any data loads.
 *
 * - No stored token     -> redirect to `/auth`.
 * - Token rejected (401)-> clear auth and redirect to `/auth`.
 * - Backend unreachable -> keep the token and redirect to `/auth`, which shows
 *                          the "unable to connect" screen.
 * - Otherwise           -> resolve on success and load collections.
 *
 * Keeping this in the `_app` loader (which runs before the shell mounts) is what
 * removes the old AuthGate render glitch: when a stored token is valid, the
 * shell mounts directly with real data and never flashes an auth/checking UI.
 */
export async function clientLoader() {
  const token = getToken();
  if (!token) {
    setAuthState("none");
    throw redirect("/auth");
  }

  let response: Response;
  try {
    response = await apiFetch("/api/collections");
  } catch {
    // Network-level failure while we have a token: keep it and defer to /auth.
    setAuthState("unreachable");
    throw redirect("/auth");
  }

  if (response.status === 401) {
    clearToken();
    setAuthState("none");
    throw redirect("/auth");
  }

  if (!response.ok) {
    // 5xx / proxy failure with a token present: keep it and defer to /auth.
    setAuthState("unreachable");
    throw redirect("/auth");
  }

  setAuthState("authenticated");
  const collections: CollectionConfig[] = await response.json();
  return { collections };
}
