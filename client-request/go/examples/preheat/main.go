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

// Command preheat preheats a file or an OCI image to the seed peers via the
// Dragonfly.
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
	if len(os.Args) != 4 || (os.Args[2] != "file" && os.Args[2] != "image") {
		fmt.Fprintf(os.Stderr, "usage: %s <scheduler-endpoint> file|image <url-or-image>\n", os.Args[0])
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

	switch os.Args[2] {
	case "file":
		err = proxy.Preheat(ctx, request.NewPreheatRequest(os.Args[3]))
	case "image":
		err = proxy.PreheatImage(ctx, request.NewPreheatImageRequest(os.Args[3]))
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
