import type { Route } from "./+types/route";
import { redirect } from "react-router";
import { verifyToken } from "~/lib/api";
import { clearToken, getToken, setAuthState } from "~/components/auth-provider";

export async function clientLoader({}: Route.ClientLoaderArgs) {
  const token = getToken();
  if (!token) {
    setAuthState("none");
    return null;
  }

  setAuthState("checking");
  const result = await verifyToken(token);

  if (result.status === "ok") {
    setAuthState("authenticated");
    throw redirect("/collections");
  }

  if (result.status === "invalid") {
    clearToken();
    setAuthState("none");
    return null;
  }

  // Unreachable: keep the token and show the unable-to-connect screen.
  setAuthState("unreachable");
  return null;
}
