// Copyright 2026 LeanSignal
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// leansignalingestbackoff/config.go
package leansignalingestbackoff

import (
	"errors"
	"time"
)

// Config configures the ingest backoff valve.
type Config struct {
	// RetryInterval is how long pushes stay suppressed after the tenant ingest
	// edge rejects one with 403 (ingest limit hit). When it elapses, exactly
	// one push is let through as a probe: a 403 re-arms the pause, anything
	// else resumes normal pushing. Keep it minute-scale — limits clear on
	// retention (storage) or month rollover (ingest budget), so probing
	// faster buys nothing.
	RetryInterval time.Duration `mapstructure:"retry_interval"`
}

// Validate implements component.ConfigValidator.
func (c *Config) Validate() error {
	if c.RetryInterval <= 0 {
		return errors.New("retry_interval must be positive")
	}

	return nil
}
