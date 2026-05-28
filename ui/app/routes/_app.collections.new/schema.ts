import { z } from "zod";

export const formSchema = z
  .object({
    collection_name: z
      .string()
      .min(1, "Collection name is required")
      .regex(
        /^[a-zA-Z0-9_-]+$/,
        "Only letters, numbers, underscores and hyphens allowed",
      ),
    composite_keys: z.boolean(),
    partition_key: z.string().optional(),
    sort_key: z.string().optional(),
    deletion_protection: z.boolean(),
  })
  .refine(
    (data) => {
      if (data.composite_keys) {
        return !!data.partition_key && data.partition_key.length > 0;
      }
      return true;
    },
    {
      message: "Partition key is required for composite keys",
      path: ["partition_key"],
    },
  )
  .refine(
    (data) => {
      if (data.sort_key && !data.partition_key) {
        return false;
      }
      return true;
    },
    {
      message: "Partition key is required when sort key is set",
      path: ["sort_key"],
    },
  )
  .refine(
    (data) => {
      if (
        data.partition_key &&
        data.sort_key &&
        data.partition_key === data.sort_key
      ) {
        return false;
      }
      return true;
    },
    {
      message: "Sort key cannot be the same as partition key",
      path: ["sort_key"],
    },
  );

export type FormData = z.infer<typeof formSchema>;
