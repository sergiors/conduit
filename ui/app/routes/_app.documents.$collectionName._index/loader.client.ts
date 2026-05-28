import type { Route } from "./+types/route";

export interface ListDocumentsResponse {
  documents: Record<string, unknown>[];
  total: number;
  page: number;
  limit: number;
  totalPages: number;
}

export interface ListDocumentsQuery {
  page?: number;
  limit?: number;
  filter?: string;
  sort?: string;
}

export async function clientLoader({
  params,
  request,
}: Route.ClientLoaderArgs) {
  const { collectionName } = params;

  let page = "1";
  let limit = "20";
  let filter: string | null = null;
  let sort: string | null = null;

  if (request) {
    const url = new URL(request.url);
    page = url.searchParams.get("page") || "1";
    limit = url.searchParams.get("limit") || "20";
    filter = url.searchParams.get("filter");
    sort = url.searchParams.get("sort");
  }

  const queryParams = new URLSearchParams({ page, limit });
  if (filter) queryParams.set("filter", filter);
  if (sort) queryParams.set("sort", sort);

  const response = await fetch(
    `/api/collections/${collectionName}/documents?${queryParams.toString()}`,
  );

  if (!response.ok) {
    throw new Error("Failed to fetch documents");
  }

  return (await response.json()) as ListDocumentsResponse;
}
