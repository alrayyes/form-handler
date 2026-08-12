// SPDX-License-Identifier: GPL-3.0-or-later

// Package config assembles what the service needs to run, from a forms file
// where there is one and from the environment where there is not.
//
// It is deliberately the only place that reads os.Getenv or touches the disk.
// Everything downstream takes a Config, which is what makes the whole service
// testable without a filesystem or an environment to arrange.
package config
