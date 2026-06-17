import type { Route } from "./+types/route";

export async function clientLoader({ params }: Route.ClientLoaderArgs) {
  const collectionName = params.collectionName;
  const response = await fetch(
    `/api/collections/${collectionName}/sinks`,
  );

  if (!response.ok) {
    throw new Error("Failed to fetch sinks");
  }

  const sinks = await response.json();
  return { sinks };
}
