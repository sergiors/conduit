package tables

import (
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
	f := ImageFilter{"valor": FilterCondition{Numeric: []any{">", float64(100)}}}
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

func TestMatchImagePrefix(t *testing.T) {
	img := map[string]interface{}{"usuario": "admin_joao"}
	prefix := "admin_"
	assert.True(t, MatchImage(img, ImageFilter{"usuario": FilterCondition{Prefix: &prefix}}))
	other := "user_"
	assert.False(t, MatchImage(img, ImageFilter{"usuario": FilterCondition{Prefix: &other}}))
}

func TestMatchImageSuffix(t *testing.T) {
	img := map[string]interface{}{"arquivo": "documento.pdf"}
	suffix := ".pdf"
	assert.True(t, MatchImage(img, ImageFilter{"arquivo": FilterCondition{Suffix: &suffix}}))
	other := ".doc"
	assert.False(t, MatchImage(img, ImageFilter{"arquivo": FilterCondition{Suffix: &other}}))
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
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{">", float64(100)}}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{"<", float64(100)}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{">=", float64(200)}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{"<=", float64(200)}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{"=", float64(200)}}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": FilterCondition{Numeric: []any{"=", float64(100)}}}))
}

func TestMatchImageNumericIntTypes(t *testing.T) {
	assert.True(t, MatchImage(map[string]interface{}{"v": int(50)}, ImageFilter{"v": FilterCondition{Numeric: []any{">", float64(10)}}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int32(50)}, ImageFilter{"v": FilterCondition{Numeric: []any{">", float64(10)}}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int64(50)}, ImageFilter{"v": FilterCondition{Numeric: []any{">", float64(10)}}}))
}

func TestMatchImageAnythingBut(t *testing.T) {
	img := map[string]interface{}{"estado": "SP"}
	assert.True(t, MatchImage(img, ImageFilter{"estado": FilterCondition{AnythingBut: "RJ"}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": FilterCondition{AnythingBut: "SP"}}))
	assert.True(t, MatchImage(img, ImageFilter{"estado": FilterCondition{AnythingBut: []any{"RJ", "MG"}}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": FilterCondition{AnythingBut: []any{"SP", "MG"}}}))
}

func TestMatchImageMultipleFieldsAnd(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf"}
	suffix := ".pdf"
	f := ImageFilter{
		"valor":   FilterCondition{Numeric: []any{">", float64(100)}},
		"arquivo": FilterCondition{Suffix: &suffix},
	}
	assert.True(t, MatchImage(img, f))
	badSuffix := ".doc"
	f["arquivo"] = FilterCondition{Suffix: &badSuffix}
	assert.False(t, MatchImage(img, f))
}

func TestMatchImageAndWithinField(t *testing.T) {
	img := map[string]interface{}{"email": "maria@gmail.com"}
	prefix := "maria"
	suffix := "gmail.com"
	f := ImageFilter{"email": FilterCondition{Prefix: &prefix, Suffix: &suffix}}
	assert.True(t, MatchImage(img, f))

	no := false
	f = ImageFilter{"email": FilterCondition{Exists: &no}}
	assert.False(t, MatchImage(img, f))
}

func TestMatchImageExistsAndValueCondition(t *testing.T) {
	img := map[string]interface{}{"email": "admin@test.com"}
	yes := true
	prefix := "admin"
	f := ImageFilter{"email": FilterCondition{Exists: &yes, Prefix: &prefix}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageExtraFieldsIgnored(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf", "usuario": "admin"}
	f := ImageFilter{"valor": FilterCondition{Numeric: []any{">", float64(100)}}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageNilValueWithExistsFalse(t *testing.T) {
	img := map[string]interface{}{"campo": nil}
	no := false
	assert.True(t, MatchImage(img, ImageFilter{"outro": FilterCondition{Exists: &no}}))
}

// --- FilterCriteria integration tests (simulates dispatch flow) ---

func TestFilterCriteriaOnlyOldImage(t *testing.T) {
	criteria := FilterCriteria{
		OldImage: ImageFilter{"status": FilterCondition{Prefix: strPtr("deleted_")}},
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

func TestFilterCriteriaOnlyNewImage(t *testing.T) {
	criteria := FilterCriteria{
		NewImage: ImageFilter{"valor": FilterCondition{Numeric: []any{">", float64(100)}}},
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

func TestFilterCriteriaBothImages(t *testing.T) {
	criteria := FilterCriteria{
		OldImage: ImageFilter{"status": FilterCondition{AnythingBut: "active"}},
		NewImage: ImageFilter{"status": FilterCondition{Prefix: strPtr("blocked_")}},
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

func TestFilterCriteriaEmpty(t *testing.T) {
	var criteria FilterCriteria
	img := map[string]interface{}{"x": 1}
	assert.True(t, MatchImage(img, criteria.OldImage))
	assert.True(t, MatchImage(img, criteria.NewImage))
}

func TestFilterCriteriaAndWithinField(t *testing.T) {
	criteria := FilterCriteria{
		NewImage: ImageFilter{
			"email": FilterCondition{
				Prefix: strPtr("maria"),
				Suffix: strPtr("gmail.com"),
			},
		},
	}

	assert.True(t, MatchImage(map[string]interface{}{"email": "maria@gmail.com"}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"email": "joao@gmail.com"}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"email": "maria@yahoo.com"}, criteria.NewImage))
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool   { return &b }
