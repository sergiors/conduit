# Filter

`filter` is filter DSL for Conduit change events. It is **not** a
MongoDB query language: field names are document field paths, and the operators
below are evaluated by Conduit against the `newImage` / `oldImage` of each
change event. Nothing here is translated to a MongoDB query.

---

## Overview

`filter` is **declarative**:

- **No `filter`, no constraints.** A sink created without a `filter` (or with an
  empty one) receives **every event**. Defining a filter never *adds* events —
  it only restricts: an event is delivered only when the filter matches.
- **AND across everything.** An event is delivered only when *every* declared
  criterion matches — across fields and within a single field's conditions.
- **Images are evaluated independently.** `oldImage` and `newImage` are
  filtered separately; a filter block you don't declare is ignored.
- **An absent image makes a declared filter `false`.** A content predicate
  requires content to match. So an `oldImage` filter never matches `INSERT`
  events (or collections streaming with `oldImage=false`), and a `newImage`
  filter never matches `REMOVE` events. This is intentional and decoupled from
  the collection's current configuration.

```json
{
  "filter": {
    "newImage": {
      "status": { "eq": "active" }
    }
  }
}
```

---

## Operators

There are 12 operators. Each is a key inside a field's condition object.

| Operator                  | JSON example                                        | Value type | Semantics                                                                 |
| ------------------------- | --------------------------------------------------- | ---------- | ------------------------------------------------------------------------- |
| `eq`                      | `{ "eq": "active" }`                                | any        | Deep equality. Numbers compare numerically across int/float widths; strings are exact. |
| `ne`                      | `{ "ne": "deleted" }`                               | any        | Inverse of `eq`. Requires the field to exist.                             |
| `gt`                      | `{ "gt": 18 }`                                      | number     | Value > operand. Both must be numeric.                                    |
| `gte`                     | `{ "gte": 18 }`                                     | number     | Value >= operand. Both must be numeric.                                  |
| `lt`                      | `{ "lt": 65 }`                                      | number     | Value < operand. Both must be numeric.                                   |
| `lte`                     | `{ "lte": 65 }`                                     | number     | Value <= operand. Both must be numeric.                                  |
| `contains`                | `{ "contains": "gmail" }`                          | any        | String value → substring match. Slice value → any element deep-equals the operand. Other kinds → `false`. |
| `startsWith`             | `{ "startsWith": "blocked_" }`                     | any        | Value string starts with the operand.                                    |
| `endsWith`               | `{ "endsWith": ".pdf" }`                           | any        | Value string ends with the operand.                                      |
| `exists`                  | `{ "exists": true }`                                | bool       | Field presence. Combines with value conditions (AND).                   |
| `in`                      | `{ "in": ["us-east-1", "eu-west-1"] }`              | array      | Any element deep-equals the value. Requires the field to exist.          |
| `notIn`                  | `{ "notIn": ["us-east-1", "eu-west-1"] }`           | array      | No element deep-equals the value. Requires the field to exist.          |

### Operator names

`eq` / `ne` / `gt` / `gte` / `lt` / `lte` intentionally follow the abbreviated
conventions familiar from MongoDB (`eq` / `ne` / `gt` / `gte` / `lt` / `lte`)
and DynamoDB (`EQ` / `NE` / `GT` / `GE` / `LT` / `LE`) — not to replicate those
APIs but to reduce the learning curve with well-known names. `startsWith` /
`endsWith` read as plain English. The goal is familiarity, not parity.

### Notes

- **Value-dependent operators require the field to exist.** `ne`,
  `in`, `notIn`, and the numeric comparisons evaluate to `false` when the
  field is missing — a missing field cannot satisfy a content predicate. Use
  `exists` to reason about presence explicitly.
- **`eq` on numbers is numeric.** `int32(5)` equals `float64(5.0)` in DSL
  terms. `eq` on strings is exact: the string `"5"` does **not** equal the
  number `5`.
- **`contains` on a string** is a substring match. **`contains` on a slice**
  (e.g. a `tags` array) matches when any element deep-equals the operand. For
  any other value kind it is `false`.

---

## Nested Object Paths

Field names may use dot notation to reach nested objects:

```json
{
  "newImage": {
    "address.city": { "eq": "Berlin" }
  }
}
```

`address.city` reads `newImage["address"]["city"]` recursively through nested
maps. A missing intermediate segment is treated as field-absent — the same
semantics as a missing top-level field (so value-dependent operators fail, and
`exists: false` matches). Top-level behavior is unchanged when no dots are
present.

---

## Supported Value Types

- **string** — for `eq`, `ne`, `contains`, `startsWith`,
  `endsWith`, `in`, `notIn`.
- **number** (int / float) — for `eq`, `ne`, `in`, `notIn`, and
  the numeric comparison operators (`gt`, etc.). JSON numbers arrive
  as floats; ints and floats compare numerically.
- **bool** — for `eq`, `ne`, `in`, `notIn`, and `exists`.
- **null** — handled via `exists: false`. A `null`/missing value is treated as
  absent; there is no `eq: null` operator.

---

## Examples

### `eq`

```json
{ "newImage": { "status": { "eq": "active" } } }
```

### `ne`

```json
{ "oldImage": { "status": { "ne": "deleted" } } }
```

### `gt` / `gte` / `lt` / `lte`

```json
{ "newImage": { "age": { "gt": 18 } } }
{ "newImage": { "age": { "gte": 18 } } }
{ "newImage": { "age": { "lt": 65 } } }
{ "newImage": { "age": { "lte": 65 } } }
```

### `contains`

```json
{ "newImage": { "email": { "contains": "gmail" } } }
{ "newImage": { "tags": { "contains": "go" } } }
```

### `startsWith` / `endsWith`

```json
{ "newImage": { "status": { "startsWith": "blocked_" } } }
{ "newImage": { "file": { "endsWith": ".pdf" } } }
```

### `exists`

```json
{ "newImage": { "email": { "exists": true } } }
{ "oldImage": { "email": { "exists": false } } }
```

### `in` / `notIn`

```json
{ "newImage": { "region": { "in": ["us-east-1", "eu-west-1"] } } }
{ "newImage": { "region": { "notIn": ["us-east-1", "eu-west-1"] } } }
```

---

## Combining Criteria

Multiple criteria within a field are ANDed, and multiple fields are ANDed:

```json
{
  "newImage": {
    "status": { "eq": "active", "startsWith": "act" },
    "age": { "gt": 18 },
    "region": { "in": ["us-east-1", "eu-west-1"] }
  }
}
```

This matches only when `status` is `"active"` **and** starts with `"act"`,
`age` is greater than 18, **and** `region` is one of the listed values.

---

## Composition: Multiple Sinks

The DSL is intentionally **non-recursive**. A filter is a single flat AND across
its declared image blocks: every declared predicate must match. There is no
`and` / `or` combinator and no notion of nesting depth.

Complex boolean expressions — anything beyond "all of these must hold" — are
modeled by creating **multiple sinks** on the same destination:

> **Sink A** — status is `ACTIVE` on the new image:

```json
{
  "filter": {
    "newImage": { "status": { "eq": "ACTIVE" } }
  }
}
```

> **Sink B** — tenant is `acme` and status is `ACTIVE` on the new image:

```json
{
  "filter": {
    "newImage": {
      "tenant": { "eq": "acme" },
      "status": { "eq": "ACTIVE" }
    }
  }
}
```

Delivering to the same endpoint from two sinks yields the union of what each
filter alone would deliver — expressing OR without a DSL `or`. To get "A **or**
B" you configure both sinks; to get "A **and** B" you put both predicates in one
sink or chain them. The exact semantics a set of sinks encodes depend on whether
the underlying transport deduplicates (webhooks fire per sink; a message queue
sink may already coalesce).

The rationale for this simplicity:

- a **smaller API** — two scalar image blocks instead of a recursive tree;
- **simpler validation and evaluation** — no nesting-depth cap, no empty-group
  edge cases, no recursive traversal;
- **simpler docs and UI** — one flat shape to explain and render;
- **complex expressions remain possible** without growing the DSL — multiple
  sinks compose the same boolean space.

### Full flat example

```json
{
  "filter": {
    "newImage": {
      "tenant": { "eq": "acme" },
      "status": { "eq": "ACTIVE" }
    },
    "oldImage": {
      "deleted": { "eq": false }
    }
  }
}
```

An event is delivered only when the new image's `tenant` is `acme` **and** its
`status` is `ACTIVE` **and** the old image's `deleted` is `false`. A declared
block whose image is absent (e.g. `oldImage` on an `INSERT`) evaluates to
`false`, so this filter never matches an `INSERT`.

---

## Using `newImage` Only / `oldImage` Only / Both

Filter only the new image:

```json
{
  "filter": {
    "newImage": { "status": { "eq": "active" } }
  }
}
```

Filter only the old image:

```json
{
  "filter": {
    "oldImage": { "status": { "eq": "pending" } }
  }
}
```

Filter both (both must match):

```json
{
  "filter": {
    "oldImage": { "status": { "ne": "active" } },
    "newImage": { "status": { "eq": "blocked" } }
  }
}
```

---

## Absent-Image Behavior

- A **`REMOVE`** event has no `newImage`; a `newImage` filter never matches it.
- An **`oldImage`** filter never matches `INSERT` events, or collections
  streaming with `oldImage=false` (no pre-image is recorded).
- This is **decoupled from the collection's current configuration**: sink
  creation does not reject an `oldImage` filter on a collection currently
  streaming with `oldImage=false`. If the stream is later re-enabled with
  `oldImage=true`, the existing sink simply starts matching. Until then,
  unmatched events are silently (by design) not delivered to that sink.
