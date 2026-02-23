package vectorindex

import "math"

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns a value in [-1, 1], where 1 means identical direction.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// CosineDistance returns 1 - CosineSimilarity.
// A distance of 0 means identical, 2 means opposite.
func CosineDistance(a, b []float32) float32 {
	return 1 - CosineSimilarity(a, b)
}

// DotProduct computes the dot product of two vectors.
func DotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return float32(sum)
}

// Normalize returns a unit-length copy of the vector.
func Normalize(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return make([]float32, len(v))
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}
