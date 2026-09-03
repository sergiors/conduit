import type { Route } from "./+types/route";
import { apiErrorMessage, apiFetch } from "~/lib/api";

/**
 * Streaming (CDC) toggle (mutation).
 *
 *   POST {oldImage: boolean} -> enable stream, configuring oldImage
 *   DELETE                    -> disable stream (and oldImage)
 *
 * Forwards the submitted method (and JSON body for POST) to the backend at
 * /api/collections/:collectionName/stream. When invoked through a fetcher,
 * React Router revalidates the active loaders on success, so the Settings
 * page refreshes automatically.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const method = request.method === "DELETE" ? "DELETE" : "POST";
  const init: RequestInit = { method };
  if (method !== "DELETE") {
    init.body = await request.text();
    init.headers = { "Content-Type": "application/json" };
  }

  const response = await apiFetch(
    `/api/collections/${params.collectionName}/stream`,
    init,
  );

  if (!response.ok) {
    return {
      error: await apiErrorMessage(response, "Failed to update streaming"),
    };
  }

  return { ok: true };
}