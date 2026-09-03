/**
 * Pure helpers for rendering document tables in a DynamoDB-like listing.
 *
 * Kept framework-free so they can be unit tested independently of the route.
 */

/**
 * Determine the ordered set of columns to display for the current page of
 * documents, mirroring how the DynamoDB UI surfaces key attributes first.
 *
 * Rules:
 * - If a `partitionKey` is configured it is always first.
 * - If a `sortKey` is configured and differs from the partition key it is
 *   second.
 * - If no configured key attributes exist, `_id` is shown first.
 * - Remaining attributes follow in a stable order (first-seen across the
 *   page), excluding duplicates of the priority columns. `_id` stays included
 *   later whenever pk/sk are configured and it is not itself a priority
 *   column.
 */
export function computeColumns(
  documents: ReadonlyArray<Record<string, unknown>>,
  partitionKey?: string,
  sortKey?: string,
): string[] {
  // Union of attribute names across the page, preserving first-seen order.
  const keys: string[] = [];
  for (const doc of documents) {
    for (const key of Object.keys(doc)) {
      if (!keys.includes(key)) keys.push(key);
    }
  }

  const pk = partitionKey || undefined;
  const sk = sortKey || undefined;
  const hasConfiguredKey = pk !== undefined || sk !== undefined;

  const result: string[] = [];

  if (hasConfiguredKey) {
    if (pk) result.push(pk);
    const skToAdd = sk && sk !== pk ? sk : undefined;
    if (skToAdd !== undefined) result.push(skToAdd);
  } else {
    result.push("_id");
  }

  const seen = new Set(result);
  for (const key of keys) {
    if (!seen.has(key)) {
      result.push(key);
      seen.add(key);
    }
  }

  return result;
}

/**
 * Map of column name to key-role label ("PK" / "SK") for header annotations.
 */
export function computeKeyLabels(
  partitionKey?: string,
  sortKey?: string,
): Record<string, string> {
  const labels: Record<string, string> = {};
  if (partitionKey) labels[partitionKey] = "PK";
  if (sortKey && sortKey !== partitionKey) labels[sortKey] = "SK";
  return labels;
}

/**
 * A value is considered "missing" when the attribute is absent on the row.
 * `null` is a present value and rendered as such.
 */
export function isMissingValue(value: unknown): boolean {
  return value === undefined;
}

const MISSING = "\u2013"; // en dash
const MAX_LENGTH = 120;

function truncate(value: string, maxLength = MAX_LENGTH): string {
  if (value.length <= maxLength) return value;
  return `${value.slice(0, maxLength)}\u2026`;
}

/**
 * Render a single attribute value for display.
 *
 * - missing       -> en dash (the route styles it muted)
 * - null          -> "null"
 * - string        -> as-is, truncated
 * - object/array  -> compact JSON, truncated
 * - primitives    -> String(value), truncated
 */
export function formatAttributeValue(
  value: unknown,
  maxLength = MAX_LENGTH,
): string {
  if (value === undefined) return MISSING;
  if (value === null) return "null";
  if (typeof value === "string") return truncate(value, maxLength);

  if (typeof value === "object") {
    let serialized: string;
    try {
      serialized = JSON.stringify(value);
    } catch {
      serialized = "[unserializable]";
    }
    return truncate(serialized, maxLength);
  }

  return truncate(String(value), maxLength);
}
