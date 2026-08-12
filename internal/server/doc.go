// SPDX-License-Identifier: GPL-3.0-or-later

// Package server wires a Config into something that serves HTTP. It is the
// composition root: the one place that knows a form has both a handler and a
// mailer, and pairs them up.
package server
