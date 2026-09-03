import { createContext, useContext, useSyncExternalStore } from "react";

/**
 * Project-specific key used to persist the API bearer token.
 */
export const TOKEN_KEY = "conduit_api_token";

export type AuthState = "checking" | "none" | "unreachable" | "authenticated";

let state: AuthState = "checking";

const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((listener) => listener());
}

function readStoredToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// --- Non-React bridge (used by the API client and tests) ---

export function getToken(): string | null {
  return readStoredToken();
}

export function setToken(token: string | null): void {
  try {
    if (token) {
      localStorage.setItem(TOKEN_KEY, token);
    } else {
      localStorage.removeItem(TOKEN_KEY);
    }
  } catch {
    // Storage may be unavailable (e.g. private mode); the in-memory token
    // still works for the current session.
  }
  emit();
}

export function clearToken(): void {
  setToken(null);
}

export function getAuthState(): AuthState {
  return state;
}

export function setAuthState(next: AuthState): void {
  state = next;
  emit();
}

// --- React context/provider + hook ---

export interface AuthContextValue {
  state: AuthState;
  token: string | null;
  setToken: (token: string | null) => void;
  clearToken: () => void;
  setAuthState: (next: AuthState) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

/**
 * Provides authorization state to the component tree. Mount this high in the
 * app (e.g. in the root layout) so every route can consume `useAuth()`.
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  // The app runs in SPA mode (`ssr: false`), so there is no real server render.
  // `getServerSnapshot` is still consulted during client hydration, so it must
  // return the same value as `getSnapshot`. Hardcoding a "checking"/null fallback
  // here would make the initial hydrated UI briefly show the checking spinner
  // (and a null token) even when a token is already stored and verified, then
  // reconcile to the real state — a hydration mismatch. Using the same getters
  // for both keeps the server and client snapshots consistent. Both getters are
  // safe on the server too: `getToken` guards against missing localStorage and
  // `getAuthState` returns the module-level state.
  const state = useSyncExternalStore(subscribe, getAuthState, getAuthState);
  const token = useSyncExternalStore(subscribe, getToken, getToken);

  return (
    <AuthContext.Provider
      value={{
        state,
        token,
        setToken,
        clearToken,
        setAuthState,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

/**
 * Access the current authorization state and the actions to change it.
 * Must be used within an `AuthProvider`.
 */
export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return ctx;
}
