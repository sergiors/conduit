export async function clientAction({ request }: { request: Request }) {
  const body = await request.json();

  const response = await fetch("/api/collections", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    const error = await response.json();
    return { error: error.error || "Failed to create collection" };
  }

  return { success: true };
}
