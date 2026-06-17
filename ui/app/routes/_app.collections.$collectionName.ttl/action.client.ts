import type { Route } from "./+types/route";

/**
 * TTL toggle (mutation).
 *
 *   PUT {attribute: string} -> set TTL attribute + create the TTL index
 *   DELETE                 -> drop the TTL index + clear the attribute
 *
 * Forwards the submitted method (and JSON body for PUT) to the backend at
 * /api/collections/:collectionName/ttl. When invoked through a fetcher,
 * React Router revalidates the active loaders on success.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const init: RequestInit = { method: request.method };
  if (request.method !== "DELETE") {
    init.body = await request.text();
    init.headers = { "Content-Type": "application/json" };
  }

  const response = await fetch(
    `/api/collections/${params.collectionName}/ttl`,
    init,
  );

  if (!response.ok) {
    const error = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    return { error: error.error || "Failed to update TTL" };
  }

  return { ok: true };
}