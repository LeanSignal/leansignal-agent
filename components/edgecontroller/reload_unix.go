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

//go:build !windows

// leansignaledgecontroller/reload_unix.go
package leansignaledgecontroller

import (
	"fmt"
	"os"
	"syscall"
)

// reloadCollector asks the collector to reload its config in place.
//
// otelcol.Collector.Run always listens for SIGHUP and answers it by shutting
// the service down and rebuilding it from a fresh read of the same --config
// sources, so signalling ourselves is the whole mechanism: no supervisor, no
// process restart, and it behaves identically under systemd, docker and k8s.
//
// The reload tears down every component, this extension included, so the
// control stream drops and reconnects a few seconds later.
func reloadCollector() error {
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		return fmt.Errorf("cannot signal the collector to reload: %w", err)
	}

	return nil
}
