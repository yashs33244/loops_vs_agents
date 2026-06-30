# CLAUDE.md

This Go module is the Graph Harness (SGH) execution engine. For everything you need to work here -
architecture, the package map, the one hard rule (single-writer scheduler, no shared writes, tests run
with `-race`), how to run it, and how to extend it - read **[`AGENTS.md`](AGENTS.md)**.

Build contract: [`ENGINE_SPEC.md`](ENGINE_SPEC.md). Paper background: [`../PAPER_EXPLAINED.md`](../PAPER_EXPLAINED.md).

Fast checks:

```bash
go build ./... && go vet ./... && go test ./...
go run ./cmd/sgh run examples/bugfix.json --provider mock
```
