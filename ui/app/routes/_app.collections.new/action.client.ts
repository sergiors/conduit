import { apiErrorMessage, apiPost } from "~/lib/api";

export async function clientAction({ request }: { request: Request }) {
  const body = await request.json();

  const response = await apiPost("/api/collections", body);

  if (!response.ok) {
    return {
      error: await apiErrorMessage(response, "Failed to create collection"),
    };
  }

  return { success: true };
}
