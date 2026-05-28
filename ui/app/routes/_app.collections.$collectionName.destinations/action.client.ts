import type { Route } from "./+types/route";

export async function clientAction({
  request,
  params,
}: Route.ClientActionArgs) {
  const collectionName = params.collectionName;
  const body = await request.json();

  const response = await fetch(
    `/api/collections/${collectionName}/destinations`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );

  if (!response.ok) {
    const error = await response.json();
    return { error: error.error || "Failed to update destinations" };
  }

  return { success: true };
}
