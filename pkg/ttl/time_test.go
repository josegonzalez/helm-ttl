package ttl

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTimeInput(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	t.Run("Go duration - minutes", func(t *testing.T) {
		result, err := ParseTimeInput("30m", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(30*time.Minute), result)
	})

	t.Run("Go duration - hours", func(t *testing.T) {
		result, err := ParseTimeInput("2h", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(2*time.Hour), result)
	})

	t.Run("Go duration - combined", func(t *testing.T) {
		result, err := ParseTimeInput("2h30m", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(2*time.Hour+30*time.Minute), result)
	})

	t.Run("Go duration - 24h", func(t *testing.T) {
		result, err := ParseTimeInput("24h", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(24*time.Hour), result)
	})

	t.Run("Go duration - 168h (1 week)", func(t *testing.T) {
		result, err := ParseTimeInput("168h", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(168*time.Hour), result)
	})

	t.Run("Go duration - negative rejected", func(t *testing.T) {
		_, err := ParseTimeInput("-1h", now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "positive")
	})

	t.Run("days shorthand - 7d", func(t *testing.T) {
		result, err := ParseTimeInput("7d", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(7*24*time.Hour), result)
	})

	t.Run("days shorthand - 30d", func(t *testing.T) {
		result, err := ParseTimeInput("30d", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(30*24*time.Hour), result)
	})

	t.Run("days shorthand - 1d", func(t *testing.T) {
		result, err := ParseTimeInput("1d", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(24*time.Hour), result)
	})

	t.Run("days shorthand - 0d rejected", func(t *testing.T) {
		_, err := ParseTimeInput("0d", now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "positive")
	})

	t.Run("natural language - tomorrow", func(t *testing.T) {
		result, err := ParseTimeInput("tomorrow", now)
		require.NoError(t, err)
		assert.True(t, result.After(now))
	})

	t.Run("natural language - in 2 hours", func(t *testing.T) {
		result, err := ParseTimeInput("in 2 hours", now)
		require.NoError(t, err)
		assert.True(t, result.After(now))
	})

	t.Run("exceeds max TTL - duration", func(t *testing.T) {
		_, err := ParseTimeInput("9000h", now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum")
	})

	t.Run("exceeds max TTL - days", func(t *testing.T) {
		_, err := ParseTimeInput("400d", now)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum")
	})
}

func TestTimeToCronSchedule(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "basic time",
			time:     time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			expected: "30 14 15 6 *",
		},
		{
			name:     "midnight",
			time:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: "0 0 1 1 *",
		},
		{
			name:     "end of day",
			time:     time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC),
			expected: "59 23 31 12 *",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := TimeToCronSchedule(tc.time)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestParseCronSchedule(t *testing.T) {
	t.Run("valid schedule - future date", func(t *testing.T) {
		// Use a date far in the future to avoid year-roll issues
		future := time.Now().Add(180 * 24 * time.Hour)
		schedule := TimeToCronSchedule(future)

		result, err := ParseCronSchedule(schedule)
		require.NoError(t, err)
		assert.Equal(t, future.Month(), result.Month())
		assert.Equal(t, future.Day(), result.Day())
		assert.Equal(t, future.Hour(), result.Hour())
		assert.Equal(t, future.Minute(), result.Minute())
	})

	t.Run("invalid schedule format", func(t *testing.T) {
		_, err := ParseCronSchedule("invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid cron schedule")
	})

	t.Run("roundtrip", func(t *testing.T) {
		original := time.Date(2026, 3, 15, 14, 30, 0, 0, time.Now().Location())
		schedule := TimeToCronSchedule(original)
		result, err := ParseCronSchedule(schedule)
		require.NoError(t, err)
		assert.Equal(t, original.Month(), result.Month())
		assert.Equal(t, original.Day(), result.Day())
		assert.Equal(t, original.Hour(), result.Hour())
		assert.Equal(t, original.Minute(), result.Minute())
	})
}
