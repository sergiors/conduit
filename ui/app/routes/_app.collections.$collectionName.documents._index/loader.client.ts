import type { CollectionConfig } from "~/lib/types";
import type { Route } from "./+types/route";
import { apiFetch, isAuthOrConnectivityFailure } from "~/lib/api";

/**
 * The backend returns a raw document array for both list and single-document
 * reads. There are no total/page/totalPages fields, no filters or sort, and no
 * create/update/delete routes — documents are strictly read-only.
 */
export interface DocumentsLoaderData {
  documents: Record<string, unknown>[];
  limit: number;
  skip: number;
  /** Collection metadata used to surface key attributes (PK/SK) first. */
  collection: Pick<CollectionConfig, "partitionKey" | "sortKey"> | null;
}

export async function clientLoader({
  params,
  request,
}: Route.ClientLoaderArgs) {
  const { collectionName } = params;
  const url = new URL(request.url);
  let limit = 20;
  let skip = 0;
  const limitRaw = url.searchParams.get("limit");
  if (limitRaw) {
    const parsed = Number.parseInt(limitRaw, 10);
    if (!Number.isNaN(parsed) && parsed > 0) limit = Math.min(parsed, 1000);
  }
  const skipRaw = url.searchParams.get("skip");
  if (skipRaw) {
    const parsed = Number.parseInt(skipRaw, 10);
    if (!Number.isNaN(parsed) && parsed >= 0) skip = parsed;
  }

  const queryParams = new URLSearchParams({ limit: String(limit) });
  if (skip > 0) queryParams.set("skip", String(skip));

  try {
    const [documentsResponse, collectionResponse] = await Promise.all([
      apiFetch(
        `/api/collections/${collectionName}/documents?${queryParams.toString()}`,
      ),
      apiFetch(`/api/collections/${collectionName}`),
    ]);

    const authOrConnectivityFailure =
      isAuthOrConnectivityFailure(documentsResponse, null) ||
      isAuthOrConnectivityFailure(collectionResponse, null);

    // Auth/bootstrap or connectivity failures are owned by the `_app`/`auth`
    // route client loaders, which redirect to /auth; return safe fallback data
    // here so the shell can render rather than routing to the error boundary.
    if (authOrConnectivityFailure) {
      return { documents: [], limit, skip, collection: null };
    }

    if (!documentsResponse.ok || !collectionResponse.ok) {
      throw new Error("Failed to fetch documents");
    }

    const documents = (await documentsResponse.json()) as Record<
      string,
      unknown
    >[];
    const collection = (await collectionResponse.json()) as CollectionConfig;

    return {
      documents,
      limit,
      skip,
      collection: {
        partitionKey: collection.partitionKey,
        sortKey: collection.sortKey,
      },
    };
  } catch (error) {
    if (isAuthOrConnectivityFailure(null, error)) {
      return { documents: [], limit, skip, collection: null };
    }
    throw error;
  }
}
