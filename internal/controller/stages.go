package controller

// LifecycleStage represents a discrete stage in the namespace lifecycle.
type LifecycleStage string

const (
	// StageEmpty is the initial state: namespace exists, team=unknown, no flag applied yet.
	StageEmpty LifecycleStage = ""

	// StageYellow: yellow flag applied.
	StageYellow LifecycleStage = "yellow"

	// StageRed: red flag applied.
	StageRed LifecycleStage = "red"

	// StageFinalWarning: final warning sent, scale-down imminent.
	StageFinalWarning LifecycleStage = "final-warning"

	// StageScaledDown: all workloads in the namespace have been scaled to
	// zero (terminal state for the first phase). The namespace itself is
	// preserved so a team can still claim and restore it manually.
	StageScaledDown LifecycleStage = "scaled-down"
)

// LifecycleStages defines the ordered progression of stages.
// Each transition should trigger exactly one notification.
var LifecycleStages = []LifecycleStage{
	StageEmpty,
	StageYellow,
	StageRed,
	StageFinalWarning,
	StageScaledDown,
}

// LifecycleEvent describes a single lifecycle stage with its human-readable metadata.
type LifecycleEvent struct {
	Stage    LifecycleStage
	Label    string
	Emoji    string
	Severity string
}

// GetLifecycleEvent returns the event metadata for a stage.
func GetLifecycleEvent(stage LifecycleStage) LifecycleEvent {
	switch stage {
	case StageEmpty:
		return LifecycleEvent{
			Stage:    StageEmpty,
			Label:    "Namespace Created",
			Emoji:    "🆕",
			Severity: "LOW",
		}
	case StageYellow:
		return LifecycleEvent{
			Stage:    StageYellow,
			Label:    "Yellow Flag Applied",
			Emoji:    "⚠️",
			Severity: "MEDIUM",
		}
	case StageRed:
		return LifecycleEvent{
			Stage:    StageRed,
			Label:    "Red Flag Applied",
			Emoji:    "🔴",
			Severity: "HIGH",
		}
	case StageFinalWarning:
		return LifecycleEvent{
			Stage:    StageFinalWarning,
			Label:    "Final Warning — Scale-Down in 24 Hours",
			Emoji:    "🚨",
			Severity: "HIGH",
		}
	case StageScaledDown:
		return LifecycleEvent{
			Stage:    StageScaledDown,
			Label:    "Workloads Scaled Down",
			Emoji:    "⏸️",
			Severity: "CRITICAL",
		}
	default:
		return LifecycleEvent{
			Stage:    stage,
			Label:    string(stage),
			Emoji:    "📋",
			Severity: "LOW",
		}
	}
}
