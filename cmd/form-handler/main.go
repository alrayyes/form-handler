// SPDX-License-Identifier: GPL-3.0-or-later

// Command form-handler accepts contact form submissions and emails them on.
//
// It was written for the contact form on andthensome.nl, because Cloudflare
// Workers can send mail through an HTTP email API but cannot open an SMTP
// connection, and the mail for that domain is self-hosted. It serves any number
// of forms across any number of sites: one endpoint per form, each with its own
// allowed origins and its own recipient.
package main

import (
	"os"

	"github.com/alrayyes/form-handler/internal/cli"
)

// version is stamped in at build time — by goreleaser from the tag, and by ko
// from its VERSION environment variable. "dev" is what you get from a plain
// `go build`, which is the honest answer for a binary built off an unknown tree.
var version = "dev"

func main() {
	os.Exit(cli.Run(version, os.Args[1:], os.Stdout, os.Stderr))
}
