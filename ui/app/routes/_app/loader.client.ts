export interface FilterCondition {
  eq?: any;
  ne?: any;
  gt?: any;
  gte?: any;
  lt?: any;
  lte?: any;
  contains?: any;
  startsWith?: any;
  endsWith?: any;
  exists?: boolean;
  in?: any[];
  notIn?: any[];
}

export interface ImageFilter {
  [field: string]: FilterCondition;
}

export interface Filter {
  oldImage?: ImageFilter;
  newImage?: ImageFilter;
}

export interface SinkConfig {
  type: string;
  endpoint?: string;
  bearerToken?: string;
  eventTypes?: string[];
  filter?: Filter;
  eventBusName?: string;
  source?: string;
  indexName?: string;
}

export interface CollectionConfig {
  _id?: string;
  collectionName: string;
  partitionKey?: string;
  sortKey?: string;
  streamEnabled: boolean;
  oldImage: boolean;
  ttlAttribute?: string;
  sinks: SinkConfig[];
  deletionProtection: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export async function clientLoader() {
  const response = await fetch("/api/collections");

  if (!response.ok) {
    throw new Error("Failed to fetch collections");
  }

  const collections: CollectionConfig[] = await response.json();
  return { collections };
}
