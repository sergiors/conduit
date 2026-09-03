import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  apiErrorMessage,
  apiFetch,
  apiGet,
  apiPost,
  verifyToken,
} from "~/lib/api";
import {
  clearToken,
  getAuthState,
  getToken,
  setAuthState,
  setToken,
  TOKEN_KEY,
} from "~/components/auth-provider";

describe("api client", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    clearToken();
    setAuthState("none");
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("attaches the Bearer header for relative /api requests when a token is present", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    await apiFetch("/api/collections");

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/collections");
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer secret-token");
  });

  it("does not attach a Bearer header when no token is present", async () => {
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    await apiFetch("/api/collections");

    const [, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBeNull();
  });

  it("preserves caller headers and options", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    await apiFetch("/api/collections", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ a: 1 }),
    });

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/collections");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ a: 1 }));
    const headers = new Headers(init.headers);
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.get("Authorization")).toBe("Bearer secret-token");
  });

  it("keeps API URLs relative (no host literals)", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    await apiGet("/api/collections");
    await apiPost("/api/collections", { x: 1 });

    for (const [url] of fetchMock.mock.calls) {
      expect(url.startsWith("/api/")).toBe(true);
      expect(url).not.toMatch(/^https?:\/\//);
    }
  });

  it("clears the token and auth state when an authenticated request returns 401", async () => {
    setToken("secret-token");
    setAuthState("authenticated");
    fetchMock.mockResolvedValue(new Response("{}", { status: 401 }));

    await apiFetch("/api/collections");

    expect(getToken()).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(getAuthState()).toBe("none");
  });

  it("does not clear auth on a 401 when no token was attached", async () => {
    setAuthState("authenticated");
    fetchMock.mockResolvedValue(new Response("{}", { status: 401 }));

    await apiFetch("/api/collections");

    expect(getAuthState()).toBe("authenticated");
  });

  it("does not treat 403 as an invalid token", async () => {
    setToken("secret-token");
    setAuthState("authenticated");
    fetchMock.mockResolvedValue(new Response("{}", { status: 403 }));

    await apiFetch("/api/collections");

    expect(getToken()).toBe("secret-token");
    expect(getAuthState()).toBe("authenticated");
  });

  it("preserves a Request's original headers and merges init.headers", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    const request = new Request("http://localhost:3000/api/collections", {
      method: "POST",
      headers: { "X-Custom": "from-request", "Content-Type": "text/plain" },
    });

    await apiFetch(request, {
      headers: { "Content-Type": "application/json" },
    });

    const [, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    // Original Request header preserved.
    expect(headers.get("X-Custom")).toBe("from-request");
    // init.headers takes precedence over the Request's header.
    expect(headers.get("Content-Type")).toBe("application/json");
    // Bearer token attached for the same-origin /api/* Request.
    expect(headers.get("Authorization")).toBe("Bearer secret-token");
  });

  it("attaches the token to a Request constructed from a relative /api path", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    // In a browser this resolves to an absolute same-origin URL. jsdom cannot
    // resolve relative Request URLs, so construct the equivalent absolute URL.
    const request = new Request("http://localhost:3000/api/collections");

    await apiFetch(request);

    const [, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer secret-token");
  });

  it("does not attach the Bearer token to an absolute external URL", async () => {
    setToken("secret-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));

    await apiFetch("https://external.example.com/api/collections");

    const [, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBeNull();
  });
});

describe("apiErrorMessage", () => {
  it("reads the canonical { error: { code, message } } shape", async () => {
    const res = new Response(
      JSON.stringify({
        error: { code: "validation_error", message: "bad thing" },
      }),
      { status: 400 },
    );
    await expect(apiErrorMessage(res, "fallback")).resolves.toBe("bad thing");
  });

  it("falls back to the code when message is missing", async () => {
    const res = new Response(
      JSON.stringify({ error: { code: "sink_identity_immutable" } }),
      { status: 400 },
    );
    await expect(apiErrorMessage(res, "fallback")).resolves.toBe(
      "sink_identity_immutable",
    );
  });

  it("accepts a legacy { error: string } shape", async () => {
    const res = new Response(JSON.stringify({ error: "legacy message" }), {
      status: 400,
    });
    await expect(apiErrorMessage(res, "fallback")).resolves.toBe(
      "legacy message",
    );
  });

  it("falls back when the body is not JSON", async () => {
    const res = new Response("not json", { status: 500, statusText: "Oops" });
    await expect(apiErrorMessage(res, "fallback")).resolves.toBe("Oops");
  });

  it("falls back when the body is not an error shape", async () => {
    const res = new Response(JSON.stringify({ hello: "world" }), {
      status: 400,
    });
    await expect(apiErrorMessage(res, "my fallback")).resolves.toBe(
      "my fallback",
    );
  });
});

describe("verifyToken", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    clearToken();
    setAuthState("none");
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns ok for a 200 response", async () => {
    fetchMock.mockResolvedValue(new Response("[]", { status: 200 }));
    await expect(verifyToken("t")).resolves.toEqual({ status: "ok" });
  });

  it("returns invalid for a 401 response", async () => {
    fetchMock.mockResolvedValue(new Response("{}", { status: 401 }));
    await expect(verifyToken("t")).resolves.toEqual({ status: "invalid" });
  });

  it("returns unreachable for a 5xx response", async () => {
    fetchMock.mockResolvedValue(new Response("{}", { status: 500 }));
    await expect(verifyToken("t")).resolves.toEqual({ status: "unreachable" });
  });

  it("returns unreachable for a network error", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(verifyToken("t")).resolves.toEqual({ status: "unreachable" });
  });

  it("routes through apiFetch with the explicit token attached", async () => {
    fetchMock.mockResolvedValue(new Response("[]", { status: 200 }));
    await verifyToken("candidate-token");

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/collections");
    const headers = new Headers(init.headers);
    expect(headers.get("Authorization")).toBe("Bearer candidate-token");
  });

  it("does not clear stored auth on a 401 (verifyToken opts out of the global clear)", async () => {
    setToken("stored-token");
    setAuthState("authenticated");
    fetchMock.mockResolvedValue(new Response("{}", { status: 401 }));

    await verifyToken("stored-token");

    expect(getToken()).toBe("stored-token");
    expect(getAuthState()).toBe("authenticated");
  });
});
