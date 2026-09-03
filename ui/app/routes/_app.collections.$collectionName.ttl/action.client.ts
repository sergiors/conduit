import type { Route } from "./+types/route";
import { apiErrorMessage, apiFetch } from "~/lib/api";

/**
 * TTL toggle (mutation).
 *
 *   POST {attribute: string} -> set TTL attribute + create the TTL index
 *   DELETE                   -> drop the TTL index + clear the attribute
 *
 * Forwards the submitted method (and JSON body for POST) to the backend at
 * /api/collections/:collectionName/ttl. When invoked through a fetcher,
 * React Router revalidates the active loaders on success.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const method = request.method === "DELETE" ? "DELETE" : "POST";
  const init: RequestInit = { method };
  if (method !== "DELETE") {
    init.body = await request.text();
    init.headers = { "Content-Type": "application/json" };
  }

  const response = await apiFetch(
    `/api/collections/${params.collectionName}/ttl`,
    init,
  );

  if (!response.ok) {
    return { error: await apiErrorMessage(response, "Failed to update TTL") };
  }

  return { ok: true };
}