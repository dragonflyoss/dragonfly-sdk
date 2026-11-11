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

// Command driver drives the Go client-request SDK for the e2e tests. It is the
// Go counterpart of the Rust driver and speaks the same command line and JSON
// output, so the e2e suite runs the same specs against both SDKs.
//
// Usage:
//
//	driver lookup-endpoints --scheduler <endpoint> <url>
//	driver get --scheduler <endpoint> [--endpoint <endpoint>]... [--header <key: value>]... --output <path> <url>
//	driver preheat --scheduler <endpoint> [--scope <scope>] <url>
//	driver preheat-image --scheduler <endpoint> [--scope <scope>] [--platform <platform>] <image>
//	driver stat-image --scheduler <endpoint> [--platform <platform>] <image>
//	driver delete --scheduler <endpoint> [--scope <scope>] <url>
//	driver delete-image --scheduler <endpoint> [--platform <platform>] <image>
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	request "d7y.io/dragonfly-sdk/client-request/go"
)

// lookupEndpointsResponse is the JSON output of the lookup-endpoints command.
type lookupEndpointsResponse struct {
	// Endpoints is the proxy endpoints of the seed peers serving the url.
	Endpoints []string `json:"endpoints"`
}

// getResponse is the JSON output of the get command.
type getResponse struct {
	// StatusCode is the status code of the response.
	StatusCode int `json:"status_code"`

	// Header is the headers of the response, with lower case keys.
	Header map[string]string `json:"header"`
}

// statImageResponse is the JSON output of the stat-image command.
type statImageResponse struct {
	// Layers is the blob urls of the image.
	Layers []string `json:"layers"`

	// Peers is the seed peers that have cached layers of the image.
	Peers []peerStatedImage `json:"peers"`
}

// peerStatedImage is a seed peer and the image layers it has cached.
type peerStatedImage struct {
	// IP is the IP address of the seed peer.
	IP string `json:"ip"`

	// Hostname is the hostname of the seed peer.
	Hostname string `json:"hostname"`

	// CachedLayers is the layers the seed peer has cached.
	CachedLayers []layer `json:"cached_layers"`
}

// layer is the download state of an image layer on a seed peer.
type layer struct {
	// URL is the blob url of the layer.
	URL string `json:"url"`

	// IsFinished indicates whether the seed peer has finished downloading the layer.
	IsFinished bool `json:"is_finished"`
}

// deleteImageResponse is the JSON output of the delete-image command.
type deleteImageResponse struct {
	// Peers is the seed peers the image is deleted from.
	Peers []peerDeletedImage `json:"peers"`
}

// peerDeletedImage is a seed peer and the tasks of the image it has deleted and
// retained.
type peerDeletedImage struct {
	// IP is the IP address of the seed peer.
	IP string `json:"ip"`

	// Hostname is the hostname of the seed peer.
	Hostname string `json:"hostname"`

	// DeletedTaskIDs is the ids of the deleted tasks.
	DeletedTaskIDs []string `json:"deleted_task_ids"`

	// RetainedTaskIDs is the ids of the retained tasks.
	RetainedTaskIDs []string `json:"retained_task_ids"`
}

// stringSliceFlag collects the values of a repeatable flag.
type stringSliceFlag []string

// String implements flag.Value.
func (f *stringSliceFlag) String() string {
	return strings.Join(*f, ",")
}

// Set implements flag.Value.
func (f *stringSliceFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "lookup-endpoints":
		err = lookupEndpoints(ctx, os.Args[2:])
	case "get":
		err = get(ctx, os.Args[2:])
	case "preheat":
		err = preheat(ctx, os.Args[2:])
	case "preheat-image":
		err = preheatImage(ctx, os.Args[2:])
	case "stat-image":
		err = statImage(ctx, os.Args[2:])
	case "delete":
		err = delete(ctx, os.Args[2:])
	case "delete-image":
		err = deleteImage(ctx, os.Args[2:])
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// usage prints the usage and exits.
func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s lookup-endpoints|get|preheat|preheat-image|stat-image|delete|delete-image [flags] <url-or-image>\n", os.Args[0])
	os.Exit(1)
}

// lookupEndpoints looks up the proxy endpoints of the seed peers serving the
// url and prints them.
func lookupEndpoints(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("lookup-endpoints", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("lookup-endpoints takes exactly one url")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	endpoints, err := proxy.LookupEndpoints(ctx, request.NewGetRequest(flags.Arg(0)))
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(lookupEndpointsResponse{Endpoints: endpoints})
}

// get downloads the url into the output file, via the seed peers the scheduler
// picks or via the given endpoints, and prints the response status and headers.
func get(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("get", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	output := flags.String("output", "", "output file path")
	var endpoints, header stringSliceFlag
	flags.Var(&endpoints, "endpoint", "seed peer endpoint to send the request to, repeatable")
	flags.Var(&header, "header", "request header \"Key: Value\", repeatable")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("get takes exactly one url")
	}

	if *output == "" {
		return errors.New("--output is required")
	}

	requestHeader := make(http.Header, len(header))
	for _, value := range header {
		key, val, ok := strings.Cut(value, ":")
		if !ok {
			return fmt.Errorf("invalid header %q, expected \"Key: Value\"", value)
		}

		requestHeader.Add(strings.TrimSpace(key), strings.TrimSpace(val))
	}

	var proxy request.RequestWithEndpoints
	if len(endpoints) > 0 {
		proxyWithEndpoints, err := request.NewWithEndpoints(endpoints)
		if err != nil {
			return err
		}

		proxy = proxyWithEndpoints
	} else {
		p, err := request.New(ctx, *schedulerEndpoint)
		if err != nil {
			return err
		}
		defer p.Close()

		proxy = p
	}

	resp, err := proxy.Get(ctx, request.NewGetRequest(flags.Arg(0), request.WithGetRequestHeader(requestHeader)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(*output)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	// Flatten the header to a map with lower case keys, the last value wins for
	// duplicate keys, matching the Rust driver.
	responseHeader := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header {
		responseHeader[strings.ToLower(key)] = values[len(values)-1]
	}

	return json.NewEncoder(os.Stdout).Encode(getResponse{
		StatusCode: resp.StatusCode,
		Header:     responseHeader,
	})
}

// preheat preheats the url to the seed peers.
func preheat(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("preheat", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	scope := flags.String("scope", string(request.ScopeDefault), "seed peers to address, default or all_seed_peers")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("preheat takes exactly one url")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	return proxy.Preheat(ctx, request.NewPreheatRequest(flags.Arg(0), request.WithPreheatRequestScope(request.Scope(*scope))))
}

// preheatImage preheats the image to the seed peers, all its platforms unless
// the platform is given.
func preheatImage(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("preheat-image", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	scope := flags.String("scope", string(request.ScopeDefault), "seed peers to address, default or all_seed_peers")
	platform := flags.String("platform", "", "platform of the image in the format os/arch, all the platforms by default")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("preheat-image takes exactly one image")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	opts := []request.PreheatImageRequestOption{request.WithPreheatImageRequestScope(request.Scope(*scope))}
	if *platform != "" {
		opts = append(opts, request.WithPreheatImageRequestPlatform(*platform))
	}

	return proxy.PreheatImage(ctx, request.NewPreheatImageRequest(flags.Arg(0), opts...))
}

// statImage queries the layers of the image cached by the seed peers, all its
// platforms unless the platform is given, and prints them.
func statImage(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("stat-image", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	platform := flags.String("platform", "", "platform of the image in the format os/arch, all the platforms by default")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("stat-image takes exactly one image")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	var opts []request.StatImageRequestOption
	if *platform != "" {
		opts = append(opts, request.WithStatImageRequestPlatform(*platform))
	}

	resp, err := proxy.StatImage(ctx, request.NewStatImageRequest(flags.Arg(0), opts...))
	if err != nil {
		return err
	}

	peers := make([]peerStatedImage, 0, len(resp.Peers))
	for _, peer := range resp.Peers {
		cachedLayers := make([]layer, 0, len(peer.CachedLayers))
		for _, cachedLayer := range peer.CachedLayers {
			cachedLayers = append(cachedLayers, layer{URL: cachedLayer.URL, IsFinished: cachedLayer.IsFinished})
		}

		peers = append(peers, peerStatedImage{IP: peer.IP, Hostname: peer.Hostname, CachedLayers: cachedLayers})
	}

	return json.NewEncoder(os.Stdout).Encode(statImageResponse{Layers: append([]string{}, resp.Layers...), Peers: peers})
}

// delete deletes the preheated url from the seed peers.
func delete(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("delete", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	scope := flags.String("scope", string(request.ScopeDefault), "seed peers to address, default or all_seed_peers")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("delete takes exactly one url")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	return proxy.Delete(ctx, request.NewDeleteRequest(flags.Arg(0), request.WithDeleteRequestScope(request.Scope(*scope))))
}

// deleteImage deletes the preheated image from all seed peers, all its platforms
// unless the platform is given, and prints the tasks every seed peer deleted
// and retained.
func deleteImage(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("delete-image", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
	platform := flags.String("platform", "", "platform of the image in the format os/arch, all the platforms by default")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return errors.New("delete-image takes exactly one image")
	}

	proxy, err := request.New(ctx, *schedulerEndpoint)
	if err != nil {
		return err
	}
	defer proxy.Close()

	var opts []request.DeleteImageRequestOption
	if *platform != "" {
		opts = append(opts, request.WithDeleteImageRequestPlatform(*platform))
	}

	resp, err := proxy.DeleteImage(ctx, request.NewDeleteImageRequest(flags.Arg(0), opts...))
	if err != nil {
		return err
	}

	peers := make([]peerDeletedImage, 0, len(resp.Peers))
	for _, peer := range resp.Peers {
		peers = append(peers, peerDeletedImage{
			IP:              peer.IP,
			Hostname:        peer.Hostname,
			DeletedTaskIDs:  append([]string{}, peer.DeletedTaskIDs...),
			RetainedTaskIDs: append([]string{}, peer.RetainedTaskIDs...),
		})
	}

	return json.NewEncoder(os.Stdout).Encode(deleteImageResponse{Peers: peers})
}
