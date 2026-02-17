package ttl

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatOutput(t *testing.T) {
	info := TTLInfo{
		ReleaseName:      "myapp",
		ReleaseNamespace: "staging",
		CronjobNamespace: "ops",
		ScheduledDate:    "2025-06-15T14:30:00Z",
		CronSchedule:     "30 14 15 6 *",
		DeleteNamespace:  false,
	}

	t.Run("text format", func(t *testing.T) {
		result, err := FormatOutput(info, "text")
		require.NoError(t, err)
		assert.Contains(t, result, "Release:          myapp")
		assert.Contains(t, result, "Release Namespace: staging")
		assert.Contains(t, result, "CronJob Namespace: ops")
		assert.Contains(t, result, "Scheduled Date:   2025-06-15T14:30:00Z")
		assert.Contains(t, result, "Cron Schedule:    30 14 15 6 *")
		assert.Contains(t, result, "Delete Namespace: no")
	})

	t.Run("text format with delete namespace", func(t *testing.T) {
		infoWithDelete := info
		infoWithDelete.DeleteNamespace = true
		result, err := FormatOutput(infoWithDelete, "text")
		require.NoError(t, err)
		assert.Contains(t, result, "Delete Namespace: yes")
	})

	t.Run("json format", func(t *testing.T) {
		result, err := FormatOutput(info, "json")
		require.NoError(t, err)
		assert.Contains(t, result, `"release_name": "myapp"`)
		assert.Contains(t, result, `"release_namespace": "staging"`)
		assert.Contains(t, result, `"cronjob_namespace": "ops"`)
		assert.Contains(t, result, `"scheduled_date": "2025-06-15T14:30:00Z"`)
		assert.Contains(t, result, `"cron_schedule": "30 14 15 6 *"`)
		assert.Contains(t, result, `"delete_namespace": false`)
	})

	t.Run("yaml format", func(t *testing.T) {
		result, err := FormatOutput(info, "yaml")
		require.NoError(t, err)
		assert.Contains(t, result, "release_name: myapp")
		assert.Contains(t, result, "release_namespace: staging")
		assert.Contains(t, result, "cronjob_namespace: ops")
		assert.Contains(t, result, "scheduled_date: \"2025-06-15T14:30:00Z\"")
		assert.Contains(t, result, "cron_schedule: 30 14 15 6 *")
		assert.Contains(t, result, "delete_namespace: false")
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := FormatOutput(info, "xml")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported output format")
	})
}

func TestFormatScheduledDate(t *testing.T) {
	ts := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	result := FormatScheduledDate(ts)
	assert.Equal(t, "2025-06-15T14:30:00Z", result)
}
