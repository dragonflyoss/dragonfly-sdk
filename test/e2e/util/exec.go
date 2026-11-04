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

package util

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// KubeCtlCommand returns a kubectl command with the args.
func KubeCtlCommand(arg ...string) *exec.Cmd {
	fmt.Printf(`kubectl command: "kubectl" "%s"`+"\n", strings.Join(arg, `" "`))
	return exec.Command("kubectl", arg...)
}

// PodUID returns the uid of the pod in the namespace, which changes when a
// statefulset recreates the pod under the same name.
func PodUID(namespace, name string) (string, error) {
	out, err := KubeCtlCommand("-n", namespace, "get", "pod", name, "-o", "jsonpath={.metadata.uid}").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w, output: %s", namespace, name, err, out)
	}

	return strings.TrimSpace(string(out)), nil
}

// IsPodReady reports whether the pod in the namespace exists and is ready.
func IsPodReady(namespace, name string) bool {
	out, err := KubeCtlCommand("-n", namespace, "get", "pod", name,
		"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`).CombinedOutput()
	if err != nil {
		fmt.Printf("get pod %s/%s: %s, output: %s\n", namespace, name, err, out)
		return false
	}

	return strings.TrimSpace(string(out)) == "True"
}

// GetPodNames returns the names of the pods of the component in the namespace.
func GetPodNames(namespace, component string) ([]string, error) {
	out, err := KubeCtlCommand("-n", namespace, "get", "pod", "-l", fmt.Sprintf("component=%s", component),
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("get pods of %s/%s: %w, output: %s", namespace, component, err, out)
	}

	return strings.Fields(string(out)), nil
}

// PodExec runs commands in a container of a pod.
type PodExec struct {
	namespace string
	name      string
	container string
}

// NewPodExec returns a PodExec for the container of the pod.
func NewPodExec(namespace, name, container string) *PodExec {
	return &PodExec{
		namespace: namespace,
		name:      name,
		container: container,
	}
}

// Name returns the pod name.
func (p *PodExec) Name() string {
	return p.name
}

// Command returns a kubectl exec command running the args in the container.
func (p *PodExec) Command(arg ...string) *exec.Cmd {
	extArgs := []string{"-n", p.namespace, "exec", p.name, "--"}
	if p.container != "" {
		extArgs = []string{"-n", p.namespace, "exec", "-c", p.container, p.name, "--"}
	}

	extArgs = append(extArgs, arg...)
	fmt.Printf(`pod %s/%s exec: "%s"`+"\n", p.namespace, p.name, strings.Join(arg, `" "`))
	return KubeCtlCommand(extArgs...)
}

// RunnerExec returns the PodExec of the runner pod the SDK drivers run in.
func RunnerExec() *PodExec {
	return NewPodExec(E2ENamespace, RunnerName, "")
}

// SeedClient is a seed client pod and the ip it announces to the scheduler,
// its pod ip.
type SeedClient struct {
	// Pod runs commands in the seed client container.
	Pod *PodExec

	// IP is the ip the seed client announces to the scheduler.
	IP string
}

// ProxyEndpoint returns the proxy endpoint of the seed client, as returned by
// the SDK's LookupEndpoints.
func (s *SeedClient) ProxyEndpoint() string {
	return fmt.Sprintf("http://%s:%d", s.IP, SeedClientProxyPort)
}

// SeedClientNames returns the pod names of the seed clients behind the proxy
// endpoints, in the order of the endpoints.
func SeedClientNames(endpoints []string) ([]string, error) {
	seedClients, err := SeedClients()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		i := slices.IndexFunc(seedClients, func(seedClient *SeedClient) bool { return seedClient.ProxyEndpoint() == endpoint })
		if i < 0 {
			return nil, fmt.Errorf("no seed client serves endpoint %s", endpoint)
		}

		names = append(names, seedClients[i].Pod.Name())
	}

	return names, nil
}

// SeedClients returns the seed clients in the cluster.
func SeedClients() ([]*SeedClient, error) {
	out, err := KubeCtlCommand("-n", DragonflyNamespace, "get", "pod", "-l", fmt.Sprintf("component=%s", SeedClientServerName),
		"-o", `jsonpath={range .items[*]}{.metadata.name}{" "}{.status.podIP}{"\n"}{end}`).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("get seed client pods: %w, output: %s", err, out)
	}

	var seedClients []*SeedClient
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("unexpected seed client pod line: %q", line)
		}

		seedClients = append(seedClients, &SeedClient{
			Pod: NewPodExec(DragonflyNamespace, fields[0], SeedClientServerName),
			IP:  fields[1],
		})
	}

	return seedClients, nil
}
