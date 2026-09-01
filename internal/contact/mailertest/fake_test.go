// SPDX-License-Identifier: GPL-3.0-or-later

package mailertest_test

import (
	"context"
	"testing"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake is held to the same contract as the two real adapters. Without this
// it is just a struct that happens to compile, and every test using it is
// asserting against behaviour nothing in production shares.
func TestTheFakeKeepsTheMailerContract(t *testing.T) {
	// The real adapters cannot supply this — their failures come from a mail
	// server — which is why the fake is the one that has to prove it wraps
	// what it was given rather than rebuilding it.
	down := mailertest.ErrMailServerDown

	mailertest.Contract(t, mailertest.Subject{
		Provider: mailertest.Provider,
		Cause:    down,
		Working:  func(*testing.T) contact.Mailer { return mailertest.NewFake() },
		Failing:  func(*testing.T) contact.Mailer { return mailertest.NewFake().Breaks(down) },
	})
}

func TestTheFakeKeepsWhatItWasSent(t *testing.T) {
	f := mailertest.NewFake()
	require.NoError(t, f.Send(context.Background(), contact.Message{Name: "Ada", Body: "hello"}))

	sent := f.Sent()

	require.Len(t, sent, 1)
	assert.Equal(t, "Ada", sent[0].Name)
}

// Sent hands back a copy. A test ranging over it while the handler it is
// driving sends another message should not race, and the race detector is what
// says whether that is true.
func TestSentIsSafeToReadWhileSending(t *testing.T) {
	f := mailertest.NewFake()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range 50 {
			_ = f.Send(context.Background(), contact.Message{Name: "Ada"})
		}
	}()
	for range 50 {
		_ = f.Sent()
	}
	<-done

	assert.Equal(t, 50, f.Count())
}

func TestABrokenFakeDeliversNothing(t *testing.T) {
	f := mailertest.NewFake().Breaks(mailertest.ErrMailServerDown)

	require.Error(t, f.Send(context.Background(), contact.Message{Name: "Ada"}))

	assert.Zero(t, f.Count(), "a failed send was recorded as delivered")
}
