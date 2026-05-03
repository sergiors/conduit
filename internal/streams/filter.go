package streams

import (
	"fmt"
	"reflect"
	"strings"
)

// FilterCondition is a single filter condition for image fields
type FilterCondition struct {
	Prefix      *string `bson:"prefix,omitempty"      json:"prefix,omitempty"`
	Suffix      *string `bson:"suffix,omitempty"      json:"suffix,omitempty"`
	Exists      *bool   `bson:"exists,omitempty"      json:"exists,omitempty"`
	Numeric     []any   `bson:"numeric,omitempty"     json:"numeric,omitempty"`
	AnythingBut any     `bson:"anything-but,omitempty" json:"anything-but,omitempty"`
}

// ImageFilter maps field names to a list of filter conditions (OR within field)
type ImageFilter map[string][]FilterCondition

// FilterCriteria specifies optional filters for oldImage and newImage
type FilterCriteria struct {
	OldImage ImageFilter `bson:"oldImage,omitempty" json:"oldImage,omitempty"`
	NewImage ImageFilter `bson:"newImage,omitempty" json:"newImage,omitempty"`
}

// MatchImage checks whether an image (oldImage or newImage) matches the given filter.
// Returns true if the filter is empty (no filter = pass everything).
// All declared fields must match (AND), and within each field at least one condition must match (OR).
func MatchImage(image interface{}, filter ImageFilter) bool {
	if len(filter) == 0 {
		return true
	}
	if image == nil {
		return false
	}
	img, ok := image.(map[string]interface{})
	if !ok {
		return false
	}
	for field, conditions := range filter {
		if !matchField(img, field, conditions) {
			return false
		}
	}
	return true
}

func matchField(img map[string]interface{}, field string, conditions []FilterCondition) bool {
	val, exists := img[field]
	for _, cond := range conditions {
		if matchCondition(val, exists, cond) {
			return true
		}
	}
	return len(conditions) == 0
}

func matchCondition(val interface{}, exists bool, cond FilterCondition) bool {
	if cond.Exists != nil {
		return exists == *cond.Exists
	}
	if !exists {
		return false
	}
	if cond.Prefix != nil {
		return strings.HasPrefix(fmt.Sprint(val), *cond.Prefix)
	}
	if cond.Suffix != nil {
		return strings.HasSuffix(fmt.Sprint(val), *cond.Suffix)
	}
	if cond.Numeric != nil {
		return matchNumeric(val, cond.Numeric)
	}
	if cond.AnythingBut != nil {
		return matchAnythingBut(val, cond.AnythingBut)
	}
	return false
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
