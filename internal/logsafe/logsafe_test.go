// SPDX-License-Identifier: GPL-3.0-or-later

package logsafe_test

import (
	"strings"
	"testing"

	"github.com/alrayyes/form-handler/internal/logsafe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrdinaryValuesArePassedThrough(t *testing.T) {
	for _, v := range []string{
		"https://www.example.com",
		"8f2b1c4d5e6f7a8b-AMS",
		"203.0.113.7",
		"Ada Lovelace",
		"naïve café — with punctuation",
	} {
		assert.Equal(t, v, logsafe.String(v))
	}
}

// The whole point. A newline in a logged value lets whoever supplied it write
// what looks like a second log entry, so a request can claim things about
// itself that never happened.
func TestLineBreaksCannotForgeAnEntry(t *testing.T) {
	got := logsafe.String("https://evil.example\n{\"level\":\"INFO\",\"msg\":\"sent message\"}")

	assert.NotContains(t, got, "\n")
	assert.NotContains(t, got, "\r")
	// The text survives, mangled but readable — dropping it entirely would
	// hide the fact that somebody tried.
	assert.Contains(t, got, "evil.example")
}

func TestOtherControlCharactersComeOutToo(t *testing.T) {
	got := logsafe.String("a\tb\x00c\x1b[31md\x7fe")

	for _, bad := range []string{"\t", "\x00", "\x1b", "\x7f"} {
		assert.NotContains(t, got, bad)
	}
	// The escape byte is what makes an ANSI sequence do anything; without it
	// "[31m" is four ordinary characters, and leaving them shows what was sent
	// rather than quietly tidying it away.
	assert.Equal(t, "abc[31mde", got)
}

func TestLongValuesAreTruncated(t *testing.T) {
	got := logsafe.String(strings.Repeat("a", logsafe.Max*3))

	assert.LessOrEqual(t, len([]rune(got)), logsafe.Max+1, "an unbounded value reached the log")
	assert.True(t, strings.HasSuffix(got, "…"), "a truncated value should say so")
}

// Truncating by bytes would cut a multi-byte rune in half and put an invalid
// UTF-8 sequence into a JSON log line.
func TestTruncationDoesNotSplitARune(t *testing.T) {
	got := logsafe.String(strings.Repeat("é", logsafe.Max*2))

	require.NotEmpty(t, got)
	assert.True(t, strings.ContainsRune(got, 'é'))
	for _, r := range got {
		assert.NotEqual(t, '�', r, "truncation produced an invalid rune")
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	assert.Empty(t, logsafe.String(""))
}
