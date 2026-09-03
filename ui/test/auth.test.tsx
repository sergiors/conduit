import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import AuthScreen from "~/routes/auth/route";
import { clientLoader as authLoader } from "~/routes/auth/route";
import { clientLoader as appLoader } from "~/routes/_app/loader.client";
import {
  AuthProvider,
  clearToken,
  getToken,
  getAuthState,
  setAuthState,
  setToken,
  TOKEN_KEY,
} from "~/components/auth-provider";

const verifyTokenMock = vi.fn<typeof import("~/lib/api").verifyToken>();

vi.mock("~/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("~/lib/api")>();
  return {
    ...actual,
    verifyToken: (...args: Parameters<typeof actual.verifyToken>) =>
      verifyTokenMock(...args),
  };
});

const navigateMock = vi.fn();

vi.mock("react-router", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

describe("auth clientLoader (route-driven state resolution)", () => {
  beforeEach(() => {
    localStorage.clear();
    clearToken();
    verifyTokenMock.mockReset();
    navigateMock.mockReset();
  });

  it("resolves to token entry (none) when no token is stored", async () => {
    await authLoader({} as never);
    expect(getAuthState()).toBe("none");
  });

  it("redirects to /collections when the stored token verifies", async () => {
    setToken("stored-token");
    verifyTokenMock.mockResolvedValue({ status: "ok" });

    await expect(authLoader({} as never)).rejects.toMatchObject({
      status: 302,
      headers: {},
    });
    expect(getAuthState()).toBe("authenticated");
  });

  it("clears an invalid stored token and resolves to token entry", async () => {
    setToken("stored-bad-token");
    verifyTokenMock.mockResolvedValue({ status: "invalid" });

    await authLoader({} as never);
    expect(getToken()).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(getAuthState()).toBe("none");
  });

  it("keeps a stored token and resolves to unreachable on backend failure", async () => {
    setToken("stored-token");
    verifyTokenMock.mockResolvedValue({ status: "unreachable" });

    await authLoader({} as never);
    expect(getToken()).toBe("stored-token");
    expect(localStorage.getItem(TOKEN_KEY)).toBe("stored-token");
    expect(getAuthState()).toBe("unreachable");
  });
});

describe("protected app shell clientLoader", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    localStorage.clear();
    clearToken();
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("redirects to /auth when no token is stored", async () => {
    await expect(appLoader()).rejects.toMatchObject({ status: 302 });
    expect(getAuthState()).toBe("none");
  });

  it("loads collections on a valid token and reports authenticated", async () => {
    setToken("stored-token");
    fetchMock.mockResolvedValue(
      new Response('[{"collectionName":"orders"}]', { status: 200 }),
    );

    const data = await appLoader();
    expect(data.collections).toHaveLength(1);
    expect(getAuthState()).toBe("authenticated");
  });

  it("clears an invalid token and redirects to /auth on 401", async () => {
    setToken("stored-token");
    fetchMock.mockResolvedValue(new Response("{}", { status: 401 }));

    await expect(appLoader()).rejects.toMatchObject({ status: 302 });
    expect(getToken()).toBeNull();
    expect(getAuthState()).toBe("none");
  });

  it("keeps the token and redirects to /auth when the backend is unreachable", async () => {
    setToken("stored-token");
    fetchMock.mockRejectedValue(new TypeError("failed to fetch"));

    await expect(appLoader()).rejects.toMatchObject({ status: 302 });
    expect(getToken()).toBe("stored-token");
    expect(getAuthState()).toBe("unreachable");
  });
});

describe("AuthScreen", () => {
  beforeEach(() => {
    localStorage.clear();
    clearToken();
    setAuthState("none");
    verifyTokenMock.mockReset();
    navigateMock.mockReset();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function renderScreen() {
    return render(
      <AuthProvider>
        <AuthScreen />
      </AuthProvider>,
    );
  }

  it("shows the token entry screen when no token is stored", () => {
    renderScreen();
    expect(screen.getByLabelText("Token")).toBeInTheDocument();
  });

  it("submits a valid token, persists it, and navigates to /collections", async () => {
    verifyTokenMock.mockResolvedValue({ status: "ok" });

    renderScreen();
    const input = screen.getByLabelText("Token");
    await userEvent.type(input, "valid-token");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith("/collections", {
        replace: true,
      }),
    );
    expect(localStorage.getItem(TOKEN_KEY)).toBe("valid-token");
    expect(getToken()).toBe("valid-token");
  });

  it("does not persist an invalid token and shows an error", async () => {
    verifyTokenMock.mockResolvedValue({ status: "invalid" });

    renderScreen();
    const input = screen.getByLabelText("Token");
    await userEvent.type(input, "bad-token");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));

    await waitFor(() => {
      expect(screen.getByText(/Invalid API token/i)).toBeInTheDocument();
    });
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(getToken()).toBeNull();
    expect(navigateMock).not.toHaveBeenCalled();
  });

  it("shows unable-to-connect when a stored token is kept but the API is down", async () => {
    setToken("stored-token");
    // Drive state to "unreachable" as the loader would.
    setAuthState("unreachable");

    renderScreen();
    expect(await screen.findByText("Unable to connect")).toBeInTheDocument();
    expect(localStorage.getItem(TOKEN_KEY)).toBe("stored-token");
    expect(getToken()).toBe("stored-token");
  });

  it("disconnect clears the token and navigates to /auth", async () => {
    setToken("stored-token");
    setAuthState("unreachable");

    renderScreen();
    await userEvent.click(
      await screen.findByRole("button", { name: "Disconnect" }),
    );

    expect(getToken()).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(navigateMock).toHaveBeenCalledWith("/auth", { replace: true });
  });
});
