# E2E Tests

The e2e tests run the Go and the Rust `client-request` SDKs against a Dragonfly
cluster in [kind](https://kind.sigs.k8s.io/): a scheduler and four seed peers
installed by the [Dragonfly helm chart](https://github.com/dragonflyoss/helm-charts),
a [dufs](https://github.com/sigoden/dufs) file server serving the generated
files and a runner pod the SDKs run in.

Every spec runs against both SDKs through the drivers in `driver/go` and
`driver/rust`, two small commands speaking the same command line and JSON
output over each SDK. The specs preheat files and OCI images with the default
two replicas, then check the preheats land on the seed peers `LookupEndpoints`
selects and only on them, and that the gets, through the scheduler or bound to
the looked up endpoints, are served from the preheated cache: the response
carries the task id, the ip of a preheated seed peer and the download finished
flag, the content matches and the file server sees no further download.

## Run locally

Requirements: Docker, kind, kubectl, helm, Go, Rust, protoc and
[ginkgo](https://onsi.github.io/ginkgo/).

The runner pod runs the drivers from `/tmp/artifact/bin`, which the kind nodes
mount, so the drivers must be built for Linux and the architecture of the
Docker host. On Linux `make build-e2e-drivers` does; on macOS build the Rust
driver in a container, e.g.:

```shell
docker run --rm -v "$PWD:/work" -w /work/test/e2e/driver/rust rust:1.88-bookworm \
  sh -c 'apt-get update -qq && apt-get install -y -qq protobuf-compiler cmake && cargo build --release'
```

Then create the cluster and run the tests:

```shell
mkdir -p /tmp/artifact/bin /tmp/artifact/dufs && chmod -R 777 /tmp/artifact
kind create cluster --name kind --config test/testdata/kind/config.yaml

helm repo add dragonfly https://dragonflyoss.github.io/helm-charts/
helm install --wait --timeout 15m --create-namespace --namespace dragonfly-system \
  -f test/testdata/charts/config.yaml dragonfly dragonfly/dragonfly --version 1.7.9
kubectl apply -f test/testdata/k8s/dufs.yaml
kubectl apply -f test/testdata/k8s/e2e-runner.yaml
kubectl wait po dufs-0 --namespace dragonfly-e2e --for=condition=ready --timeout=10m
kubectl wait po e2e-runner --namespace dragonfly-e2e --for=condition=ready --timeout=10m

make e2e-test
make clean-e2e-test
```

The pod logs are collected into `/tmp/artifact/<component>/` after the suite.
