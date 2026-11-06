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

// Command get-with-seed-peers lists the seed peers from the scheduler, then
// downloads a file via the Dragonfly from them and writes it to stdout.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	request "d7y.io/dragonfly-sdk/client-request/go"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <scheduler-endpoint> <url>\n", os.Args[0])
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schedulerClient, err := request.NewSchedulerClient(os.Args[1], 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer schedulerClient.Close()

	seedPeers, err := schedulerClient.ListSeedPeers(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, seedPeer := range seedPeers {
		fmt.Fprintf(os.Stderr, "seed peer %s %s:%d healthy: %t\n", seedPeer.Host.Hostname, seedPeer.Host.Ip, seedPeer.Host.ProxyPort, seedPeer.Healthy)
	}

	proxy, err := request.NewWithSeedPeers(seedPeers)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	resp, err := proxy.Get(ctx, request.NewGetRequest(os.Args[2]))
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
