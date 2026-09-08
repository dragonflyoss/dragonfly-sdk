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
//	driver preheat --scheduler <endpoint> <url>
//	driver preheat-image --scheduler <endpoint> <image>
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
	fmt.Fprintf(os.Stderr, "usage: %s lookup-endpoints|get|preheat|preheat-image [flags] <url-or-image>\n", os.Args[0])
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

	h, err := parseHeader(header)
	if err != nil {
		return err
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

	resp, err := proxy.Get(ctx, request.NewGetRequest(flags.Arg(0), request.WithGetRequestHeader(h)))
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

	return json.NewEncoder(os.Stdout).Encode(getResponse{
		StatusCode: resp.StatusCode,
		Header:     lowerHeader(resp.Header),
	})
}

// preheat preheats the url to the seed peers.
func preheat(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("preheat", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
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

	return proxy.Preheat(ctx, request.NewPreheatRequest(flags.Arg(0)))
}

// preheatImage preheats the image to the seed peers.
func preheatImage(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("preheat-image", flag.ExitOnError)
	schedulerEndpoint := flags.String("scheduler", "", "scheduler endpoint")
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

	return proxy.PreheatImage(ctx, request.NewPreheatImageRequest(flags.Arg(0)))
}

// parseHeader parses the repeatable "Key: Value" header flags.
func parseHeader(values []string) (http.Header, error) {
	header := make(http.Header, len(values))
	for _, value := range values {
		key, val, ok := strings.Cut(value, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header %q, expected \"Key: Value\"", value)
		}

		header.Add(strings.TrimSpace(key), strings.TrimSpace(val))
	}

	return header, nil
}

// lowerHeader flattens the header to a map with lower case keys, the last
// value wins for duplicate keys, matching the Rust driver.
func lowerHeader(header http.Header) map[string]string {
	m := make(map[string]string, len(header))
	for key, values := range header {
		m[strings.ToLower(key)] = values[len(values)-1]
	}

	return m
}
