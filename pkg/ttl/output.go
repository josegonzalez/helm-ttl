package ttl

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// TTLInfo contains information about a TTL setting for output.
type TTLInfo struct {
	ReleaseName      string `json:"release_name" yaml:"release_name"`
	ReleaseNamespace string `json:"release_namespace" yaml:"release_namespace"`
	CronjobNamespace string `json:"cronjob_namespace" yaml:"cronjob_namespace"`
	ScheduledDate    string `json:"scheduled_date" yaml:"scheduled_date"`
	CronSchedule     string `json:"cron_schedule" yaml:"cron_schedule"`
	DeleteNamespace  bool   `json:"delete_namespace" yaml:"delete_namespace"`
}

// FormatOutput formats a TTLInfo in the specified format.
func FormatOutput(info TTLInfo, format string) (string, error) {
	switch format {
	case "text":
		deleteNs := "no"
		if info.DeleteNamespace {
			deleteNs = "yes"
		}

		return fmt.Sprintf("Release:          %s\n"+
			"Release Namespace: %s\n"+
			"CronJob Namespace: %s\n"+
			"Scheduled Date:   %s\n"+
			"Cron Schedule:    %s\n"+
			"Delete Namespace: %s\n",
			info.ReleaseName,
			info.ReleaseNamespace,
			info.CronjobNamespace,
			info.ScheduledDate,
			info.CronSchedule,
			deleteNs,
		), nil

	case "json":
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}

		return string(data) + "\n", nil

	case "yaml":
		data, err := yaml.Marshal(info)
		if err != nil {
			return "", fmt.Errorf("failed to marshal YAML: %w", err)
		}

		return string(data), nil

	default:
		return "", fmt.Errorf("unsupported output format %q; valid formats: text, json, yaml", format)
	}
}

// FormatScheduledDate formats a time for display.
func FormatScheduledDate(t time.Time) string {
	return t.Format(time.RFC3339)
}
