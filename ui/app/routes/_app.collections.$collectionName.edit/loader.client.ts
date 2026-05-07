import type { CollectionConfig } from "../_app.collections._index/loader.client";
import type { Route } from "./+types/route";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const collectioName = params.collectionName;
  const response = await fetch(`/api/collections/${collectioName}`);

  if (!response.ok) {
    throw new Error("Failed to fetch collection");
  }

  const collection: CollectionConfig = await response.json();
  return { collection };
}
