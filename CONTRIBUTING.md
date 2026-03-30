# Contributing to stackd

Thanks for your interest in contributing! Before submitting a pull request, please read the following.

## Contributor License Agreement

All contributions require acceptance of the [Contributor License Agreement (CLA)](CLA.md). By opening a pull request you confirm that you have read and agree to its terms. This allows the project to be offered under both the AGPL-3.0 open-source license and a commercial license.

## Getting Started

```bash
# Clone the repo
git clone https://github.com/antnsn/stackd.git
cd stackd

# Build the frontend
cd internal/ui && npm install && npm run build && cd ../..

# Build the binary
go build ./...

# Run locally (requires Docker)
go run . 
```

## Development

- **Backend:** Go 1.25+, standard library preferred. New packages go under `internal/`.
- **Frontend:** Preact + Vite in `internal/ui/`. Run `npm run dev` for hot-reload.
- **Logging:** Use `log/slog` only — no `log.Printf`.
- **Errors:** Always wrap with context: `fmt.Errorf("operation %s: %w", name, err)`.
- **Tests:** All new Go code must have `_test.go` coverage. Table-driven tests preferred.

## Pull Request Guidelines

- Keep PRs focused — one feature or fix per PR.
- Include a clear description of what changed and why.
- Ensure `go build ./...` and frontend `npm run build` pass before submitting.
- For significant changes, open an issue first to discuss the approach.

## Licensing

stackd is licensed under [AGPL-3.0](LICENSE). For commercial licensing enquiries, see [COMMERCIAL.md](COMMERCIAL.md).
