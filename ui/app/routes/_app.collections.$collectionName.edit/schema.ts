import { z } from "zod";

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

export const destinationSchema = z
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

export const updateCollectionSchema = z
  .object({
    stream_enabled: z.boolean(),
    old_image: z.boolean(),
    deletion_protection: z.boolean(),
    destinations: z.array(destinationSchema),
  })
  .refine((data) => !data.stream_enabled || data.destinations.length > 0, {
    message: "At least one destination is required when stream is enabled",
    path: ["destinations"],
  });

export type UpdateCollectionInput = z.infer<typeof updateCollectionSchema>;

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

export interface DestinationConfig {
  type: string;
  endpoint?: string;
  bearer_token?: string;
  event_types?: string[];
  filter_criteria?: FilterCriteria;
  region?: string;
  event_bus_name?: string;
  source?: string;
  index_name?: string;
}

export interface CollectionConfig {
  _id?: string;
  collection_name: string;
  primary_key?: string;
  sort_key?: string;
  stream_enabled: boolean;
  old_image: boolean;
  ttl_attribute?: string;
  destinations: DestinationConfig[];
  deletion_protection: boolean;
  created_at?: string;
  updated_at?: string;
}

export const collectionFormSchema = z.object({
  collection_name: z.string().min(1, "Collection name is required"),
  primary_key: z.string().optional(),
  sort_key: z.string().optional(),
  stream_enabled: z.boolean(),
  old_image: z.boolean(),
  ttl_attribute: z.string().optional(),
  destinations: z.array(destinationSchema),
  deletion_protection: z.boolean(),
});

export type CollectionForm = z.infer<typeof collectionFormSchema>;

export type FieldFilter = z.infer<typeof fieldFilterSchema>;
export type Condition = z.infer<typeof conditionSchema>;