import { z } from "zod";

const VALID_EVENT_TYPES = ["INSERT", "MODIFY", "REMOVE"] as const;

export const conditionSchema = z.object({
  type: z.enum(["prefix", "suffix", "exists", "numeric", "anything-but"]),
  value: z.string().optional(),
  numericOp: z.enum([">", "<", ">=", "<=", "="]).optional(),
});

export const fieldFilterSchema = z.object({
  field: z.string().min(1, "Field name is required"),
  conditions: z.array(conditionSchema),
});

export const filterCriteriaSchema = z.object({
  old_image: z.array(fieldFilterSchema),
  new_image: z.array(fieldFilterSchema),
});

export const sinkSchema = z
  .object({
    type: z.enum(["http", "eventbridge", "meilisearch"]),
    endpoint: z.string().optional(),
    bearer_token: z.string().optional(),
    event_types: z
      .array(z.string())
      .min(1, "At least one event type is required"),
    filter_criteria: filterCriteriaSchema.optional(),
    event_bus_name: z.string().optional(),
    source: z.string().optional(),
    index_name: z.string().optional(),
  })
  .refine(
    (data) => {
      if (!data.endpoint) return false;
      if (data.type === "eventbridge" && !data.event_bus_name) return false;
      return true;
    },
    {
      message: "Endpoint is required for all sink types",
      path: ["endpoint"],
    },
  )
  .refine(
    (data) => {
      const invalidTypes = data.event_types.filter(
        (t) => !VALID_EVENT_TYPES.includes(t as any),
      );
      return invalidTypes.length === 0;
    },
    {
      message: `Event types must be one of: ${VALID_EVENT_TYPES.join(", ")}`,
      path: ["event_types"],
    },
  );

export const sinksFormSchema = z.object({
  sinks: z.array(sinkSchema),
});

export type SinksForm = z.infer<typeof sinksFormSchema>;

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

export type FieldFilter = z.infer<typeof fieldFilterSchema>;
export type Condition = z.infer<typeof conditionSchema>;

export const conditionOptions: { value: Condition["type"]; label: string }[] = [
  { value: "prefix", label: "Prefix" },
  { value: "suffix", label: "Suffix" },
  { value: "exists", label: "Exists" },
  { value: "numeric", label: "Numeric" },
  { value: "anything-but", label: "Anything But" },
];

export const numericOperators = [
  { value: ">", label: ">" },
  { value: "<", label: "<" },
  { value: ">=", label: ">=" },
  { value: "<=", label: "<=" },
  { value: "=", label: "=" },
];