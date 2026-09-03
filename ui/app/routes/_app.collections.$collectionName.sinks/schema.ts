import { z } from "zod";
import type {
  Filter,
  FilterCondition,
  ImageFilter,
  SinkConfig,
} from "~/lib/types";

const VALID_EVENT_TYPES = ["INSERT", "MODIFY", "REMOVE"] as const;

/**
 * The sink-filter builder works with a flat list of conditions per field. The
 * backend DSL is implicit AND between field conditions within each image
 * (oldImage/newImage). No OR/NOT/grouping is supported.
 */
export const conditionSchema = z.object({
  type: z.enum([
    "eq",
    "ne",
    "gt",
    "gte",
    "lt",
    "lte",
    "contains",
    "startsWith",
    "endsWith",
    "exists",
    "in",
    "notIn",
  ]),
  value: z.string().optional(),
});

export const fieldFilterSchema = z.object({
  field: z.string(),
  conditions: z.array(conditionSchema),
});

export type FieldFilter = z.infer<typeof fieldFilterSchema>;
export type Condition = z.infer<typeof conditionSchema>;

export const conditionOptions: { value: Condition["type"]; label: string }[] = [
  { value: "eq", label: "Equals" },
  { value: "ne", label: "Not Equals" },
  { value: "gt", label: "Greater Than" },
  { value: "gte", label: "Greater Than or Equal" },
  { value: "lt", label: "Less Than" },
  { value: "lte", label: "Less Than or Equal" },
  { value: "contains", label: "Contains" },
  { value: "startsWith", label: "Starts With" },
  { value: "endsWith", label: "Ends With" },
  { value: "exists", label: "Exists" },
  { value: "in", label: "In" },
  { value: "notIn", label: "Not In" },
];

/**
 * Form fields for creating a single sink. Spec fields are nested under `spec`
 * matching the backend shape, and are validated per type.
 */
export const createSinkSchema = z
  .object({
    type: z.enum(["http", "eventbridge", "meilisearch"]),
    spec: z
      .object({
        endpoint: z.string().optional(),
        bearerToken: z.string().optional(),
        eventBusName: z.string().optional(),
        source: z.string().optional(),
        host: z.string().optional(),
        apiKey: z.string().optional(),
        indexName: z.string().optional(),
      })
      .optional(),
    eventTypes: z
      .array(z.string())
      .min(1, "At least one event type is required")
      .refine(
        (types) => types.every((t) => VALID_EVENT_TYPES.includes(t as never)),
        `Event types must be one of: ${VALID_EVENT_TYPES.join(", ")}`,
      ),
    filter: z
      .object({
        oldImage: z.array(fieldFilterSchema),
        newImage: z.array(fieldFilterSchema),
      })
      .optional(),
  })
  .superRefine((data, ctx) => {
    const spec = data.spec ?? {};
    if (data.type === "http" && !spec.endpoint) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["spec", "endpoint"],
        message: "Endpoint is required for HTTP sinks",
      });
    }
    if (data.type === "eventbridge" && !spec.eventBusName) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["spec", "eventBusName"],
        message: "Event bus name is required for EventBridge sinks",
      });
    }
    if (data.type === "meilisearch" && !spec.host) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["spec", "host"],
        message: "Host is required for Meilisearch sinks",
      });
    }
  });

/** The form shape for creating a sink. */
export type CreateSinkForm = z.infer<typeof createSinkSchema>;

/** Empty per-type spec helpers. */
export function emptySpecFor(type: SinkConfig["type"]): Record<string, string> {
  if (type === "http") return { endpoint: "", bearerToken: "" };
  if (type === "eventbridge") return { eventBusName: "", source: "" };
  return { host: "", apiKey: "", indexName: "" };
}

/**
 * Build the canonical `Filter` payload from the form's per-field condition
 * lists. Only fields with at least one condition are emitted. Exists is a
 * boolean; in/notIn parse a comma-separated string.
 */
export function formToFilter(form: {
  oldImage: FieldFilter[];
  newImage: FieldFilter[];
}): Filter {
  const out: Filter = {};

  for (const image of ["oldImage", "newImage"] as const) {
    const filters = form[image] ?? [];
    const imageFilter: ImageFilter = {};
    for (const f of filters) {
      if (!f.field) continue;
      const cond: FilterCondition = {};
      for (const condition of f.conditions) {
        if (condition.type === "exists") {
          if (condition.value === "true" || condition.value === "false") {
            cond.exists = condition.value === "true";
          }
        } else if (condition.type === "in" || condition.type === "notIn") {
          const raw = condition.value ?? "";
          const parsed = raw
            .split(",")
            .map((s) => s.trim())
            .filter((s) => s !== "");
          if (parsed.length > 0) cond[condition.type] = parsed;
        } else if (condition.value !== undefined && condition.value !== "") {
          cond[condition.type] = condition.value;
        }
      }
      if (Object.keys(cond).length > 0) {
        imageFilter[f.field] = cond;
      }
    }
    if (Object.keys(imageFilter).length > 0) {
      out[image] = imageFilter;
    }
  }

  return out;
}

/** Convert a backend `Filter` into the per-field condition-list form shape. */
export function filterToForm(filter: Filter | undefined): {
  oldImage: FieldFilter[];
  newImage: FieldFilter[];
} {
  const form: { oldImage: FieldFilter[]; newImage: FieldFilter[] } = {
    oldImage: [],
    newImage: [],
  };
  if (!filter) return form;

  for (const image of ["oldImage", "newImage"] as const) {
    const imageFilter = filter[image];
    if (!imageFilter) continue;
    const fieldFilters: FieldFilter[] = [];
    for (const [field, cond] of Object.entries(imageFilter)) {
      const conditions: Condition[] = [];
      for (const op of conditionOptions) {
        const value = cond[op.value];
        if (value === undefined) continue;
        conditions.push({
          type: op.value,
          value:
            op.value === "in" || op.value === "notIn"
              ? Array.isArray(value)
                ? value.join(", ")
                : String(value)
              : String(value),
        });
      }
      fieldFilters.push({ field, conditions });
    }
    form[image] = fieldFilters;
  }

  return form;
}
