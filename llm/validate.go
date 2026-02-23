package llm

import "fmt"

// MemoryType wraps string for validation (mirrors storage.MemoryType).
type MemoryType string

const (
	TypeIdentity     MemoryType = "IDENTITY"
	TypePreference   MemoryType = "PREFERENCE"
	TypeRelationship MemoryType = "RELATIONSHIP"
	TypeEvent        MemoryType = "EVENT"
	TypeActivity     MemoryType = "ACTIVITY"
	TypePlan         MemoryType = "PLAN"
	TypeContext      MemoryType = "CONTEXT"
	TypeEphemeral    MemoryType = "EPHEMERAL"
)

func (t MemoryType) IsValid() bool {
	switch t {
	case TypeIdentity, TypePreference, TypeRelationship, TypeEvent,
		TypeActivity, TypePlan, TypeContext, TypeEphemeral:
		return true
	default:
		return false
	}
}

// StabilityDays returns the default stability (in days) for a memory type.
func (t MemoryType) StabilityDays() float64 {
	switch t {
	case TypeIdentity:
		return 365
	case TypePreference:
		return 180
	case TypeRelationship:
		return 180
	case TypeEvent:
		return 60
	case TypeActivity:
		return 45
	case TypePlan:
		return 30
	case TypeContext:
		return 7
	case TypeEphemeral:
		return 1
	default:
		return 60
	}
}

func validateResponse(resp *ExtractionResponse) error {
	if resp.Memories == nil {
		resp.Memories = []ExtractedMemory{}
	}
	if resp.Updates == nil {
		resp.Updates = []MemoryUpdate{}
	}
	if resp.Deletes == nil {
		resp.Deletes = []MemoryDelete{}
	}
	if resp.Skipped == nil {
		resp.Skipped = []SkippedContent{}
	}

	validMemories := make([]ExtractedMemory, 0, len(resp.Memories))
	for i, mem := range resp.Memories {
		if !MemoryType(mem.Type).IsValid() {
			return fmt.Errorf("memory[%d]: invalid type %q", i, mem.Type)
		}
		if mem.Importance < 0 || mem.Importance > 1 {
			return fmt.Errorf("memory[%d]: importance %f out of range [0,1]", i, mem.Importance)
		}
		if mem.Confidence < 0 || mem.Confidence > 1 {
			return fmt.Errorf("memory[%d]: confidence %f out of range [0,1]", i, mem.Confidence)
		}
		if mem.Content == "" {
			return fmt.Errorf("memory[%d]: content is empty", i)
		}
		if mem.ImportanceFactors == nil {
			mem.ImportanceFactors = []string{}
		}
		if mem.ConfidenceFactors == nil {
			mem.ConfidenceFactors = []string{}
		}
		validMemories = append(validMemories, mem)
	}
	resp.Memories = validMemories

	validUpdates := make([]MemoryUpdate, 0, len(resp.Updates))
	for _, upd := range resp.Updates {
		if upd.Query != "" && upd.NewContent != "" {
			validUpdates = append(validUpdates, upd)
		}
	}
	resp.Updates = validUpdates

	validDeletes := make([]MemoryDelete, 0, len(resp.Deletes))
	for _, del := range resp.Deletes {
		if del.Query != "" {
			validDeletes = append(validDeletes, del)
		}
	}
	resp.Deletes = validDeletes

	return nil
}
