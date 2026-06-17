import type { Route } from "./+types/route";

/**
 * Streaming (CDC) toggle (mutation).
 *
 *   PUT {old_image: boolean} -> enable stream, configuring old_image
 *   DELETE                  -> disable stream (and old_image)
 *
 * Forwards the submitted method (and JSON body for PUT) to the backend at
 * /api/collections/:collectionName/stream. When invoked through a fetcher,
 * React Router revalidates the active loaders on success, so the Settings
 * page refreshes automatically.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const init: RequestInit = { method: request.method };
  if (request.method !== "DELETE") {
    init.body = await request.text();
    init.headers = { "Content-Type": "application/json" };
  }

  const response = await fetch(
    `/api/collections/${params.collectionName}/stream`,
    init,
  );

  if (!response.ok) {
    const error = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    return { error: error.error || "Failed to update streaming" };
  }

  return { ok: true };
}