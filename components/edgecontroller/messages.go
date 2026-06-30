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

// leansignaledgecontroller/messages.go
package leansignaledgecontroller

import "time"

// AckTimeout is how long the agent waits for lean-api to acknowledge an
// index-sync message (IndexCreate/Update/Delete) sent over the control stream.
const AckTimeout = 30 * time.Second

// The wire protocol is defined by the protobuf service AgentControl in
// github.com/leansignal/leansignal-agent/proto (package leansignal.agent.v1).
// The previous JSON-over-WebSocket message types have been replaced by the
// generated agentv1 messages.
