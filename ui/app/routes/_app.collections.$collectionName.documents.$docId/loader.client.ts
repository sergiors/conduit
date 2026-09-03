import type { Route } from "./+types/route";
import { apiFetch, isAuthOrConnectivityFailure } from "~/lib/api";

export interface DocumentConfig {
  _id: string;
  [key: string]: unknown;
}

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const { collectionName, docId } = params;

  try {
    const response = await apiFetch(
      `/api/collections/${collectionName}/documents/${docId}`,
    );

    if (!response.ok) {
      if (isAuthOrConnectivityFailure(response, null)) {
        return { document: null };
      }
      throw new Error("Failed to fetch document");
    }

    const document: DocumentConfig = await response.json();
    return { document };
  } catch (error) {
    if (isAuthOrConnectivityFailure(null, error)) {
      return { document: null };
    }
    throw error;
  }
}
