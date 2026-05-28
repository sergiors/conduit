import type { CollectionConfig } from "~/routes/_app/loader.client";
import type { Route } from "./+types/route";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const collectionName = params.collectionName;
  const response = await fetch(`/api/collections/${collectionName}`);

  if (!response.ok) {
    throw new Error("Failed to fetch collection");
  }

  const collection: CollectionConfig = await response.json();
  return { collection };
}
