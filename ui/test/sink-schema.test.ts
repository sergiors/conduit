import { describe, expect, it } from "vitest";
import {
  emptySpecFor,
  filterToForm,
  formToFilter,
} from "~/routes/_app.collections.$collectionName.sinks/schema";

describe("formToFilter", () => {
  it("maps per-field conditions into the implicit-AND backend shape", () => {
    const result = formToFilter({
      oldImage: [
        {
          field: "name",
          conditions: [{ type: "eq", value: "alice" }],
        },
        {
          field: "age",
          conditions: [
            { type: "gte", value: "21" },
            { type: "lt", value: "65" },
          ],
        },
      ],
      newImage: [
        {
          field: "status",
          conditions: [{ type: "in", value: "a, b, c" }],
        },
        {
          field: "deleted",
          conditions: [{ type: "exists", value: "false" }],
        },
      ],
    });

    expect(result).toEqual({
      oldImage: {
        name: { eq: "alice" },
        age: { gte: "21", lt: "65" },
      },
      newImage: {
        status: { in: ["a", "b", "c"] },
        deleted: { exists: false },
      },
    });
  });

  it("omits fields with no conditions and empty images", () => {
    const result = formToFilter({
      oldImage: [],
      newImage: [
        { field: "x", conditions: [{ type: "endsWith", value: ".zip" }] },
      ],
    });
    expect(result).toEqual({ newImage: { x: { endsWith: ".zip" } } });
  });

  it("does not emit exists without a boolean value", () => {
    const result = formToFilter({
      oldImage: [{ field: "f", conditions: [{ type: "exists", value: "" }] }],
      newImage: [],
    });
    expect(result).toEqual({});
  });

  it("omits empty-string values for scalar operators", () => {
    const result = formToFilter({
      oldImage: [
        { field: "f", conditions: [{ type: "contains", value: "" }] },
      ],
      newImage: [],
    });
    expect(result).toEqual({});
  });
});

describe("filterToForm", () => {
  it("round-trips a backend filter into field lists", () => {
    const form = filterToForm({
      oldImage: { name: { ne: "x" }, age: { gt: 18 } },
      newImage: { status: { in: ["a", "b"] } },
    });
    expect(form.oldImage).toHaveLength(2);
    expect(form.newImage).toHaveLength(1);
    expect(form.newImage[0].field).toBe("status");
    expect(form.newImage[0].conditions[0].type).toBe("in");
    expect(form.newImage[0].conditions[0].value).toBe("a, b");
  });

  it("treats undefined filter as empty form", () => {
    expect(filterToForm(undefined)).toEqual({ oldImage: [], newImage: [] });
  });
});

describe("emptySpecFor", () => {
  it("produces the correct spec keys per type", () => {
    expect(emptySpecFor("http")).toEqual({ endpoint: "", bearerToken: "" });
    expect(emptySpecFor("eventbridge")).toEqual({
      eventBusName: "",
      source: "",
    });
    expect(emptySpecFor("meilisearch")).toEqual({
      host: "",
      apiKey: "",
      indexName: "",
    });
  });
});
