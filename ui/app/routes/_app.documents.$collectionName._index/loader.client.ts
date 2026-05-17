import type { Route } from "./+types/route";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const { collectionName } = params;

  const response = await fetch(`/api/collections/${collectionName}/documents`);

  if (!response.ok) {
    throw new Error("Failed to fetch document");
  }

  return await response.json();
}
