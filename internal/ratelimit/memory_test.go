// SPDX-License-Identifier: GPL-3.0-or-later

package ratelimit_test

import (
	"testing"
	"time"

	"github.com/alrayyes/form-handler/internal/ratelimit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const caller = "203.0.113.7"

func TestACallerMaySubmitUpToTheLimit(t *testing.T) {
	m := ratelimit.New(2, time.Hour)

	require.True(t, m.Allow(caller), "first submission")

	assert.True(t, m.Allow(caller), "second submission")
}

func TestTheSubmissionAfterTheLimitIsRefused(t *testing.T) {
	m := ratelimit.New(2, time.Hour)
	require.True(t, m.Allow(caller))
	require.True(t, m.Allow(caller))

	assert.False(t, m.Allow(caller))
}

// Zero is how a form says it does not want a limit, so it cannot mean "no
// submissions allowed" — that reading would take a form offline the moment
// somebody left the setting out.
func TestALimitOfZeroTurnsItOff(t *testing.T) {
	m := ratelimit.New(0, time.Hour)
	for range 100 {
		require.True(t, m.Allow(caller))
	}

	assert.True(t, m.Allow(caller))
}

// One visitor filling the form must not stop the next. This is the branch the
// key exists for, and behind a proxy it is the difference between limiting
// visitors and limiting the proxy.
func TestCallersAreCountedSeparately(t *testing.T) {
	m := ratelimit.New(1, time.Hour)
	require.True(t, m.Allow(caller))

	assert.True(t, m.Allow("198.51.100.4"))
}

// A window that never expired would refuse a caller forever after their first
// burst. The period is a nanosecond so the test does not wait for one.
func TestTheWindowForgivesACallerOnceItPasses(t *testing.T) {
	m := ratelimit.New(1, time.Nanosecond)
	require.True(t, m.Allow(caller))

	assert.True(t, m.Allow(caller))
}
