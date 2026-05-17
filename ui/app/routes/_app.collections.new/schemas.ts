import { z } from "zod";

export const createCollectionSchema = z
  .object({
    collection_name: z
      .string()
      .min(1, "Collection name is required")
      .regex(
        /^[a-zA-Z0-9_-]+$/,
        "Only letters, numbers, underscores and hyphens allowed",
      ),
    use_dynamodb_mode: z.boolean(),
    primary_key: z.string().optional(),
    sort_key: z.string().optional(),
  })
  .refine(
    (data) => {
      if (data.use_dynamodb_mode) {
        return !!data.primary_key && data.primary_key.length > 0;
      }
      return true;
    },
    {
      message: "Primary key is required in DynamoDB mode",
      path: ["primary_key"],
    },
  );

export type CreateCollectionInput = z.infer<typeof createCollectionSchema>;

export interface CreateCollectionPayload {
  collection_name: string;
  primary_key?: string;
  sort_key?: string;
  stream_enabled: false;
  old_image: false;
  destinations: [];
  deletion_protection: true;
}