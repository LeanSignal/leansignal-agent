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

//go:build windows

// leansignaledgecontroller/reload_windows.go
package leansignaledgecontroller

import "errors"

// reloadCollector is unavailable on Windows: the collector's in-place reload is
// driven by SIGHUP, which the platform has no equivalent for. The new config is
// still validated and written — it takes effect when the service is restarted.
func reloadCollector() error {
	return errors.New("in-place config reload is not supported on Windows; restart the LeanSignal Agent service to apply the new config")
}
