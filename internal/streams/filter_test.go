package streams

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchImageEmptyFilter(t *testing.T) {
	img := map[string]interface{}{"valor": 200, "arquivo": "doc.pdf"}
	assert.True(t, MatchImage(img, nil))
	assert.True(t, MatchImage(img, ImageFilter{}))
}

func TestMatchImageNilImage(t *testing.T) {
	f := ImageFilter{"valor": []FilterCondition{{Numeric: []any{">", float64(100)}}}}
	assert.False(t, MatchImage(nil, f))
}

func TestMatchImageNonMapImage(t *testing.T) {
	f := ImageFilter{"x": []FilterCondition{{}}}
	assert.False(t, MatchImage("not-a-map", f))
	assert.False(t, MatchImage(42, f))
}

func TestMatchImagePrefix(t *testing.T) {
	img := map[string]interface{}{"usuario": "admin_joao"}

	prefix := "admin_"
	assert.True(t, MatchImage(img, ImageFilter{"usuario": []FilterCondition{{Prefix: &prefix}}}))

	other := "user_"
	assert.False(t, MatchImage(img, ImageFilter{"usuario": []FilterCondition{{Prefix: &other}}}))
}

func TestMatchImagePrefixNonString(t *testing.T) {
	img := map[string]interface{}{"codigo": 12345}
	prefix := "123"
	assert.True(t, MatchImage(img, ImageFilter{"codigo": []FilterCondition{{Prefix: &prefix}}}))
}

func TestMatchImageSuffix(t *testing.T) {
	img := map[string]interface{}{"arquivo": "documento.pdf"}

	suffix := ".pdf"
	assert.True(t, MatchImage(img, ImageFilter{"arquivo": []FilterCondition{{Suffix: &suffix}}}))

	other := ".doc"
	assert.False(t, MatchImage(img, ImageFilter{"arquivo": []FilterCondition{{Suffix: &other}}}))
}

func TestMatchImageSuffixNonString(t *testing.T) {
	img := map[string]interface{}{"codigo": 999}
	suffix := "99"
	assert.True(t, MatchImage(img, ImageFilter{"codigo": []FilterCondition{{Suffix: &suffix}}}))
}

func TestMatchImageExists(t *testing.T) {
	img := map[string]interface{}{"nome": "joao"}
	yes := true
	no := false

	assert.True(t, MatchImage(img, ImageFilter{"nome": []FilterCondition{{Exists: &yes}}}))
	assert.False(t, MatchImage(img, ImageFilter{"email": []FilterCondition{{Exists: &yes}}}))
	assert.True(t, MatchImage(img, ImageFilter{"email": []FilterCondition{{Exists: &no}}}))
	assert.False(t, MatchImage(img, ImageFilter{"nome": []FilterCondition{{Exists: &no}}}))
}

func TestMatchImageExistsWithNilValue(t *testing.T) {
	img := map[string]interface{}{"campo": nil}
	yes := true
	no := false

	assert.True(t, MatchImage(img, ImageFilter{"campo": []FilterCondition{{Exists: &yes}}}))
	assert.False(t, MatchImage(img, ImageFilter{"campo": []FilterCondition{{Exists: &no}}}))
}

func TestMatchImageNumeric(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}

	assert.True(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{">", float64(100)}}}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{"<", float64(100)}}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{">=", float64(200)}}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{"<=", float64(200)}}}}))
	assert.True(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{"=", float64(200)}}}}))
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{"=", float64(100)}}}}))
}

func TestMatchImageNumericIntTypes(t *testing.T) {
	assert.True(t, MatchImage(map[string]interface{}{"v": int(50)}, ImageFilter{"v": []FilterCondition{{Numeric: []any{">", float64(10)}}}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int32(50)}, ImageFilter{"v": []FilterCondition{{Numeric: []any{">", float64(10)}}}}))
	assert.True(t, MatchImage(map[string]interface{}{"v": int64(50)}, ImageFilter{"v": []FilterCondition{{Numeric: []any{">", float64(10)}}}}))
}

func TestMatchImageNumericNonNumericValue(t *testing.T) {
	img := map[string]interface{}{"valor": "abc"}
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{">", float64(100)}}}}))
}

func TestMatchImageNumericInvalidOperator(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{"!=", float64(100)}}}}))
}

func TestMatchImageNumericMissingOperand(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{">"}}}}))
}

func TestMatchImageNumericNonStringOp(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}
	assert.False(t, MatchImage(img, ImageFilter{"valor": []FilterCondition{{Numeric: []any{42, float64(100)}}}}))
}

func TestMatchImageAnythingButScalar(t *testing.T) {
	img := map[string]interface{}{"estado": "SP"}

	assert.True(t, MatchImage(img, ImageFilter{"estado": []FilterCondition{{AnythingBut: "RJ"}}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": []FilterCondition{{AnythingBut: "SP"}}}))
}

func TestMatchImageAnythingButArray(t *testing.T) {
	img := map[string]interface{}{"estado": "SP"}

	assert.True(t, MatchImage(img, ImageFilter{"estado": []FilterCondition{{AnythingBut: []any{"RJ", "MG"}}}}))
	assert.False(t, MatchImage(img, ImageFilter{"estado": []FilterCondition{{AnythingBut: []any{"SP", "MG"}}}}))
}

func TestMatchImageAnythingButNumeric(t *testing.T) {
	img := map[string]interface{}{"codigo": float64(42)}

	assert.True(t, MatchImage(img, ImageFilter{"codigo": []FilterCondition{{AnythingBut: float64(99)}}}))
	assert.False(t, MatchImage(img, ImageFilter{"codigo": []FilterCondition{{AnythingBut: float64(42)}}}))
}

func TestMatchImageMultipleFieldsAnd(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf"}

	suffix := ".pdf"
	f := ImageFilter{
		"valor":   []FilterCondition{{Numeric: []any{">", float64(100)}}},
		"arquivo": []FilterCondition{{Suffix: &suffix}},
	}
	assert.True(t, MatchImage(img, f))

	badSuffix := ".doc"
	f["arquivo"] = []FilterCondition{{Suffix: &badSuffix}}
	assert.False(t, MatchImage(img, f))
}

func TestMatchImageOrWithinField(t *testing.T) {
	img := map[string]interface{}{"arquivo": "doc.pdf"}

	pdfSuffix := ".pdf"
	docSuffix := ".doc"
	f := ImageFilter{"arquivo": []FilterCondition{
		{Suffix: &pdfSuffix},
		{Suffix: &docSuffix},
	}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageOrWithinFieldFirstFailsSecondPasses(t *testing.T) {
	img := map[string]interface{}{"nome": "joao"}

	prefixAdmin := "admin_"
	prefixJoao := "jo"
	f := ImageFilter{"nome": []FilterCondition{
		{Prefix: &prefixAdmin},
		{Prefix: &prefixJoao},
	}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageExtraFieldsIgnored(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200), "arquivo": "doc.pdf", "usuario": "admin"}
	f := ImageFilter{"valor": []FilterCondition{{Numeric: []any{">", float64(100)}}}}
	assert.True(t, MatchImage(img, f))
}

func TestMatchImageNoConditionsMeansPass(t *testing.T) {
	img := map[string]interface{}{"campo": "valor"}
	assert.True(t, MatchImage(img, ImageFilter{"campo": []FilterCondition{}}))
}

func TestMatchImageFieldNotInImage(t *testing.T) {
	img := map[string]interface{}{"valor": float64(200)}
	yes := true
	assert.False(t, MatchImage(img, ImageFilter{"arquivo": []FilterCondition{{Exists: &yes}}}))
}

func TestMatchBothImages(t *testing.T) {
	criteria := FilterCriteria{
		OldImage: ImageFilter{"deleted": []FilterCondition{{Numeric: []any{"=", float64(1)}}}},
		NewImage: ImageFilter{"status": []FilterCondition{}},
	}

	oldImg := map[string]interface{}{"deleted": float64(1)}
	newImg := map[string]interface{}{"status": "active"}

	assert.True(t, MatchImage(oldImg, criteria.OldImage))
	assert.True(t, MatchImage(newImg, criteria.NewImage))
}

func TestMatchOnlyOldImageFilter(t *testing.T) {
	criteria := FilterCriteria{
		OldImage: ImageFilter{"valor": []FilterCondition{{Numeric: []any{">", float64(100)}}}},
	}

	assert.True(t, MatchImage(map[string]interface{}{"valor": float64(200)}, criteria.OldImage))
	assert.True(t, MatchImage(map[string]interface{}{"x": 1}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"valor": float64(50)}, criteria.OldImage))
}

func TestMatchOnlyNewImageFilter(t *testing.T) {
	criteria := FilterCriteria{
		NewImage: ImageFilter{"status": []FilterCondition{{AnythingBut: "blocked"}}},
	}

	assert.True(t, MatchImage(map[string]interface{}{"x": 1}, criteria.OldImage))
	assert.True(t, MatchImage(map[string]interface{}{"status": "active"}, criteria.NewImage))
	assert.False(t, MatchImage(map[string]interface{}{"status": "blocked"}, criteria.NewImage))
}

func TestFilterCriteriaEmpty(t *testing.T) {
	var criteria FilterCriteria
	img := map[string]interface{}{"x": 1}
	assert.True(t, MatchImage(img, criteria.OldImage))
	assert.True(t, MatchImage(img, criteria.NewImage))
}
