"use server";

import type { CreateCollectionPayload } from "./schemas";

export async function clientAction({ request }: { request: Request }) {
  const formData = await request.formData();
  const data = Object.fromEntries(formData);

  const payload: CreateCollectionPayload = {
    collection_name: data.collection_name as string,
    primary_key:
      data.use_dynamodb_mode === "true" ? (data.primary_key as string) || undefined : undefined,
    sort_key:
      data.use_dynamodb_mode === "true" ? (data.sort_key as string) || undefined : undefined,
    stream_enabled: false,
    old_image: false,
    destinations: [],
    deletion_protection: true,
  };

  const res = await fetch("/api/collections", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });

  if (!res.ok) {
    const error = await res.json();
    return { error: error.error || "Failed to create collection" };
  }

  return { success: true };
}