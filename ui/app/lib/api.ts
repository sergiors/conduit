import {
  clearToken,
  getToken,
  setAuthState,
} from "../components/auth-provider";

/**
 * Centralized API client.
 *
 * All frontend API requests should go through `apiFetch` (or the `get`/`post`
 * helpers) so that the Bearer token is attached automatically and 401
 * responses are handled consistently.
 *
 * URLs must remain relative (e.g. `/api/collections`). The Vite dev proxy maps
 * `/api` and `/health` to the backend; do not introduce host literals here.
 */

/**
 * The `error` object returned by the backend on every non-2xx response:
 *   { "error": { "code": "validation_error", "message": "..." } }
 */
export interface ApiErrorInfo {
  code?: string;
  message?: string;
}

/**
 * Parse a backend error response into a human-readable message.
 *
 * The backend always returns `{ error: { code, message } }`. Older code assumed
 * `error.error` was a plain string; this helper reads the canonical object shape
 * and falls back to `response.statusText` / the explicit `fallback` when the body
 * is not parseable or is not shaped as expected.
 */
export async function apiErrorMessage(
  response: Response,
  fallback: string,
): Promise<string> {
  try {
    const data: unknown = await response.json();
    if (
      data &&
      typeof data === "object" &&
      "error" in data &&
      data.error !== null &&
      typeof data.error === "object"
    ) {
      const info = (data as { error: ApiErrorInfo }).error;
      if (typeof info.message === "string" && info.message) {
        return info.message;
      }
      if (typeof info.code === "string" && info.code) {
        return info.code;
      }
    }
    if (typeof data === "object" && data !== null) {
      // Accept a legacy `{ error: "..." }` string shape defensively.
      const legacy = (data as { error?: unknown }).error;
      if (typeof legacy === "string" && legacy) return legacy;
    }
  } catch {
    // Body was not JSON; fall through.
  }
  return response.statusText || fallback;
}

function isSameOrigin(url: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return new URL(url, window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/**
 * Decide whether a request URL should receive the Bearer token.
 *
 * - Relative `/api/*` paths (the normal case) always attach.
 * - Absolute URLs only attach when they are same-origin AND point at `/api/*`.
 *   This covers `Request` objects constructed from a relative `/api/*` path,
 *   which browsers resolve to an absolute same-origin URL.
 * - Absolute external URLs (different origin) never receive the token.
 */
function shouldAttachToken(url: string): boolean {
  if (url.startsWith("/api/")) return true;

  if (/^https?:\/\//i.test(url)) {
    return isSameOrigin(url) && new URL(url).pathname.startsWith("/api/");
  }

  return false;
}

/**
 * Handle an authenticated request that returned HTTP 401.
 *
 * The backend only uses 401 to signal an invalid/missing token, so a 401 on an
 * authenticated request is the single signal that the stored token is no longer
 * valid. We clear auth state and storage and notify the app to return to the
 * `/auth` screen via a custom event (listened for in the root layout). Other
 * statuses (e.g. 403) are not treated as invalid-token signals, so they must
 * not clear auth here.
 */
function handleUnauthorized(): void {
  clearToken();
  setAuthState("none");
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event("conduit:unauthorized"));
  }
}

/**
 * Perform a fetch against the backend API, attaching the Bearer token for
 * relative `/api/*` requests.
 *
 * By default the stored token (see `getToken`) is attached. Callers that need
 * to validate a specific (not-yet-stored) token may pass it via `init.token`.
 * Preserves the caller's method, body and other options. When `input` is a
 * `Request`, its original headers are preserved and merged with `init.headers`
 * (explicit `init.headers` take precedence). A 401 on an authenticated request
 * clears auth state and storage.
 */
export async function apiFetch(
  input: string | URL | Request,
  init?: RequestInit & { token?: string; skipAuthClear?: boolean },
): Promise<Response> {
  const token = init?.token ?? getToken();

  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.href
        : input.url;
  const attachToken = shouldAttachToken(url) && token !== null;

  // Start from the Request's own headers (if any) so they are preserved, then
  // layer `init.headers` on top so explicit caller headers win, and finally set
  // the Authorization header for API requests.
  const headers = new Headers(
    input instanceof Request ? input.headers : undefined,
  );
  if (init?.headers) {
    for (const [key, value] of new Headers(init.headers)) {
      headers.set(key, value);
    }
  }
  if (attachToken) {
    headers.set("Authorization", `Bearer ${token}`);
  }

  const {
    token: _ignored,
    skipAuthClear: _skipAuthClear,
    ...fetchInit
  } = init ?? {};
  const response = await fetch(input, { ...fetchInit, headers });

  // Route loader-owned verification (verifyToken) opts out of the global clear so
  // the auth route loader can decide how to react to a 401 itself.
  if (response.status === 401 && attachToken && !init?.skipAuthClear) {
    handleUnauthorized();
  }

  return response;
}

export async function apiGet(
  input: string,
  init?: RequestInit,
): Promise<Response> {
  return apiFetch(input, { method: "GET", ...init });
}

export async function apiPost(
  input: string,
  body?: unknown,
  init?: RequestInit,
): Promise<Response> {
  return apiFetch(input, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    ...init,
  });
}

export async function apiPatch(
  input: string,
  body?: unknown,
  init?: RequestInit,
): Promise<Response> {
  return apiFetch(input, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    ...init,
  });
}

export async function apiDelete(
  input: string,
  init?: RequestInit,
): Promise<Response> {
  return apiFetch(input, { method: "DELETE", ...init });
}

/**
 * Classify a failed API call as an authentication/bootstrap or connectivity
 * condition that the auth/`_app` route loaders own, versus a genuine API error.
 *
 * - 401: invalid/missing token. The auth route clears auth and shows the token
 *   screen (or the `_app` loader redirects to /auth), so loaders must not throw
 *   (which would route to the error boundary).
 * - network rejection / 5xx: temporary backend/connectivity failure. The
 *   `/auth` route keeps the token and shows the "unable to connect" screen, so
 *   loaders must not throw here either.
 *
 * Other non-2xx statuses (e.g. 404, 422) are real API errors and should keep
 * their existing error behavior.
 */
export function isAuthOrConnectivityFailure(
  response: Response | null,
  error: unknown,
): boolean {
  if (error) return true;
  if (!response) return true;
  return response.status === 401 || response.status >= 500;
}

export type VerifyResult =
  | { status: "ok" }
  | { status: "invalid" }
  | { status: "unreachable" };

/**
 * Validate a token against the inexpensive `GET /api/collections` endpoint.
 *
 * - 200            -> valid
 * - 401            -> invalid token
 * - network error / 5xx -> unreachable (do NOT treat as invalid)
 */
export async function verifyToken(token: string): Promise<VerifyResult> {
  try {
    // Route through apiFetch so the Bearer logic stays in one place, but pass
    // an explicit token and skip the global auth clear: the `/auth` route owns
    // how a 401 for a not-yet-stored (or already-stored) token is handled.
    const response = await apiFetch("/api/collections", {
      token,
      skipAuthClear: true,
    });

    if (response.status === 200) return { status: "ok" };
    if (response.status === 401) return { status: "invalid" };
    return { status: "unreachable" };
  } catch {
    return { status: "unreachable" };
  }
}
