# Dragonfly SDK

[![GitHub release](https://img.shields.io/github/release/dragonflyoss/dragonfly-sdk.svg)](https://github.com/dragonflyoss/dragonfly-sdk/releases)
[![CI (Rust)](https://github.com/dragonflyoss/dragonfly-sdk/actions/workflows/ci-rust.yml/badge.svg?branch=main)](https://github.com/dragonflyoss/dragonfly-sdk/actions/workflows/ci-rust.yml)
[![CI (Go)](https://github.com/dragonflyoss/dragonfly-sdk/actions/workflows/ci-go.yml/badge.svg?branch=main)](https://github.com/dragonflyoss/dragonfly-sdk/actions/workflows/ci-go.yml)
[![Open Source Helpers](https://www.codetriage.com/dragonflyoss/dragonfly-sdk/badges/users.svg)](https://www.codetriage.com/dragonflyoss/dragonfly-sdk)
[![Discussions](https://img.shields.io/badge/discussions-on%20github-blue?style=flat-square)](https://github.com/dragonflyoss/dragonfly/discussions)
[![Twitter](https://img.shields.io/twitter/url?style=social&url=https%3A%2F%2Ftwitter.com%2Fdragonfly_oss)](https://twitter.com/dragonfly_oss)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly/blob/main/LICENSE)

Public SDK for Dragonfly

## Packages

Each package lives in its own directory with one subdirectory per language.
Every package/language pair is released independently with prefixed tags
(e.g. `client-request/go/v1.5.0`).

| Package | Language | Location | Install |
|---|---|---|---|
| client-request | Rust | [client-request/rust](./client-request/rust) | [`dragonfly-client-request`](https://crates.io/crates/dragonfly-client-request) on crates.io |
| client-request | Go | [client-request/go](./client-request/go) | `go get d7y.io/dragonfly-sdk/client-request/go` |

The Rust and Go implementations of a package are functionally identical.
Cross-language consistency (task ids, hashring selection) is pinned by shared
test vectors in both test suites.

## Documentation

You can find the full documentation on the [d7y.io](https://d7y.io).

## Community

Join the conversation and help the community grow. Here are the ways to get involved:

- **Slack Channel**: [#dragonfly](https://cloud-native.slack.com/messages/dragonfly/) on [CNCF Slack](https://slack.cncf.io/)
- **Github Discussions**: [Dragonfly Discussion Forum](https://github.com/dragonflyoss/dragonfly/discussions)
- **Developer Group**: <dragonfly-developers@googlegroups.com>
- **Mailing Lists**:
  - **Developers**: <dragonfly-developers@googlegroups.com>
  - **Maintainers**: <dragonfly-maintainers@googlegroups.com>
- **Twitter**: [@dragonfly_oss](https://twitter.com/dragonfly_oss)
- **DingTalk Group**: `22880028764`

## Contributing

You should check out our
[CONTRIBUTING](./CONTRIBUTING.md) and develop the project together.
