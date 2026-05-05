import { z } from "zod";

const conditionSchema = z.object({
  type: z.enum(["prefix", "suffix", "exists", "numeric", "anything-but"]),
  value: z.string().optional(),
  numericOp: z.enum([">", "<", ">=", "<=", "="]).optional(),
});

const fieldFilterSchema = z.object({
  field: z.string().min(1, "Field name is required"),
  conditions: z.array(conditionSchema),
});

export const filterCriteriaSchema = z.object({
  old_image: z.array(fieldFilterSchema),
  new_image: z.array(fieldFilterSchema),
});

const destinationSchema = z
  .object({
    type: z.enum(["http", "eventbridge", "meilisearch"]),
    endpoint: z.string().optional(),
    bearer_token: z.string().optional(),
    event_types: z
      .array(z.string())
      .min(1, "At least one event type is required"),
    filter_criteria: filterCriteriaSchema.optional(),
    region: z.string().optional(),
    event_bus_name: z.string().optional(),
    source: z.string().optional(),
    index_name: z.string().optional(),
  })
  .refine(
    (data) => {
      if (data.type === "http" && !data.endpoint) return false;
      if (data.type === "eventbridge" && !data.region) return false;
      if (data.type === "eventbridge" && !data.event_bus_name) return false;
      if (data.type === "meilisearch" && !data.endpoint) return false;
      return true;
    },
    {
      message: "Required fields missing for destination type",
      path: ["endpoint"],
    },
  );

export const tableSchema = z
  .object({
    table_name: z.string().min(1, "Table name is required"),
    stream_enabled: z.boolean(),
    old_image: z.boolean(),
    ttl_attribute: z.string().optional(),
    destinations: z.array(destinationSchema),
    deletion_protection: z.boolean(),
  })
  .refine((data) => !data.stream_enabled || data.destinations.length > 0, {
    message: "At least one destination is required",
    path: ["destinations"],
  });

export type TableForm = z.infer<typeof tableSchema>;
export type FieldFilter = z.infer<typeof fieldFilterSchema>;
export type Condition = z.infer<typeof conditionSchema>;
