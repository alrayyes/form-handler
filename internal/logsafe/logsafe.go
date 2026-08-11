// SPDX-License-Identifier: GPL-3.0-or-later

// Package logsafe cleans a value on its way into a log line.
//
// It exists because this service logs things it did not choose: the Origin and
// CF-Ray headers, the client address, the request path, the decoder's
// complaint about a body somebody sent. All of those are attacker-controlled,
// and two of them are the whole reason the logs are worth having.
//
// slog's JSON handler already escapes what it writes, so a newline in a value
// cannot forge a second entry today. This does not rely on that. The handler is
// a choice somebody could change to a text one in a minute, log lines get piped
// through things that unescape them, and neither of those should turn into a
// way to write fiction into an audit trail. Cleaning at the point the value
// enters the line survives all of it.
package logsafe

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Max is the longest value that reaches a log line, in runes. Generous for
// anything legitimate — an origin, a request id, an address — and mean for
// anything trying to use the log as storage.
const Max = 200

// truncated marks a value that was cut short, so a reader can tell the
// difference between a long value and the whole of a short one.
const truncated = "…"

// String returns v with anything that could break a log line removed, bounded
// to Max runes.
//
// Control characters come out rather than being escaped: the value is going
// into a field a person reads, and "https://evil.example\n{...}" reading as
// "https://evil.example{...}" makes the attempt visible instead of tidy. What
// is left is still enough to see what was sent.
func String(v string) string {
	if v == "" {
		return ""
	}

	cleaned := strings.Map(func(r rune) rune {
		// Drop the C0 and C1 control ranges, DEL, and anything that is not a
		// valid rune to begin with. unicode.IsControl covers all of it.
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, v)

	// Counted in runes, not bytes: cutting at a byte offset splits a multi-byte
	// character in half and puts invalid UTF-8 into a JSON log line.
	if utf8.RuneCountInString(cleaned) <= Max {
		return cleaned
	}

	var b strings.Builder
	for i, r := range []rune(cleaned) {
		if i >= Max {
			break
		}
		b.WriteRune(r)
	}
	b.WriteString(truncated)
	return b.String()
}
