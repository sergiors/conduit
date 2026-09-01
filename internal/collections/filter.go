package collections

import (
	"fmt"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// FilterCondition is a single filter condition for image fields.
type FilterCondition struct {
	Exists     *bool `bson:"exists,omitempty"     json:"exists,omitempty"`
	Eq         any   `bson:"eq,omitempty"         json:"eq,omitempty"`
	Ne         any   `bson:"ne,omitempty"         json:"ne,omitempty"`
	Gt         any   `bson:"gt,omitempty"         json:"gt,omitempty"`
	Gte        any   `bson:"gte,omitempty"        json:"gte,omitempty"`
	Lt         any   `bson:"lt,omitempty"         json:"lt,omitempty"`
	Lte        any   `bson:"lte,omitempty"        json:"lte,omitempty"`
	Contains   any   `bson:"contains,omitempty"   json:"contains,omitempty"`
	StartsWith any   `bson:"startsWith,omitempty" json:"startsWith,omitempty"`
	EndsWith   any   `bson:"endsWith,omitempty"  json:"endsWith,omitempty"`
	In         []any `bson:"in,omitempty"         json:"in,omitempty"`
	NotIn      []any `bson:"notIn,omitempty"     json:"notIn,omitempty"`
}

// ImageFilter maps field names to a filter condition (AND across fields, AND within condition).
type ImageFilter map[string]FilterCondition

// Filter is a declarative filter over an event's images, combined with AND.
type Filter struct {
	OldImage ImageFilter `bson:"oldImage,omitempty" json:"oldImage,omitempty"`
	NewImage ImageFilter `bson:"newImage,omitempty" json:"newImage,omitempty"`
}

// Matches evaluates the filter against an event's two images.
func (c *Filter) Matches(newImage, oldImage interface{}) bool {
	if c == nil {
		return true
	}
	if len(c.NewImage) > 0 && !MatchImage(newImage, c.NewImage) {
		return false
	}
	if len(c.OldImage) > 0 && !MatchImage(oldImage, c.OldImage) {
		return false
	}
	return true
}

// MatchImage checks whether a single image (oldImage or newImage) matches
// the given filter.
func MatchImage(image interface{}, filter ImageFilter) bool {
	if len(filter) == 0 {
		return true
	}
	if image == nil {
		return false
	}
	img, ok := toMap(image)
	if !ok {
		return false
	}
	for field, cond := range filter {
		if !matchField(img, field, cond) {
			return false
		}
	}
	return true
}

func toMap(image interface{}) (map[string]interface{}, bool) {
	switch v := image.(type) {
	case map[string]interface{}:
		return v, true
	case bson.M:
		return map[string]interface{}(v), true
	}
	return nil, false
}

func matchField(img map[string]interface{}, field string, cond FilterCondition) bool {
	val, exists := resolvePath(img, field)
	return matchCondition(val, exists, cond)
}

// resolvePath reads a field value, supporting dotted nested paths such as "address.city".
func resolvePath(img map[string]interface{}, path string) (interface{}, bool) {
	if !strings.Contains(path, ".") {
		val, ok := img[path]
		return val, ok
	}
	var cur interface{} = img
	for _, part := range strings.Split(path, ".") {
		m, ok := toMap(cur)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func matchCondition(val interface{}, exists bool, cond FilterCondition) bool {
	if cond.Exists != nil {
		if exists != *cond.Exists {
			return false
		}
	}
	if !exists {
		// Field doesn't exist — if Exists check passed (or wasn't set), no other condition can match
		return cond.noValueConditionsOnly()
	}

	// Every present operator must match (AND within field).
	if cond.Eq != nil && !deepEqual(val, cond.Eq) {
		return false
	}
	if cond.Ne != nil && deepEqual(val, cond.Ne) {
		return false
	}
	if cond.Gt != nil && !matchNumericCompare(val, cond.Gt, func(a, b float64) bool { return a > b }) {
		return false
	}
	if cond.Gte != nil && !matchNumericCompare(val, cond.Gte, func(a, b float64) bool { return a >= b }) {
		return false
	}
	if cond.Lt != nil && !matchNumericCompare(val, cond.Lt, func(a, b float64) bool { return a < b }) {
		return false
	}
	if cond.Lte != nil && !matchNumericCompare(val, cond.Lte, func(a, b float64) bool { return a <= b }) {
		return false
	}
	if cond.Contains != nil && !matchContains(val, cond.Contains) {
		return false
	}
	if cond.StartsWith != nil && !strings.HasPrefix(fmt.Sprint(val), fmt.Sprint(cond.StartsWith)) {
		return false
	}
	if cond.EndsWith != nil && !strings.HasSuffix(fmt.Sprint(val), fmt.Sprint(cond.EndsWith)) {
		return false
	}
	if cond.In != nil && !matchIn(val, cond.In) {
		return false
	}
	if cond.NotIn != nil && matchIn(val, cond.NotIn) {
		return false
	}
	return true
}

// noValueConditionsOnly returns true when no value-dependent conditions are set.
func (cond FilterCondition) noValueConditionsOnly() bool {
	return cond.Eq == nil &&
		cond.Ne == nil &&
		cond.Gt == nil &&
		cond.Gte == nil &&
		cond.Lt == nil &&
		cond.Lte == nil &&
		cond.Contains == nil &&
		cond.StartsWith == nil &&
		cond.EndsWith == nil &&
		cond.In == nil &&
		cond.NotIn == nil
}

// matchNumericCompare applies a numeric comparison between val and operand.
// Both must be numeric-convertible; otherwise it returns false.
func matchNumericCompare(val, operand interface{}, cmp func(a, b float64) bool) bool {
	a, aOK := toFloat64(val)
	b, bOK := toFloat64(operand)
	if !aOK || !bOK {
		return false
	}
	return cmp(a, b)
}

// deepEqual compares two values. Numbers are compared numerically across
// int/float widths (via toFloat64); all other values use exact reflect.DeepEqual
// semantics (so the string "5" does not equal the number 5).
func deepEqual(a, b interface{}) bool {
	af, aOK := toFloat64(a)
	bf, bOK := toFloat64(b)
	if aOK && bOK {
		return af == bf
	}
	return reflect.DeepEqual(a, b)
}

// matchContains implements the contains operator:
//   - string value → substring match (strings.Contains).
//   - slice value at the field path → true when any element deep-equals the operand.
//   - any other kind → false.
func matchContains(val, operand interface{}) bool {
	if s, ok := val.(string); ok {
		sub, ok := operand.(string)
		return ok && strings.Contains(s, sub)
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			if deepEqual(rv.Index(i).Interface(), operand) {
				return true
			}
		}
	}
	return false
}

// matchIn reports whether any element of the operand array deep-equals val.
func matchIn(val interface{}, operand []any) bool {
	for _, item := range operand {
		if deepEqual(val, item) {
			return true
		}
	}
	return false
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
