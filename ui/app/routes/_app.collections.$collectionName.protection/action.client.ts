import type { Route } from "./+types/route";

/**
 * Deletion protection toggle (mutation).
 *
 *   PUT    -> enable protection
 *   DELETE -> disable protection
 *
 * The submitted HTTP method is forwarded to the backend
 * (/api/collections/:collectionName/protection). When invoked through a
 * fetcher, React Router revalidates the active loaders on success, so the
 * collections list (and the protection badge) refreshes automatically.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const response = await fetch(
    `/api/collections/${params.collectionName}/protection`,
    { method: request.method },
  );

  if (!response.ok) {
    const error = (await response.json().catch(() => ({}))) as {
      error?: string;
    };
    return {
      error: error.error || "Failed to update deletion protection",
    };
  }

  return { ok: true };
}