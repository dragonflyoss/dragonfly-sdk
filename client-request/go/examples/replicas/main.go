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

// Command replicas preheats the url to three replicas of seed peers via the
// Dragonfly, then downloads it with the request scattered across the replicas,
// writing the content to stdout. The default replicas is 2 when not set.
package main

import (
	"context"
	"fmt"
	"io"
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

	// Preheat the file to 3 replicas of seed peers.
	if err := proxy.Preheat(ctx, request.NewPreheatRequest(os.Args[2], request.WithPreheatRequestReplicas(3))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Download the file with the request scattered across the 3 replicas.
	resp, err := proxy.Get(ctx, request.NewGetRequest(os.Args[2], request.WithGetRequestReplicas(3)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
