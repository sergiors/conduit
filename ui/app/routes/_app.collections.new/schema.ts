import { z } from "zod";

export const formSchema = z
  .object({
    collectionName: z
      .string()
      .min(1, "Collection name is required")
      .regex(
        /^[a-zA-Z0-9_-]+$/,
        "Only letters, numbers, underscores and hyphens allowed",
      ),
    compositeKeys: z.boolean(),
    partitionKey: z.string().optional(),
    sortKey: z.string().optional(),
    deletionProtection: z.boolean(),
  })
  .refine(
    (data) => {
      if (data.compositeKeys) {
        return !!data.partitionKey && data.partitionKey.length > 0;
      }
      return true;
    },
    {
      message: "Partition key is required for composite keys",
      path: ["partitionKey"],
    },
  )
  .refine(
    (data) => {
      if (data.sortKey && !data.partitionKey) {
        return false;
      }
      return true;
    },
    {
      message: "Partition key is required when sort key is set",
      path: ["sortKey"],
    },
  )
  .refine(
    (data) => {
      if (
        data.partitionKey &&
        data.sortKey &&
        data.partitionKey === data.sortKey
      ) {
        return false;
      }
      return true;
    },
    {
      message: "Sort key cannot be the same as partition key",
      path: ["sortKey"],
    },
  );

export type FormData = z.infer<typeof formSchema>;
