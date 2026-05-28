import { z } from "zod";

export const collectionFormSchema = z.object({
  collection_name: z.string().min(1, "Collection name is required"),
  partition_key: z.string().optional(),
  sort_key: z.string().optional(),
  stream_enabled: z.boolean(),
  old_image: z.boolean(),
  ttl_attribute: z.string().optional(),
  deletion_protection: z.boolean(),
});

export type CollectionForm = z.infer<typeof collectionFormSchema>;

export const updateCollectionSchema = z.object({
  stream_enabled: z.boolean(),
  old_image: z.boolean(),
  ttl_attribute: z.string().optional(),
  deletion_protection: z.boolean(),
});

export type UpdateCollectionForm = z.infer<typeof updateCollectionSchema>;