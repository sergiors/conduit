export interface FilterCondition {
  prefix?: string;
  suffix?: string;
  exists?: boolean;
  numeric?: any[];
  "anything-but"?: any;
}

export interface ImageFilter {
  [field: string]: FilterCondition;
}

export interface FilterCriteria {
  old_image?: ImageFilter;
  new_image?: ImageFilter;
}

export interface SinkConfig {
  type: string;
  endpoint?: string;
  bearer_token?: string;
  event_types?: string[];
  filter_criteria?: FilterCriteria;
  event_bus_name?: string;
  source?: string;
  index_name?: string;
}

export interface CollectionConfig {
  _id?: string;
  collection_name: string;
  partition_key?: string;
  sort_key?: string;
  stream_enabled: boolean;
  old_image: boolean;
  ttl_attribute?: string;
  sinks: SinkConfig[];
  deletion_protection: boolean;
  created_at?: string;
  updated_at?: string;
}

export async function clientLoader() {
  const response = await fetch("/api/collections");

  if (!response.ok) {
    throw new Error("Failed to fetch collections");
  }

  const collections: CollectionConfig[] = await response.json();
  return { collections };
}
