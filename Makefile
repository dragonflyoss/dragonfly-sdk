# Copyright 2026 The Dragonfly Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# ARTIFACT_DIR is shared with the kind nodes, the SDK drivers are built into
# its bin directory and run from there by the e2e runner pod.
ARTIFACT_DIR ?= /tmp/artifact

# Build the Go and the Rust SDK drivers of the e2e tests for the e2e runner pod.
build-e2e-drivers:
	@mkdir -p $(ARTIFACT_DIR)/bin
	@cd test/e2e && CGO_ENABLED=0 GOOS=linux go build -o $(ARTIFACT_DIR)/bin/driver-go ./driver/go
	@cd test/e2e/driver/rust && cargo build --release
	@cp test/e2e/driver/rust/target/release/driver $(ARTIFACT_DIR)/bin/driver-rust
.PHONY: build-e2e-drivers

# Run the e2e tests against the kind cluster, see test/e2e/README.md.
e2e-test:
	@cd test/e2e && ginkgo -v --race --fail-fast --trace --show-node-events .
.PHONY: e2e-test

# Delete the kind cluster of the e2e tests.
clean-e2e-test:
	@kind delete cluster --name kind
	@rm -rf $(ARTIFACT_DIR)
.PHONY: clean-e2e-test
