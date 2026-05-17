"use server";

import type { CollectionConfig } from "./schema";

export async function clientAction({ request }: { request: Request }) {
  const formData = await request.formData();
  const data = Object.fromEntries(formData);

  const destinations = data.destinations
    ? JSON.parse(data.destinations as string)
    : [];

  const payload: CollectionConfig = {
    collection_name: data.collection_name as string,
    primary_key: (data.primary_key as string) || undefined,
    sort_key: (data.sort_key as string) || undefined,
    stream_enabled: data.stream_enabled === "true",
    old_image: data.old_image === "true",
    ttl_attribute: (data.ttl_attribute as string) || undefined,
    deletion_protection: data.deletion_protection === "true",
    destinations,
  };

  const res = await fetch(`/api/collections/${data.collection_name}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });

  if (!res.ok) {
    const error = await res.json();
    return { error: error.error || "Failed to update collection" };
  }

  return { success: true };
}
