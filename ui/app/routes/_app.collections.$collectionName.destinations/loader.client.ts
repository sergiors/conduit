import type { Route } from "./+types/route";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const collectionName = params.collectionName;
  const response = await fetch(
    `/api/collections/${collectionName}/destinations`,
  );

  if (!response.ok) {
    throw new Error("Failed to fetch destinations");
  }

  const destinations = await response.json();
  return { destinations };
}
