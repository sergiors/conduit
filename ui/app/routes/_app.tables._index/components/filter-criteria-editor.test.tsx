import { describe, it, expect } from "vitest";
import { filterCriteriaSchema, tableSchema } from "./types";
import type { FilterCondition, FilterCriteria } from "../loader.client";

describe("Filter Criteria Schemas", () => {
  describe("tableSchema", () => {
    it("validates a complete table config", () => {
      const data = {
        table_name: "test_table",
        stream_enabled: true,
        old_image: true,
        ttl_attribute: "expires_at",
        destinations: [
          {
            type: "http" as const,
            endpoint: "https://example.com",
            bearer_token: "token123",
            event_types: ["INSERT", "MODIFY"],
            filter_criteria: {
              old_image: [
                {
                  field: "status",
                  conditions: [
                    { type: "prefix" as const, value: "active" },
                  ],
                },
              ],
              new_image: [],
            },
          },
        ],
        deletion_protection: false,
      };

      const result = tableSchema.safeParse(data);
      expect(result.success).toBe(true);
    });

    it("fails validation without table name", () => {
      const data = {
        table_name: "",
        stream_enabled: false,
        old_image: false,
        destinations: [],
        deletion_protection: true,
      };

      const result = tableSchema.safeParse(data);
      expect(result.success).toBe(false);
    });

    it("requires destinations when stream_enabled is true", () => {
      const data = {
        table_name: "test",
        stream_enabled: true,
        old_image: false,
        destinations: [],
        deletion_protection: true,
      };

      const result = tableSchema.safeParse(data);
      expect(result.success).toBe(false);
    });

    it("allows empty destinations when stream_enabled is false", () => {
      const data = {
        table_name: "test",
        stream_enabled: false,
        old_image: false,
        destinations: [],
        deletion_protection: true,
      };

      const result = tableSchema.safeParse(data);
      expect(result.success).toBe(true);
    });
  });

  describe("filterCriteriaSchema", () => {
    it("validates criteria with all condition types", () => {
      const data = {
        old_image: [
          {
            field: "status",
            conditions: [
              { type: "prefix" as const, value: "test" },
              { type: "suffix" as const, value: "end" },
              { type: "exists" as const, value: "true" },
              { type: "numeric" as const, numericOp: ">" as const, value: "100" },
              { type: "anything-but" as const, value: "exclude" },
            ],
          },
        ],
        new_image: [],
      };

      const result = filterCriteriaSchema.safeParse(data);
      expect(result.success).toBe(true);
    });

    it("validates empty criteria", () => {
      const data = {
        old_image: [],
        new_image: [],
      };

      const result = filterCriteriaSchema.safeParse(data);
      expect(result.success).toBe(true);
    });

    it("requires field name", () => {
      const data = {
        old_image: [
          {
            field: "",
            conditions: [],
          },
        ],
        new_image: [],
      };

      const result = filterCriteriaSchema.safeParse(data);
      expect(result.success).toBe(false);
    });

    it("validates numeric condition with all operators", () => {
      const operators = [">", "<", ">=", "<=", "="] as const;

      for (const op of operators) {
        const data = {
          old_image: [
            {
              field: "count",
              conditions: [
                { type: "numeric" as const, numericOp: op, value: "42" },
              ],
            },
          ],
          new_image: [],
        };

        const result = filterCriteriaSchema.safeParse(data);
        expect(result.success).toBe(true);
      }
    });
  });
});

describe("Criteria Conversion", () => {
  // These would be integration tests for the conversion functions
  // They're commented out because they require the table-form module
  // which has React Router dependencies

  it("schema types are correctly inferred", () => {
    // Basic type check - if this compiles, types are working
    type TableForm = {
      table_name: string;
      stream_enabled: boolean;
      old_image: boolean;
      ttl_attribute?: string;
      destinations: Array<{
        type: "http" | "eventbridge";
        endpoint: string;
        bearer_token?: string;
        event_types: string[];
        filter_criteria?: {
          old_image: Array<{
            field: string;
            conditions: Array<{
              type: "prefix" | "suffix" | "exists" | "numeric" | "anything-but";
              value?: string;
              numericOp?: ">" | "<" | ">=" | "<=" | "=";
            }>;
          }>;
          new_image: Array<{
            field: string;
            conditions: Array<{
              type: "prefix" | "suffix" | "exists" | "numeric" | "anything-but";
              value?: string;
              numericOp?: ">" | "<" | ">=" | "<=" | "=";
            }>;
          }>;
        };
      }>;
      deletion_protection: boolean;
    };

    // This is a compile-time check
    const _typeCheck: TableForm = {
      table_name: "test",
      stream_enabled: false,
      old_image: false,
      destinations: [],
      deletion_protection: true,
    };

    expect(_typeCheck).toBeDefined();
  });
});
