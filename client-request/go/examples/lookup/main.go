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

// Command lookup looks up the endpoints of the seed peers serving the url via
// the Dragonfly and prints them to stdout.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	request "d7y.io/dragonfly-sdk/client-request/go"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <scheduler-endpoint> <url>\n", os.Args[0])
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxy, err := request.New(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer proxy.Close()

	endpoints, err := proxy.LookupEndpoints(ctx, request.NewGetRequest(os.Args[2]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, endpoint := range endpoints {
		fmt.Println(endpoint)
	}
}
