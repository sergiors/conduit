package collections

import (
	"fmt"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
)

// FilterCondition is a single filter condition for image fields.
type FilterCondition struct {
	Prefix      *string `bson:"prefix,omitempty"       json:"prefix,omitempty"`
	Suffix      *string `bson:"suffix,omitempty"       json:"suffix,omitempty"`
	Exists      *bool   `bson:"exists,omitempty"       json:"exists,omitempty"`
	Numeric     []any   `bson:"numeric,omitempty"      json:"numeric,omitempty"`
	AnythingBut any     `bson:"anything-but,omitempty" json:"anything-but,omitempty"`
}

// ImageFilter maps field names to a filter condition (AND across fields, AND within condition).
type ImageFilter map[string]FilterCondition

// FilterCriteria specifies optional filters for old_image and new_image.
type FilterCriteria struct {
	OldImage ImageFilter `bson:"old_image,omitempty" json:"old_image,omitempty"`
	NewImage ImageFilter `bson:"new_image,omitempty" json:"new_image,omitempty"`
}

// MatchImage checks whether an image (old_image or new_image) matches the given filter.
// Returns true if the filter is empty (no filter = pass everything).
// All declared fields must match (AND), and all conditions within a field must match (AND).
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
	val, exists := img[field]
	return matchCondition(val, exists, cond)
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
	if cond.Prefix != nil && !strings.HasPrefix(fmt.Sprint(val), *cond.Prefix) {
		return false
	}
	if cond.Suffix != nil && !strings.HasSuffix(fmt.Sprint(val), *cond.Suffix) {
		return false
	}
	if cond.Numeric != nil && !matchNumeric(val, cond.Numeric) {
		return false
	}
	if cond.AnythingBut != nil && !matchAnythingBut(val, cond.AnythingBut) {
		return false
	}
	return true
}

// noValueConditionsOnly returns true when no value-dependent conditions are set.
func (cond FilterCondition) noValueConditionsOnly() bool {
	return cond.Prefix == nil && cond.Suffix == nil && cond.Numeric == nil && cond.AnythingBut == nil
}

func matchNumeric(val interface{}, op []any) bool {
	if len(op) < 2 {
		return false
	}
	operator, ok := op[0].(string)
	if !ok {
		return false
	}
	a, aOK := toFloat64(val)
	b, bOK := toFloat64(op[1])
	if !aOK || !bOK {
		return false
	}
	switch operator {
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	case "=":
		return a == b
	}
	return false
}

func matchAnythingBut(val interface{}, ref any) bool {
	rv := reflect.ValueOf(ref)
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			if reflect.DeepEqual(val, rv.Index(i).Interface()) {
				return false
			}
		}
		return true
	}
	return !reflect.DeepEqual(val, ref)
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
