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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
)

const (
	// driverDir is the directory of the SDK drivers in the runner pod, shared
	// with the host through the kind nodes.
	driverDir = ArtifactDir + "/bin"

	// HeaderTaskID is the response header carrying the task id, set by the
	// seed peer proxy.
	HeaderTaskID = "x-dragonfly-task-id"

	// HeaderServerIP is the response header carrying the ip of the seed peer
	// that served the request, set by the seed peer proxy.
	HeaderServerIP = "x-dragonfly-server-ip"

	// HeaderTaskDownloadFinished is the response header set to "true" by the
	// seed peer proxy when the task was already finished when the request
	// arrived, i.e. the request hit the cache.
	HeaderTaskDownloadFinished = "x-dragonfly-task-download-finished"
)

// SDK drives a client-request SDK through its e2e driver running in the runner
// pod. The Go and the Rust drivers speak the same command line and JSON output.
type SDK struct {
	// Name is the SDK name, "go" or "rust".
	Name string

	// driver is the path of the driver binary in the runner pod.
	driver string
}

// SDKs are the SDKs under test.
var SDKs = []*SDK{
	{Name: "go", driver: driverDir + "/driver-go"},
	{Name: "rust", driver: driverDir + "/driver-rust"},
}

// GetResponse is the output of the get command of the drivers.
type GetResponse struct {
	// StatusCode is the status code of the response.
	StatusCode int `json:"status_code"`

	// Header is the headers of the response, with lower case keys.
	Header map[string]string `json:"header"`
}

// StatImageResponse is the output of the stat-image command of the drivers.
type StatImageResponse struct {
	// Layers is the blob urls of the image.
	Layers []string `json:"layers"`

	// Peers is the seed peers that have cached layers of the image.
	Peers []*PeerStatedImage `json:"peers"`
}

// PeerStatedImage is a seed peer and the image layers it has cached.
type PeerStatedImage struct {
	// IP is the IP address of the seed peer.
	IP string `json:"ip"`

	// Hostname is the hostname of the seed peer.
	Hostname string `json:"hostname"`

	// CachedLayers is the layers the seed peer has cached.
	CachedLayers []*Layer `json:"cached_layers"`
}

// Layer is the download state of an image layer on a seed peer.
type Layer struct {
	// URL is the blob url of the layer.
	URL string `json:"url"`

	// IsFinished indicates whether the seed peer has finished downloading the layer.
	IsFinished bool `json:"is_finished"`
}

// DeleteImageResponse is the output of the delete-image command of the drivers.
type DeleteImageResponse struct {
	// Peers is the seed peers the image is deleted from.
	Peers []*PeerDeletedImage `json:"peers"`
}

// PeerDeletedImage is a seed peer and the tasks of the image it has deleted and
// retained.
type PeerDeletedImage struct {
	// IP is the IP address of the seed peer.
	IP string `json:"ip"`

	// Hostname is the hostname of the seed peer.
	Hostname string `json:"hostname"`

	// DeletedTaskIDs is the ids of the deleted tasks.
	DeletedTaskIDs []string `json:"deleted_task_ids"`

	// RetainedTaskIDs is the ids of the retained tasks.
	RetainedTaskIDs []string `json:"retained_task_ids"`
}

// GetDeletedTaskIDs returns the distinct ids of the tasks the seed peers deleted.
func (r *DeleteImageResponse) GetDeletedTaskIDs() []string {
	var taskIDs []string
	for _, peer := range r.Peers {
		for _, taskID := range peer.DeletedTaskIDs {
			if !slices.Contains(taskIDs, taskID) {
				taskIDs = append(taskIDs, taskID)
			}
		}
	}

	return taskIDs
}

// GetRetainedTaskIDs returns the distinct ids of the tasks the seed peers
// retained.
func (r *DeleteImageResponse) GetRetainedTaskIDs() []string {
	var taskIDs []string
	for _, peer := range r.Peers {
		for _, taskID := range peer.RetainedTaskIDs {
			if !slices.Contains(taskIDs, taskID) {
				taskIDs = append(taskIDs, taskID)
			}
		}
	}

	return taskIDs
}

// LookupEndpoints looks up the proxy endpoints of the seed peers serving the
// url through the SDK.
func (s *SDK) LookupEndpoints(url string) ([]string, error) {
	var out struct {
		Endpoints []string `json:"endpoints"`
	}
	if err := s.run(&out, "lookup-endpoints", url); err != nil {
		return nil, err
	}

	return out.Endpoints, nil
}

// Get downloads the url into the output path in the runner pod through the
// SDK, via the seed peers the scheduler picks or via the endpoints when given.
func (s *SDK) Get(url, output string, endpoints []string, header http.Header) (*GetResponse, error) {
	args := []string{"--output", output}
	for _, endpoint := range endpoints {
		args = append(args, "--endpoint", endpoint)
	}
	for key, values := range header {
		for _, value := range values {
			args = append(args, "--header", fmt.Sprintf("%s: %s", key, value))
		}
	}
	args = append(args, url)

	var resp GetResponse
	if err := s.run(&resp, "get", args...); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Preheat preheats the url to the seed peers through the SDK, to the default
// replicas or to the scope when given.
func (s *SDK) Preheat(url, scope string) error {
	var args []string
	if scope != "" {
		args = append(args, "--scope", scope)
	}

	return s.run(nil, "preheat", append(args, url)...)
}

// PreheatImage preheats the image to the seed peers through the SDK, to the
// default replicas or to the scope when given, for all the platforms of the
// image or for the platform when given.
func (s *SDK) PreheatImage(image, scope, platform string) error {
	var args []string
	if scope != "" {
		args = append(args, "--scope", scope)
	}

	if platform != "" {
		args = append(args, "--platform", platform)
	}

	return s.run(nil, "preheat-image", append(args, image)...)
}

// StatImage queries the layers of the image cached by the seed peers through
// the SDK, for all the platforms of the image or for the platform when given.
func (s *SDK) StatImage(image, platform string) (*StatImageResponse, error) {
	var args []string
	if platform != "" {
		args = append(args, "--platform", platform)
	}

	var resp StatImageResponse
	if err := s.run(&resp, "stat-image", append(args, image)...); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Delete deletes the preheated url from the seed peers through the SDK, from
// the default replicas or from the scope when given.
func (s *SDK) Delete(url, scope string) error {
	var args []string
	if scope != "" {
		args = append(args, "--scope", scope)
	}

	return s.run(nil, "delete", append(args, url)...)
}

// DeleteImage deletes the preheated image from all seed peers through the SDK,
// for all the platforms of the image or for the platform when given.
func (s *SDK) DeleteImage(image, platform string) (*DeleteImageResponse, error) {
	var args []string
	if platform != "" {
		args = append(args, "--platform", platform)
	}

	var resp DeleteImageResponse
	if err := s.run(&resp, "delete-image", append(args, image)...); err != nil {
		return nil, err
	}

	return &resp, nil
}

// run runs the command of the driver against the scheduler in the runner pod
// and decodes its JSON output into out when out is not nil.
func (s *SDK) run(out any, command string, args ...string) error {
	cmd := RunnerExec().Command(append([]string{s.driver, command, "--scheduler", SchedulerEndpoint}, args...)...)
	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s driver %s: %w: %s", s.Name, command, err, exitErr.Stderr)
		}

		return fmt.Errorf("%s driver %s: %w", s.Name, command, err)
	}

	fmt.Printf("%s driver %s output: %s", s.Name, command, stdout)
	if out == nil {
		return nil
	}

	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("%s driver %s: decode output %q: %w", s.Name, command, stdout, err)
	}

	return nil
}
