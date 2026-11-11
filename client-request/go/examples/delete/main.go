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

// Command delete deletes a preheated file or an OCI image from the seed peers
// via the Dragonfly. A file is deleted from the replicas of the task by default
// or from all seed peers with the all_seed_peers scope, an image is deleted
// from all healthy seed peers.
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
	if len(os.Args) < 4 || len(os.Args) > 5 || (os.Args[2] != "file" && os.Args[2] != "image") {
		fmt.Fprintf(os.Stderr, "usage: %s <scheduler-endpoint> file|image <url-or-image> [default|all_seed_peers]\n", os.Args[0])
		os.Exit(1)
	}

	// Address the replicas of the task unless the scope is given, the scope only
	// applies to a file.
	scope := request.ScopeDefault
	if len(os.Args) == 5 {
		scope = request.Scope(os.Args[4])
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proxy, err := request.New(ctx, os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer proxy.Close()

	switch os.Args[2] {
	case "file":
		err = proxy.Delete(ctx, request.NewDeleteRequest(os.Args[3], request.WithDeleteRequestScope(scope)))
	case "image":
		var resp *request.DeleteImageResponse
		resp, err = proxy.DeleteImage(ctx, request.NewDeleteImageRequest(os.Args[3]))
		if err == nil {
			for _, peer := range resp.Peers {
				fmt.Printf("peer %s (%s) deleted %d tasks and retained %d tasks\n", peer.Hostname, peer.IP, len(peer.DeletedTaskIDs), len(peer.RetainedTaskIDs))
			}
		}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
