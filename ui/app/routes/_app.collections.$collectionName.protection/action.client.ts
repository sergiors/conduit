import type { Route } from "./+types/route";
import { apiErrorMessage, apiFetch } from "~/lib/api";

/**
 * Deletion protection toggle (mutation).
 *
 *   POST   -> enable protection
 *   DELETE -> disable protection
 *
 * The submitted HTTP method is forwarded to the backend
 * (/api/collections/:collectionName/protection). When invoked through a
 * fetcher, React Router revalidates the active loaders on success, so the
 * collections list (and the protection badge) refreshes automatically.
 */
export async function clientAction({ request, params }: Route.ClientActionArgs) {
  const response = await apiFetch(
    `/api/collections/${params.collectionName}/protection`,
    { method: request.method === "DELETE" ? "DELETE" : "POST" },
  );

  if (!response.ok) {
    return {
      error: await apiErrorMessage(
        response,
        "Failed to update deletion protection",
      ),
    };
  }

  return { ok: true };
}