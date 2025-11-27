package checks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestPropertyVerifyIdempotence verifies Verify methods are idempotent
func TestPropertyVerifyIdempotence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Test with Web check (pattern applies to all check types)
		scheme := rapid.SampledFrom([]string{"http", "https", ""}).Draw(t, "scheme")
		display := rapid.SampledFrom([]string{"web", "api", "frontend", ""}).Draw(t, "display")

		webCheck := &Web{
			Service: Service{
				Display: display,
			},
			Scheme: scheme,
			Url: []urlData{
				{Path: "/"}, // Required: at least one URL
			},
		}

		// First Verify call
		err1 := webCheck.Verify("box01", "10.0.0.1", 5, 30, 25, 5)
		require.NoError(t, err1, "first verify should succeed")

		// Save state after first verify
		firstPort := webCheck.Port
		firstName := webCheck.Name
		firstScheme := webCheck.Scheme
		firstDisplay := webCheck.Display
		firstServiceType := webCheck.ServiceType

		// Second Verify call (IDEMPOTENCE TEST)
		err2 := webCheck.Verify("box01", "10.0.0.1", 5, 30, 25, 5)
		require.NoError(t, err2, "second verify should succeed")

		// Property: Verify(Verify(x)) == Verify(x)
		assert.Equal(t, firstPort, webCheck.Port,
			"port should not change on second verify")
		assert.Equal(t, firstName, webCheck.Name,
			"name should not change on second verify")
		assert.Equal(t, firstScheme, webCheck.Scheme,
			"scheme should not change on second verify")
		assert.Equal(t, firstDisplay, webCheck.Display,
			"display should not change on second verify")
		assert.Equal(t, firstServiceType, webCheck.ServiceType,
			"service type should not change on second verify")
	})
}

// TestPropertyVerifyPortDefaults verifies port logic across schemes
func TestPropertyVerifyPortDefaults(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		scheme := rapid.SampledFrom([]string{"http", "https"}).Draw(t, "scheme")

		webCheck := &Web{
			Scheme: scheme,
			Url:    []urlData{{Path: "/"}},
			// Port intentionally 0 (should be set by Verify)
		}

		err := webCheck.Verify("box01", "10.0.0.1", 5, 30, 25, 5)
		require.NoError(t, err)

		// Property: Port defaults based on scheme (lines 127-133)
		if webCheck.Scheme == "https" {
			assert.Equal(t, 443, webCheck.Port,
				"https should default to port 443")
		} else {
			assert.Equal(t, 80, webCheck.Port,
				"http should default to port 80")
		}

		// Property: Name always set to box-display format (lines 124-126)
		assert.NotEmpty(t, webCheck.Name, "name must be set")
		assert.Equal(t, "box01-web", webCheck.Name,
			"name should be formatted as box-display")

		// Property: Scheme defaults to http if empty (lines 118-120)
		assert.NotEmpty(t, webCheck.Scheme, "scheme must be set")
		if scheme == "" {
			assert.Equal(t, "http", webCheck.Scheme,
				"empty scheme should default to http")
		}

		// Property: ServiceType always set (lines 111-113)
		assert.Equal(t, "Web", webCheck.ServiceType,
			"service type should be set to Web")
	})
}

// TestPropertyVerifyRequiresURL verifies Web check requires at least one URL
func TestPropertyVerifyRequiresURL(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		webCheck := &Web{
			Scheme: "http",
			// Url intentionally empty or nil
		}

		err := webCheck.Verify("box01", "10.0.0.1", 5, 30, 25, 5)

		// Property: Must reject configs without URLs (lines 134-136)
		require.Error(t, err, "verify should fail without URLs")
		assert.Contains(t, err.Error(), "no urls defined",
			"error should mention missing URLs")
	})
}

// TestPropertyRunnableDisabledAlwaysFalse verifies disabled services never runnable
func TestPropertyRunnableDisabledAlwaysFalse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random time configurations
		now := time.Now()
		launchOffset := rapid.IntRange(-100, 100).Draw(t, "launchOffset")
		stopOffset := rapid.IntRange(-100, 100).Draw(t, "stopOffset")

		service := &Service{
			Disabled:   true, // ALWAYS disabled
			LaunchTime: now.Add(time.Duration(launchOffset) * time.Second),
			StopTime:   now.Add(time.Duration(stopOffset) * time.Second),
		}

		runnable := service.Runnable()

		// Property: Disabled always returns false (lines 142-144)
		assert.False(t, runnable,
			"disabled service should never be runnable, regardless of times")
	})
}

// TestPropertyRunnableFutureLaunchBlocks verifies future launch time blocks execution
func TestPropertyRunnableFutureLaunchBlocks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random future time (1 to 1000 seconds from now)
		futureSeconds := rapid.IntRange(1, 1000).Draw(t, "futureSeconds")

		service := &Service{
			Disabled:   false,
			LaunchTime: time.Now().Add(time.Duration(futureSeconds) * time.Second),
		}

		runnable := service.Runnable()

		// Property: Future launch time returns false (lines 145-147)
		assert.False(t, runnable,
			"service with future launch time should not be runnable")
	})
}

// TestPropertyRunnablePastStopTimeBlocks verifies stopped services not runnable
func TestPropertyRunnablePastStopTimeBlocks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random past time (1 to 1000 seconds ago)
		pastSeconds := rapid.IntRange(1, 1000).Draw(t, "pastSeconds")

		service := &Service{
			Disabled:   false,
			LaunchTime: time.Now().Add(-2000 * time.Second), // Launched long ago
			StopTime:   time.Now().Add(-time.Duration(pastSeconds) * time.Second), // Stopped in past
		}

		runnable := service.Runnable()

		// Property: Past stop time returns false (lines 148-150)
		assert.False(t, runnable,
			"service with past stop time should not be runnable")
	})
}

// TestPropertyRunnableNormalOperation verifies normal services are runnable
func TestPropertyRunnableNormalOperation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		now := time.Now()

		// Generate past launch time (already started)
		pastLaunchSeconds := rapid.IntRange(1, 1000).Draw(t, "pastLaunchSeconds")

		service := &Service{
			Disabled:   false,
			LaunchTime: now.Add(-time.Duration(pastLaunchSeconds) * time.Second),
			StopTime:   time.Time{}, // Zero time (never stops)
		}

		runnable := service.Runnable()

		// Property: Normal operation returns true (line 151)
		assert.True(t, runnable,
			"enabled service that has launched and not stopped should be runnable")
	})
}

// TestPropertyRunnableTimeBoundaries verifies edge cases near time boundaries
func TestPropertyRunnableTimeBoundaries(t *testing.T) {
	t.Run("just launched", func(t *testing.T) {
		service := &Service{
			Disabled:   false,
			LaunchTime: time.Now().Add(-1 * time.Second), // Launched 1 second ago
			StopTime:   time.Time{},                      // Zero time
		}

		runnable := service.Runnable()

		// Property: Recently launched service should be runnable
		assert.True(t, runnable,
			"service that just launched should be runnable")
	})

	t.Run("about to stop", func(t *testing.T) {
		service := &Service{
			Disabled:   false,
			LaunchTime: time.Now().Add(-10 * time.Second), // Started 10s ago
			StopTime:   time.Now().Add(1 * time.Second),   // Stopping in 1 second
		}

		runnable := service.Runnable()

		// Property: Service not yet stopped should be runnable
		assert.True(t, runnable,
			"service that hasn't stopped yet should be runnable")
	})

	t.Run("zero stop time never blocks", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			service := &Service{
				Disabled:   false,
				LaunchTime: time.Now().Add(-100 * time.Second),
				StopTime:   time.Time{}, // Zero time (IsZero() == true)
			}

			runnable := service.Runnable()

			// Property: Zero stop time means service runs indefinitely (line 148)
			assert.True(t, runnable,
				"service with zero stop time should be runnable")
		})
	})
}

// TestPropertyRunnableLogicCorrectness verifies complete state machine
func TestPropertyRunnableLogicCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random service configuration with clear boundaries (avoid edge cases)
		disabled := rapid.Bool().Draw(t, "disabled")
		// Launch time: clearly in past (-100 to -10) or future (10 to 100), avoiding boundaries
		launchInPast := rapid.Bool().Draw(t, "launchInPast")
		var launchOffset int
		if launchInPast {
			launchOffset = rapid.IntRange(-100, -10).Draw(t, "launchOffset")
		} else {
			launchOffset = rapid.IntRange(10, 100).Draw(t, "launchOffset")
		}

		service := &Service{
			Disabled:   disabled,
			LaunchTime: time.Now().Add(time.Duration(launchOffset) * time.Second),
		}

		hasStopTime := rapid.Bool().Draw(t, "hasStopTime")
		if hasStopTime {
			// Stop time: clearly in past (-100 to -10) or future (10 to 100)
			stopInPast := rapid.Bool().Draw(t, "stopInPast")
			var stopOffset int
			if stopInPast {
				stopOffset = rapid.IntRange(-100, -10).Draw(t, "stopOffset")
			} else {
				stopOffset = rapid.IntRange(10, 100).Draw(t, "stopOffset")
			}
			service.StopTime = time.Now().Add(time.Duration(stopOffset) * time.Second)
		}

		runnable := service.Runnable()

		// Verify logic matches implementation (lines 142-151)
		// Note: Using fresh time.Now() call to match Runnable() behavior
		now := time.Now()
		expectedRunnable := true

		if service.Disabled {
			expectedRunnable = false
		} else if service.LaunchTime.After(now) {
			expectedRunnable = false
		} else if !service.StopTime.IsZero() && service.StopTime.Before(now) {
			expectedRunnable = false
		}

		// Property: Runnable() matches expected logic
		assert.Equal(t, expectedRunnable, runnable,
			"runnable result should match expected state machine logic")
	})
}
