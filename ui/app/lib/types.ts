/**
 * Shared frontend domain types that mirror the backend response shapes.
 *
 * These are intentionally kept in sync with the Go `collections` package so
 * the UI never assumes a field the backend does not (or should not) return.
 */

/**
 * A single field condition in a sink filter. Mirrors the backend DSL: implicit
 * AND between field conditions within `oldImage`/`newImage`. Supported
 * operators: exists, eq, ne, gt, gte, lt, lte, contains, startsWith, endsWith,
 * in, notIn.
 */
export type FilterCondition = {
  eq?: unknown;
  ne?: unknown;
  gt?: unknown;
  gte?: unknown;
  lt?: unknown;
  lte?: unknown;
  contains?: unknown;
  startsWith?: unknown;
  endsWith?: unknown;
  exists?: boolean;
  in?: unknown[];
  notIn?: unknown[];
};

export interface ImageFilter {
  [field: string]: FilterCondition;
}

export interface Filter {
  oldImage?: ImageFilter;
  newImage?: ImageFilter;
}

/**
 * A sink as returned by the backend. `id`, `type` and `spec` are immutable
 * after creation; only `eventTypes` and `filter` are mutable via PATCH.
 *
 * `spec` carries the nested type-specific shape:
 *   - http:        { endpoint, bearerToken? }
 *   - eventbridge: { eventBusName, source? }
 *   - meilisearch: { host, apiKey?, indexName? }
 */
export interface SinkConfig {
  id: string;
  type: "http" | "eventbridge" | "meilisearch";
  spec: Record<string, unknown>;
  eventTypes: string[];
  filter?: Filter;
  createdAt?: string;
  updatedAt?: string;
}

/**
 * A collection as returned by the backend. There is no embedded `sinks` array:
 * sinks are fetched through the dedicated `/api/collections/:name/sinks`.
 */
export interface CollectionConfig {
  _id?: string;
  collectionName: string;
  partitionKey?: string;
  sortKey?: string;
  streamEnabled: boolean;
  oldImage: boolean;
  streamStartedAt?: string;
  ttlAttribute?: string;
  deletionProtection: boolean;
  createdAt?: string;
  updatedAt?: string;
}

/** The payload accepted by the sink-filter builder (see sinks schema). */
export type FilterOperator =
  | "eq"
  | "ne"
  | "gt"
  | "gte"
  | "lt"
  | "lte"
  | "contains"
  | "startsWith"
  | "endsWith"
  | "exists"
  | "in"
  | "notIn";
