package notification

import (
	"fmt"
	"math"
	"time"
)

// FormatAge converts a duration into a human-readable string.
// Examples: "2 hours 15 min", "3 days 4 hours", "45 min", "30 sec"
func FormatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d sec", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}

	totalHours := int(d.Hours())
	days := totalHours / 24
	hours := totalHours % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%d days %d hours", days, hours)
		}
		return fmt.Sprintf("%d days", days)
	}
	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%d hours %d min", hours, minutes)
		}
		return fmt.Sprintf("%d hours", hours)
	}
	return fmt.Sprintf("%d min", minutes)
}

// ActionInfo contains human-readable information about a lifecycle action.
type ActionInfo struct {
	Emoji    string
	Label    string
	Guidance string
	Severity string
	Color    string
}

// GetActionInfo returns human-readable info for an action code.
func GetActionInfo(action string) ActionInfo {
	switch action {
	case "AppliedyellowFlag":
		return ActionInfo{
			Emoji:    "⚠️",
			Label:    "Yellow Flag Applied",
			Guidance: "This namespace has no team owner. Please assign a team by setting `snappcloud.io/team=<your-team>`. It will be deleted if unclaimed.",
			Severity: "MEDIUM",
			Color:    "#FFCC00",
		}
	case "AppliedredFlag":
		return ActionInfo{
			Emoji:    "🔴",
			Label:    "Red Flag Applied",
			Guidance: "⚠️ **URGENT**: This namespace's workloads will be **scaled to zero** soon. Assign a team immediately: `kc label ns <name> snappcloud.io/team=<your-team>`",
			Severity: "HIGH",
			Color:    "#FF6600",
		}
	case "FinalWarning":
		return ActionInfo{
			Emoji:    "🚨",
			Label:    "Final Warning — Scale-Down in 24 Hours",
			Guidance: "🚨 **THIS NAMESPACE'S WORKLOADS WILL BE SCALED TO ZERO IN ~24 HOURS.** Assign a team NOW to prevent service interruption: `kc label ns <name> snappcloud.io/team=<your-team>`",
			Severity: "HIGH",
			Color:    "#FF3300",
		}
	case "ScalingDownWorkloads":
		return ActionInfo{
			Emoji:    "⏸️",
			Label:    "Workloads Scaled Down",
			Guidance: "All Deployments, StatefulSets, ReplicaSets and CronJobs in this namespace have been scaled to zero. The namespace itself is preserved; scale workloads back up manually if a team claims it.",
			Severity: "CRITICAL",
			Color:    "#FF0000",
		}
	case "NamespaceClaimed":
		return ActionInfo{
			Emoji:    "✅",
			Label:    "Namespace Claimed",
			Guidance: "A team has taken ownership. Lifecycle management is now complete and the flag has been removed.",
			Severity: "LOW",
			Color:    "#36A64F",
		}
	default:
		return ActionInfo{
			Emoji:    "📋",
			Label:    action,
			Guidance: "",
			Severity: "LOW",
			Color:    "#808080",
		}
	}
}

// DaysUntilDeletion returns a human-readable countdown to deletion based on the current flag.
func DaysUntilDeletion(flag string, age time.Duration, deleteThreshold time.Duration) string {
	remaining := deleteThreshold - age
	if remaining <= 0 {
		return "imminent"
	}
	days := int(math.Ceil(remaining.Hours() / 24))
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
