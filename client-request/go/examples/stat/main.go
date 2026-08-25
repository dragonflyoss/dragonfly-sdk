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

// Command stat queries the distribution of an OCI image in the seed peers via
// the Dragonfly.
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
		fmt.Fprintf(os.Stderr, "usage: %s <scheduler-endpoint> <image>\n", os.Args[0])
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

	resp, err := proxy.StatImage(ctx, request.NewStatImageRequest(os.Args[2]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("image has %d layers\n", len(resp.Layers))
	for _, peer := range resp.Peers {
		var finished int
		for _, layer := range peer.CachedLayers {
			if layer.IsFinished {
				finished++
			}
		}

		fmt.Printf("peer %s (%s) finished %d of %d cached layers\n", peer.Hostname, peer.IP, finished, len(peer.CachedLayers))
	}
}
