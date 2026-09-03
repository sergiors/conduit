import { describe, expect, it } from "vitest";
import {
  computeColumns,
  computeKeyLabels,
  formatAttributeValue,
  isMissingValue,
} from "~/lib/document-columns";

const docs = [
  { _id: "1", pkVal: "a", skVal: "b", name: "one", tags: ["x", "y"] },
  { _id: "2", pkVal: "c", skVal: "d", name: "two", meta: { count: 2 } },
];

describe("computeColumns", () => {
  it("puts partition key first and distinct sort key second", () => {
    expect(computeColumns(docs, "pkVal", "skVal")).toEqual([
      "pkVal",
      "skVal",
      "_id",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("shows _id first when no key attributes are configured", () => {
    expect(computeColumns(docs, undefined, undefined)).toEqual([
      "_id",
      "pkVal",
      "skVal",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("shows _id first when partition key config is empty", () => {
    expect(computeColumns(docs, "", "")).toEqual([
      "_id",
      "pkVal",
      "skVal",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("does not duplicate the sort key when it equals the partition key", () => {
    expect(computeColumns(docs, "pkVal", "pkVal")).toEqual([
      "pkVal",
      "_id",
      "skVal",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("shows only partition key when only it is configured", () => {
    expect(computeColumns(docs, "pkVal", undefined)).toEqual([
      "pkVal",
      "_id",
      "skVal",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("includes _id later when pk/sk are configured and _id is not a key", () => {
    expect(computeColumns(docs, "pkVal", "skVal")).toContain("_id");
    expect(computeColumns(docs, "pkVal", "skVal")[0]).toBe("pkVal");
  });

  it("keeps _id first when it is itself the configured partition key", () => {
    expect(computeColumns(docs, "_id", "skVal")).toEqual([
      "_id",
      "skVal",
      "pkVal",
      "name",
      "tags",
      "meta",
    ]);
  });

  it("handles empty document pages", () => {
    expect(computeColumns([], "pkVal", "skVal")).toEqual(["pkVal", "skVal"]);
    expect(computeColumns([], undefined, undefined)).toEqual(["_id"]);
  });

  it("orders remaining attributes by first-seen, deduped", () => {
    const order = computeColumns(docs, "skVal", undefined);
    const duplicates = order.filter((k) => order.indexOf(k) !== order.lastIndexOf(k));
    expect(duplicates).toEqual([]);
  });
});

describe("computeKeyLabels", () => {
  it("maps distinct pk/sk", () => {
    expect(computeKeyLabels("pk", "sk")).toEqual({ pk: "PK", sk: "SK" });
  });

  it("omits sort key label when it equals the partition key", () => {
    expect(computeKeyLabels("key", "key")).toEqual({ key: "PK" });
  });

  it("returns empty map for no keys", () => {
    expect(computeKeyLabels(undefined, undefined)).toEqual({});
  });
});

describe("formatAttributeValue", () => {
  it("renders en dash for missing values", () => {
    expect(formatAttributeValue(undefined)).toBe("\u2013");
    expect(isMissingValue(undefined)).toBe(true);
    expect(isMissingValue(null)).toBe(false);
  });

  it("renders null explicitly", () => {
    expect(formatAttributeValue(null)).toBe("null");
  });

  it("stringifies objects/arrays compactly", () => {
    expect(formatAttributeValue({ a: 1 })).toBe('{"a":1}');
    expect(formatAttributeValue(["a", "b"])).toBe('["a","b"]');
  });

  it("truncates long stringified values", () => {
    const long = "x".repeat(300);
    const out = formatAttributeValue(long);
    expect(out.length).toBeLessThan(150);
    expect(out.endsWith("\u2026")).toBe(true);
  });

  it("renders primitive values", () => {
    expect(formatAttributeValue(42)).toBe("42");
    expect(formatAttributeValue(true)).toBe("true");
  });
});
