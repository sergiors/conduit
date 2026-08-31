package collections

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestMatchImageEmptyFilter(t *testing.T) {
	img := map[string]interface{}{"valor": 200, "arquivo": "doc.pdf"}
	assert.True(t, MatchImage(img, nil))
	assert.True(t, MatchImage(img, ImageFilter{}))
}

func TestMatchImageNilImage(t *testing.T) {
	f := ImageFilter{"valor": FilterCondition{Gt: float64(100)}}
	assert.False(t, MatchImage(nil, f))
}

func TestMatchImageNonMapImage(t *testing.T) {
	f := ImageFilter{"x": FilterCondition{}}
	assert.False(t, MatchImage("not-a-map", f))
	assert.False(t, MatchImage(42, f))
}

func TestMatchImageBSONM(t *testing.T) {
	img := bson.M{"email": "sergio@somosbeta.com.br", "name": "Sérgio"}
	f := ImageFilter{"email": FilterCondition{Exists: boolPtr(true)}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageStartsWith(t *testing.T) {
	img := map[string]interface{}{"usuario": "admin_joao"}
	prefix := "admin_"
	assert.True(t, MatchImage(img, ImageFilter{"usuario": FilterCondition{StartsWith: prefix}}))
	other := "user_"
	assert.False(t, MatchImage(img, ImageFilter{"usuario": FilterCondition{StartsWith: other}}))
}

func TestMatchImageEndsWith(t *testing.T) {
	img := map[string]interface{}{"arquivo": "documento.pdf"}
	suffix := ".pdf"
	assert.True(t, MatchImage(img, ImageFilter{"arquivo": FilterCondition{EndsWith: suffix}}))
	other := ".doc"
	assert.False(t, MatchImage(img, ImageFilter{"arquivo": FilterCondition{EndsWith: other}}))
}

func TestMatchImageExists(t *testing.T) {
	img := map[string]interface{}{"nome": "joao"}
	yes := true
	no := false
	assert.True(t, MatchImage(img, ImageFilter{"nome": FilterCondition{Exists: &yes}}))
	assert.False(t, MatchImage(img, ImageFilter{"email": FilterCondition{Exists: &yes}}))
	assert.True(t, MatchImage(img, ImageFilter{"email": FilterCondition{Exists: &no}}))
	assert.False(t, MatchImage(img, ImageFilter{"nome": FilterCondition{Exists: &no}}))
}

func TestMatchImageNumeric(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Gt: float64(100)}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Lt: float64(100)}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Gte: float64(200)}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Lte: float64(200)}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Eq: float64(200)}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Eq: float64(100)}}))
}

func TestMatchImageNumericIntTypes(t *testing.T) {
	assert.True(t, MatchImage(map[string]interface{}{"v": int(50)}, ImageFilter{"v": FilterCondition{Gt: float64(10)}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int32(50)}, ImageFilter{"v": FilterCondition{Gt: float64(10)}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int64(50)}, ImageFilter{"v": FilterCondition{Gt: float64(10)}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": float32(50)}, ImageFilter{"v": FilterCondition{Gte: float64(50)}}))
}

func TestMatchImageNotEqualsNotIn(t *testing.T) {
	img := map[string]interface{}{"estado": "SP"}
	assert.True(t, MatchImage(img, ImageFilter{"estado": FilterCondition{Ne: "RJ"}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": FilterCondition{Ne: "SP"}}))
	assert.True(t, MatchImage(img, ImageFilter{"estado": FilterCondition{NotIn: []any{"RJ", "MG"}}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": FilterCondition{NotIn: []any{"SP", "MG"}}}))
}

func TestMatchImageMultipleFieldsAnd(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf"}
	suffix := ".pdf"
	f := ImageFilter{
		"valor":   FilterCondition{Gt: float64(100)},
		"arquivo": FilterCondition{EndsWith: suffix},
	}
	assert.True(t, MatchImage(img, f))
	badSuffix := ".doc"
	f["arquivo"] = FilterCondition{EndsWith: badSuffix}
	assert.False(t, MatchImage(img, f))
}

func TestMatchImageAndWithinField(t *testing.T) {
	img := map[string]interface{}{"email": "maria@gmail.com"}
	prefix := "maria"
	suffix := "gmail.com"
	f := ImageFilter{"email": FilterCondition{StartsWith: prefix, EndsWith: suffix}}
	assert.True(t, MatchImage(img, f))

	no := false
	f = ImageFilter{"email": FilterCondition{Exists: &no}}
	assert.False(t, MatchImage(img, f))
}

func TestMatchImageExistsAndValueCondition(t *testing.T) {
	img := map[string]interface{}{"email": "admin@test.com"}
	yes := true
	prefix := "admin"
	f := ImageFilter{"email": FilterCondition{Exists: &yes, StartsWith: prefix}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageExtraFieldsIgnored(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf", "usuario": "admin"}
	f := ImageFilter{"valor": FilterCondition{Gt: float64(100)}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageNilValueWithExistsFalse(t *testing.T) {
	img := map[string]interface{}{"campo": nil}
	no := false
	assert.True(t, MatchImage(img, ImageFilter{"outro": FilterCondition{Exists: &no}}))
}

// --- Filter integration tests (simulates dispatch flow) ---

func TestFilterOnlyOldImage(t *testing.T) {
	criteria := Filter{
		OldImage: ImageFilter{"status": FilterCondition{StartsWith: "deleted_"}},
	}

	// INSERT: no oldImage → filter fails → event skipped
	assert.False(t, MatchImage(nil, criteria.OldImage))

	// MODIFY: oldImage matches → passes
	assert.True(t, MatchImage(map[string]interface{}{"status": "deleted_old"}, criteria.OldImage))

	// MODIFY: oldImage doesn't match → event skipped
	assert.False(t, MatchImage(map[string]interface{}{"status": "active"}, criteria.OldImage))

	// REMOVE: oldImage matches → passes
	assert.True(t, MatchImage(map[string]interface{}{"status": "deleted_old"}, criteria.OldImage))

	// new_image filter is empty → always passes
	assert.True(t, MatchImage(nil, criteria.NewImage))
	assert.True(t, MatchImage(map[string]interface{}{"x": 1}, criteria.NewImage))
}

func TestFilterOnlyNewImage(t *testing.T) {
	criteria := Filter{
		NewImage: ImageFilter{"valor": FilterCondition{Gt: float64(100)}},
	}

	// old_image filter is empty → always passes
	assert.True(t, MatchImage(nil, criteria.OldImage))

	// INSERT: newImage matches → passes
	assert.True(t, MatchImage(map[string]interface{}{"valor": float64(200)}, criteria.NewImage))

	// INSERT: newImage doesn't match → event skipped
	assert.False(t, MatchImage(map[string]interface{}{"valor": float64(50)}, criteria.NewImage))

	// REMOVE: no newImage → filter fails → event skipped
	assert.False(t, MatchImage(nil, criteria.NewImage))
}

func TestFilterBothImages(t *testing.T) {
	criteria := Filter{
		OldImage: ImageFilter{"status": FilterCondition{Ne: "active"}},
		NewImage: ImageFilter{"status": FilterCondition{StartsWith: "blocked_"}},
	}

	// Both match → passes
	assert.True(t, MatchImage(map[string]interface{}{"status": "inactive"}, criteria.OldImage))
	assert.True(t, MatchImage(map[string]interface{}{"status": "blocked_user"}, criteria.NewImage))

	// old matches, new doesn't → fails
	assert.True(t, MatchImage(map[string]interface{}{"status": "inactive"}, criteria.OldImage))
	assert.False(t, MatchImage(map[string]interface{}{"status": "active_user"}, criteria.NewImage))

	// old doesn't match, new matches → fails
	assert.False(t, MatchImage(map[string]interface{}{"status": "active"}, criteria.OldImage))
	assert.True(t, MatchImage(map[string]interface{}{"status": "blocked_user"}, criteria.NewImage))
}

func TestFilterEmpty(t *testing.T) {
	var criteria Filter
	img := map[string]interface{}{"x": 1}
	assert.True(t, MatchImage(img, criteria.OldImage))
	assert.True(t, MatchImage(img, criteria.NewImage))
}

func TestFilterAndWithinField(t *testing.T) {
	criteria := Filter{
		NewImage: ImageFilter{
			"email": FilterCondition{
				StartsWith: "maria",
				EndsWith:   "gmail.com",
			},
		},
	}

	assert.True(t, MatchImage(map[string]interface{}{"email": "maria@gmail.com"}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"email": "joao@gmail.com"}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"email": "maria@yahoo.com"}, criteria.NewImage))
}

// --- DSL operator tests ---

func TestMatchImageDSL_Equality(t *testing.T) {
	// equals: string / int / bool
	assert.True(t, MatchImage(map[string]interface{}{"status": "active"}, ImageFilter{"status": FilterCondition{Eq: "active"}}))
	assert.False(t, MatchImage(map[string]interface{}{"status": "active"}, ImageFilter{"status": FilterCondition{Eq: "pending"}}))
	assert.True(t, MatchImage(map[string]interface{}{"age": float64(30)}, ImageFilter{"age": FilterCondition{Eq: float64(30)}}))
	assert.True(t, MatchImage(map[string]interface{}{"age": int(30)}, ImageFilter{"age": FilterCondition{Eq: float64(30)}}))
	assert.True(t, MatchImage(map[string]interface{}{"enabled": true}, ImageFilter{"enabled": FilterCondition{Eq: true}}))
	assert.False(t, MatchImage(map[string]interface{}{"enabled": true}, ImageFilter{"enabled": FilterCondition{Eq: false}}))

	// equals on numbers compares numerically across int/float widths
	assert.True(t, MatchImage(map[string]interface{}{"age": int32(30)}, ImageFilter{"age": FilterCondition{Eq: float64(30)}}))

	// equals on strings is exact: "5" != 5
	assert.False(t, MatchImage(map[string]interface{}{"v": "5"}, ImageFilter{"v": FilterCondition{Eq: float64(5)}}))

	// not_equals
	assert.True(t, MatchImage(map[string]interface{}{"status": "active"}, ImageFilter{"status": FilterCondition{Ne: "pending"}}))
	assert.False(t, MatchImage(map[string]interface{}{"status": "active"}, ImageFilter{"status": FilterCondition{Ne: "active"}}))

	// not_equals on a missing field → false (value-dependent operator requires the field to exist)
	assert.False(t, MatchImage(map[string]interface{}{"status": "active"}, ImageFilter{"email": FilterCondition{Ne: "x@y.com"}}))
}

func TestMatchImageDSL_Comparisons(t *testing.T) {
	img := map[string]interface{}{"age": float64(30)}
	assert.True(t, MatchImage(img, ImageFilter{"age": FilterCondition{Gt: float64(20)}}))
	assert.False(t, MatchImage(img, ImageFilter{"age": FilterCondition{Gt: float64(30)}}))
	assert.True(t, MatchImage(img, ImageFilter{"age": FilterCondition{Gte: float64(30)}}))
	assert.False(t, MatchImage(img, ImageFilter{"age": FilterCondition{Gte: float64(31)}}))
	assert.True(t, MatchImage(img, ImageFilter{"age": FilterCondition{Lt: float64(40)}}))
	assert.False(t, MatchImage(img, ImageFilter{"age": FilterCondition{Lt: float64(30)}}))
	assert.True(t, MatchImage(img, ImageFilter{"age": FilterCondition{Lte: float64(30)}}))
	assert.False(t, MatchImage(img, ImageFilter{"age": FilterCondition{Lte: float64(29)}}))

	// int value compared against float operand
	assert.True(t, MatchImage(map[string]interface{}{"age": int(30)}, ImageFilter{"age": FilterCondition{Gt: float64(20)}}))

	// non-numeric value → false
	assert.False(t, MatchImage(map[string]interface{}{"age": "thirty"}, ImageFilter{"age": FilterCondition{Gt: float64(20)}}))
	// non-numeric operand → false
	assert.False(t, MatchImage(map[string]interface{}{"age": float64(30)}, ImageFilter{"age": FilterCondition{Gt: "twenty"}}))
}

func TestMatchImageDSL_Contains(t *testing.T) {
	// substring on string
	assert.True(t, MatchImage(map[string]interface{}{"email": "maria@gmail.com"}, ImageFilter{"email": FilterCondition{Contains: "gmail"}}))
	assert.False(t, MatchImage(map[string]interface{}{"email": "maria@gmail.com"}, ImageFilter{"email": FilterCondition{Contains: "yahoo"}}))

	// array element membership on []interface{}
	assert.True(t, MatchImage(map[string]interface{}{"tags": []interface{}{"go", "backend"}}, ImageFilter{"tags": FilterCondition{Contains: "go"}}))
	assert.False(t, MatchImage(map[string]interface{}{"tags": []interface{}{"go", "backend"}}, ImageFilter{"tags": FilterCondition{Contains: "rust"}}))

	// other kinds → false
	assert.False(t, MatchImage(map[string]interface{}{"age": float64(30)}, ImageFilter{"age": FilterCondition{Contains: "3"}}))
}

func TestMatchImageDSL_InNotIn(t *testing.T) {
	// in with mixed JSON types
	assert.True(t, MatchImage(map[string]interface{}{"region": "us-east-1"}, ImageFilter{"region": FilterCondition{In: []any{"us-west-2", "us-east-1"}}}))
	assert.False(t, MatchImage(map[string]interface{}{"region": "eu-west-1"}, ImageFilter{"region": FilterCondition{In: []any{"us-west-2", "us-east-1"}}}))
	assert.True(t, MatchImage(map[string]interface{}{"age": float64(30)}, ImageFilter{"age": FilterCondition{In: []any{float64(20), float64(30)}}}))

	// not_in inverse
	assert.True(t, MatchImage(map[string]interface{}{"region": "eu-west-1"}, ImageFilter{"region": FilterCondition{NotIn: []any{"us-west-2", "us-east-1"}}}))
	assert.False(t, MatchImage(map[string]interface{}{"region": "us-east-1"}, ImageFilter{"region": FilterCondition{NotIn: []any{"us-west-2", "us-east-1"}}}))

	// missing field → false
	assert.False(t, MatchImage(map[string]interface{}{"region": "us-east-1"}, ImageFilter{"country": FilterCondition{In: []any{"us", "br"}}}))
	assert.False(t, MatchImage(map[string]interface{}{"region": "us-east-1"}, ImageFilter{"country": FilterCondition{NotIn: []any{"us", "br"}}}))
}

func TestMatchImageDSL_StartsEndsWith(t *testing.T) {
	// starts_with / ends_with
	assert.True(t, MatchImage(map[string]interface{}{"status": "blocked_user"}, ImageFilter{"status": FilterCondition{StartsWith: "blocked_"}}))
	assert.False(t, MatchImage(map[string]interface{}{"status": "active_user"}, ImageFilter{"status": FilterCondition{StartsWith: "blocked_"}}))
	assert.True(t, MatchImage(map[string]interface{}{"file": "report.pdf"}, ImageFilter{"file": FilterCondition{EndsWith: ".pdf"}}))
	assert.False(t, MatchImage(map[string]interface{}{"file": "report.doc"}, ImageFilter{"file": FilterCondition{EndsWith: ".pdf"}}))
}

func TestMatchImageNestedPaths(t *testing.T) {
	img := map[string]interface{}{
		"address": map[string]interface{}{"city": "Berlin"},
	}
	// nested path equals
	assert.True(t, MatchImage(img, ImageFilter{"address.city": FilterCondition{Eq: "Berlin"}}))
	assert.False(t, MatchImage(img, ImageFilter{"address.city": FilterCondition{Eq: "Paris"}}))

	// missing intermediate → false
	assert.False(t, MatchImage(img, ImageFilter{"address.zip": FilterCondition{Eq: "10115"}}))
	assert.False(t, MatchImage(img, ImageFilter{"billing.city": FilterCondition{Eq: "Berlin"}}))

	// flat field still works
	assert.True(t, MatchImage(map[string]interface{}{"city": "Berlin"}, ImageFilter{"city": FilterCondition{Eq: "Berlin"}}))

	// nested path with exists
	yes := true
	assert.True(t, MatchImage(img, ImageFilter{"address.city": FilterCondition{Exists: &yes}}))
	assert.False(t, MatchImage(img, ImageFilter{"address.zip": FilterCondition{Exists: &yes}}))
}

func TestFilterJSONRoundTrip(t *testing.T) {
	criteria := Filter{
		NewImage: ImageFilter{
			"status": FilterCondition{Eq: "active"},
			"age":    FilterCondition{Gt: float64(18)},
			"region": FilterCondition{In: []any{"us-east-1", "eu-west-1"}},
			"email":  FilterCondition{Contains: "gmail"},
		},
		OldImage: ImageFilter{
			"status": FilterCondition{Ne: "deleted"},
		},
	}

	data, err := json.Marshal(criteria)
	assert.NoError(t, err)

	var decoded Filter
	assert.NoError(t, json.Unmarshal(data, &decoded))

	img := map[string]interface{}{
		"status": "active",
		"age":    float64(30),
		"region": "us-east-1",
		"email":  "maria@gmail.com",
	}
	assert.Equal(t, MatchImage(img, criteria.NewImage), MatchImage(img, decoded.NewImage))
	assert.True(t, MatchImage(img, decoded.NewImage))
	assert.Equal(t, MatchImage(img, criteria.OldImage), MatchImage(img, decoded.OldImage))
	assert.True(t, MatchImage(img, decoded.OldImage))
}

func boolPtr(b bool) *bool { return &b }

// --- Filter.Matches backward-compat (flat, no-or criteria) ---

func TestFilterMatchesNilReceiver(t *testing.T) {
	var c *Filter
	assert.True(t, c.Matches(map[string]interface{}{"x": 1}, map[string]interface{}{"y": 2}))
}

func TestFilterMatchesFlatBackwardCompat(t *testing.T) {
	criteria := Filter{
		NewImage: ImageFilter{"status": FilterCondition{StartsWith: "active"}},
		OldImage: ImageFilter{"status": FilterCondition{Eq: "pending"}},
	}
	// both images match -> true
	assert.True(t, criteria.Matches(
		map[string]interface{}{"status": "active_user"},
		map[string]interface{}{"status": "pending"},
	))
	// new image fails -> false
	assert.False(t, criteria.Matches(
		map[string]interface{}{"status": "inactive_user"},
		map[string]interface{}{"status": "pending"},
	))
	// new image declared but nil (REMOVE) -> false
	assert.False(t, criteria.Matches(
		nil,
		map[string]interface{}{"status": "pending"},
	))
	// empty criteria -> true
	assert.True(t, (&Filter{}).Matches(map[string]interface{}{"x": 1}, nil))
}

// --- Filter.Matches flat semantics (single AND across all declared blocks) ---

func TestFilterMatchesFlatSemantics(t *testing.T) {
	// The canonical flat example: new_image.tenant acme AND new_image.status
	// ACTIVE AND old_image.deleted false. All three predicates are ANDed.
	criteria := Filter{
		NewImage: ImageFilter{
			"tenant": FilterCondition{Eq: "acme"},
			"status": FilterCondition{Eq: "ACTIVE"},
		},
		OldImage: ImageFilter{
			"deleted": FilterCondition{Eq: false},
		},
	}

	// All three predicates satisfied -> delivered.
	assert.True(t, criteria.Matches(
		map[string]interface{}{"tenant": "acme", "status": "ACTIVE"},
		map[string]interface{}{"deleted": false},
	))

	// new_image.status PENDING -> NOT delivered.
	assert.False(t, criteria.Matches(
		map[string]interface{}{"tenant": "acme", "status": "PENDING"},
		map[string]interface{}{"deleted": false},
	))

	// old_image.deleted true -> NOT delivered.
	assert.False(t, criteria.Matches(
		map[string]interface{}{"tenant": "acme", "status": "ACTIVE"},
		map[string]interface{}{"deleted": true},
	))

	// INSERT (no old_image) with a declared old_image block -> NOT delivered.
	assert.False(t, criteria.Matches(
		map[string]interface{}{"tenant": "acme", "status": "ACTIVE"},
		nil,
	))

	// new_image.tenant differs -> NOT delivered even when the rest matches.
	assert.False(t, criteria.Matches(
		map[string]interface{}{"tenant": "other", "status": "ACTIVE"},
		map[string]interface{}{"deleted": false},
	))
}

// --- Filter.Matches absent-image behavior (flat, no or/and) ---

func TestFilterMatchesDeclaredBlockAbsentImage(t *testing.T) {
	// A declared old_image block never matches an INSERT (nil old image),
	// even though the new_image block matches. There is no or group to
	// compensate: all declared blocks are AND.
	criteria := Filter{
		OldImage: ImageFilter{"status": FilterCondition{Eq: "ACTIVE"}},
		NewImage: ImageFilter{"status": FilterCondition{Eq: "ACTIVE"}},
	}
	assert.False(t, criteria.Matches(
		map[string]interface{}{"status": "ACTIVE"},
		nil,
	))
	// Both images present and matching -> true.
	assert.True(t, criteria.Matches(
		map[string]interface{}{"status": "ACTIVE"},
		map[string]interface{}{"status": "ACTIVE"},
	))
	// new matches but old doesn't -> false (AND requires all).
	assert.False(t, criteria.Matches(
		map[string]interface{}{"status": "ACTIVE"},
		map[string]interface{}{"status": "PENDING"},
	))
}
