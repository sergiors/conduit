import type { Route } from "./+types/route";

export interface DestinationConfig {
  type: string;
  endpoint?: string;
  bearer_token?: string;
  event_types?: string[];
}

export interface TableConfig {
  _id?: string;
  table_name: string;
  stream_enabled: boolean;
  old_image: boolean;
  ttl_attribute?: string;
  destinations: DestinationConfig[];
  deletion_protection: boolean;
  created_at?: string;
  updated_at?: string;
}

export async function clientLoader({}: Route.ClientLoaderArgs) {
  const res = await fetch("/api/tables");
  if (!res.ok) throw new Error("Failed to fetch tables");
  const data: TableConfig[] = await res.json();
  return { tables: data };
}
