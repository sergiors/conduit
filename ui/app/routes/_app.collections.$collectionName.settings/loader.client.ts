import type { CollectionConfig } from "~/lib/types";
import type { Route } from "./+types/route";
import { apiFetch, isAuthOrConnectivityFailure } from "~/lib/api";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const collectionName = params.collectionName;

  try {
    const response = await apiFetch(`/api/collections/${collectionName}`);

    if (!response.ok) {
      // Auth/bootstrap or connectivity failures are owned by the `_app`/`auth`
      // route client loaders, which redirect to /auth when appropriate. Other
      // API errors keep their existing throw behavior.
      if (isAuthOrConnectivityFailure(response, null)) {
        return { collection: null };
      }
      throw new Error("Failed to fetch collection");
    }

    const collection: CollectionConfig = await response.json();
    return { collection };
  } catch (error) {
    if (isAuthOrConnectivityFailure(null, error)) {
      return { collection: null };
    }
    throw error;
  }
}
