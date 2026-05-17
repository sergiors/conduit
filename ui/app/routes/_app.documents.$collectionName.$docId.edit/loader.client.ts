import type { Route } from "./+types/route";

export interface DocumentConfig {
  _id: string;
  [key: string]: unknown;
}

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const { collectionName, docId } = params;

  const response = await fetch(
    `/api/collections/${collectionName}/documents/${docId}`,
  );

  if (!response.ok) {
    throw new Error("Failed to fetch document");
  }

  const document: DocumentConfig = await response.json();
  return { document };
}
