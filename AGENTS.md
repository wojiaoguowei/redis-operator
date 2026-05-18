# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go Kubernetes operator for Redis. The entry point is `cmd/main.go`, with CLI wiring under `internal/cmd`. Custom resource API types live in `api/<resource>/v1beta2`; reconciliation and Kubernetes object construction are mainly under `internal/controller`, `internal/k8sutils`, `internal/service`, and `internal/webhook`. Generated CRDs, RBAC, webhook manifests, and samples are in `config/`. Helm charts are in `charts/`, examples in `example/v1beta2`, docs in `docs/`, dashboards in `dashboards/`, and static images in `static/`. Tests are colocated as `*_test.go`; end-to-end assets are under `tests/e2e-chainsaw`.

## Build, Test, and Development Commands

- `make manager`: generates code, formats, vets, and builds `bin/manager`.
- `make agent`: builds the auxiliary agent binary at `bin/agent`.
- `make run`: runs the operator locally against the current kubeconfig.
- `make manifests`: regenerates CRDs, RBAC, and webhook manifests from API markers.
- `make codegen`: refreshes generated code, CRDs, chart CRDs, metrics docs, and API docs.
- `make test`: runs envtest-backed Go tests with coverage in `cover.out`.
- `make unit-tests`: runs `go test ./... -race` and writes `coverage.out`.
- `make docker-build IMG=<repo>:<tag>`: builds the operator image.
- `make deploy IMG=<repo>:<tag>`: deploys to the active Kubernetes context.

## Coding Style & Naming Conventions

Use Go 1.23.4 conventions. Keep Go files formatted with `go fmt`; `golangci-lint` also enables `gofmt`, `gofumpt`, and `gci`. Package names are lowercase and directory-oriented. Test files must use the standard `*_test.go` suffix. Do not manually edit generated files such as `zz_generated.deepcopy.go`; update API definitions and run `make codegen`.

## Testing Guidelines

Prefer focused unit tests near the package being changed. Use existing frameworks in `go.mod`, including Ginkgo/Gomega, Testify, envtest, and redismock. Run `make test` before submitting API, webhook, or controller changes. For cluster behavior, create a kind cluster with `tests/_config/kind-config.yaml` and run Chainsaw tests:

```sh
chainsaw test tests/e2e-chainsaw/v1beta2 --config tests/_config/chainsaw-configuration.yaml
```

## Commit & Pull Request Guidelines

Recent history uses Conventional Commit-style subjects such as `fix(sentinel): ...`, `docs: ...`, and `chore(deps): ...`. Keep subjects imperative and scoped when useful. Pull requests should describe the behavior change, include test results, call out generated files, and link related issues. Include screenshots only for documentation, dashboard, or rendered chart changes.

## Security & Configuration Tips

Do not commit real credentials, kubeconfigs, or private registry secrets. Use sample manifests under `example/` and `tests/testdata/` only with non-production values. `make deploy`, `make install`, and `make uninstall` operate on the active Kubernetes context.
