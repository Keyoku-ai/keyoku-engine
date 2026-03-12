// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package engine

import "strings"

// stopWords are standard NLP stop words (articles, pronouns, prepositions).
// These are language-structural, not domain-specific — safe to hardcode.
var stopWords = map[string]bool{
	"what": true, "is": true, "the": true, "a": true, "an": true,
	"user's": true, "user": true, "their": true, "do": true, "does": true,
	"where": true, "how": true, "who": true, "when": true, "which": true,
	"and": true, "or": true, "of": true, "to": true, "in": true,
	"for": true, "with": true, "they": true, "are": true, "was": true,
	"about": true, "from": true, "at": true, "has": true, "have": true,
	"did": true, "can": true, "will": true, "my": true, "me": true,
	"tell": true, "know": true,
}

// expandQueryForFTS extracts meaningful keywords from a query for FTS fallback.
// Only does generic word extraction — no domain-specific pattern matching.
// Semantic understanding is handled by embeddings and optional LLM re-ranking.
func expandQueryForFTS(query string) []string {
	queries := []string{query}
	words := strings.Fields(strings.ToLower(query))

	for _, w := range words {
		clean := strings.Trim(w, "?.,!;:'\"")
		if len(clean) > 2 && !stopWords[clean] {
			queries = append(queries, clean)
		}
	}

	return queries
}
