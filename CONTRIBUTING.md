# Contributing to monolog

Contributions are welcome — bug reports, fixes, and features. This guide covers
the essentials for building, testing, and getting a change merged.

## Building

monolog requires **Go 1.26+** (see [`go.mod`](go.mod)).

```bash
make build          # builds ./monolog
# or:
go build -o monolog .
```

Running the TUI locally is just the binary with no subcommand:

```bash
./monolog
```

To avoid touching your real backlog while developing, point `MONOLOG_DIR` at a
throwaway directory:

```bash
MONOLOG_DIR=$(mktemp -d) ./monolog
```

## Testing

```bash
make test                                    # go test ./...
go test ./internal/store/                     # a single package
go test -run TestCreate ./internal/store/     # a single test

make vet                                       # go vet ./...
```

## Development rules

- Every change must include tests.
- All tests must pass before merging (`make test` and `make vet` green).
- Keep changes small and focused.

## Planning workflow

Non-trivial work is planned first. Plans live in [`docs/plans/`](docs/plans/);
completed plans move to [`docs/plans/completed/`](docs/plans/completed/).

## License

Contributions are accepted under the project's MIT license — see
[LICENSE](LICENSE).
