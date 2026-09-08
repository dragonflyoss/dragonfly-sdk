/*
 *     Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package util provides the helpers of the e2e tests: running the SDK drivers
// and commands in the pods of the kind cluster, generating files on the file
// server and inspecting the task content held by the seed peers.
package util

const (
	// DragonflyNamespace is the namespace of the dragonfly components.
	DragonflyNamespace = "dragonfly-system"

	// E2ENamespace is the namespace of the e2e components, the file server and
	// the runner pod.
	E2ENamespace = "dragonfly-e2e"

	// SchedulerEndpoint is the scheduler endpoint in the cluster, the SDK
	// drivers run in the runner pod and connect to it.
	SchedulerEndpoint = "http://dragonfly-scheduler.dragonfly-system.svc:8002"

	// SeedClientReplicas is the number of seed clients in the cluster.
	SeedClientReplicas = 4

	// SeedClientProxyPort is the proxy port of the seed clients, the default
	// of the dragonfly chart.
	SeedClientProxyPort = 4001

	// ArtifactDir is the directory shared with the kind nodes, the file server
	// serves from it and the logs are collected into it.
	ArtifactDir = "/tmp/artifact"
)

const (
	SchedulerServerName  = "scheduler"
	SeedClientServerName = "seed-client"
	FileServerName       = "dufs"
	RunnerName           = "e2e-runner"
)

// server is a component whose pod logs are collected as artifacts.
type server struct {
	Name      string
	Namespace string
}

// Servers is the components whose pod logs are collected as artifacts.
var Servers = map[string]server{
	SchedulerServerName: {
		Name:      SchedulerServerName,
		Namespace: DragonflyNamespace,
	},
	SeedClientServerName: {
		Name:      SeedClientServerName,
		Namespace: DragonflyNamespace,
	},
	FileServerName: {
		Name:      FileServerName,
		Namespace: E2ENamespace,
	},
}
