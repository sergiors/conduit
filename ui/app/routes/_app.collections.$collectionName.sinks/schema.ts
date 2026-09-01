import { z } from "zod";

const VALID_EVENT_TYPES = ["INSERT", "MODIFY", "REMOVE"] as const;

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
  field: z.string().min(1, "Field name is required"),
  conditions: z.array(conditionSchema),
});

export const filterSchema = z.object({
  oldImage: z.array(fieldFilterSchema),
  newImage: z.array(fieldFilterSchema),
});

export const sinkSchema = z
  .object({
    type: z.enum(["http", "eventbridge", "meilisearch"]),
    endpoint: z.string().optional(),
    bearerToken: z.string().optional(),
    eventTypes: z
      .array(z.string())
      .min(1, "At least one event type is required"),
    filter: filterSchema.optional(),
    eventBusName: z.string().optional(),
    source: z.string().optional(),
    indexName: z.string().optional(),
  })
  .refine(
    (data) => {
      if (!data.endpoint) return false;
      if (data.type === "eventbridge" && !data.eventBusName) return false;
      return true;
    },
    {
      message: "Endpoint is required for all sink types",
      path: ["endpoint"],
    },
  )
  .refine(
    (data) => {
      const invalidTypes = data.eventTypes.filter(
        (t) => !VALID_EVENT_TYPES.includes(t as any),
      );
      return invalidTypes.length === 0;
    },
    {
      message: `Event types must be one of: ${VALID_EVENT_TYPES.join(", ")}`,
      path: ["eventTypes"],
    },
  );

export const sinksFormSchema = z.object({
  sinks: z.array(sinkSchema),
});

export type SinksForm = z.infer<typeof sinksFormSchema>;

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