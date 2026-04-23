# Contributing to keyoku-engine

Thank you for your interest in contributing! This guide will help you get started.

## Reporting Bugs

Open a [GitHub Issue](https://github.com/keyoku-ai/keyoku-engine/issues/new?template=bug_report.md) with:

- A clear description of the bug
- Steps to reproduce
- Expected vs actual behavior
- Go version and OS

## Suggesting Features

Open a [feature request](https://github.com/keyoku-ai/keyoku-engine/issues/new?template=feature_request.md) describing:

- The problem you're trying to solve
- Your proposed solution
- Any alternatives you've considered

## Development Setup

```bash
# Clone the repo
git clone https://github.com/keyoku-ai/keyoku-engine.git
cd keyoku-engine

# Run tests
make test

# Run with race detector
make test-race

# Lint
make lint

# Build
make build
```

Requires Go 1.24+.

## Contributor License Agreement (CLA)

All contributors must sign our [Contributor License Agreement](CLA.md) before their pull request can be merged. When you open your first PR, a bot will comment with instructions — simply reply with the required comment to sign.

## Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Make your changes
4. Run `make test` and `make lint` to verify
5. Commit with a descriptive message (see below)
6. Push to your fork and open a PR
7. Sign the CLA if you haven't already (the bot will prompt you)

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(engine): add batch memory insertion
fix(storage): handle concurrent writes correctly
docs: update API reference
test(heartbeat): add edge case coverage
chore: update dependencies
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Run `make lint` before submitting
- Add tests for new functionality
- Keep functions focused and well-named

## License

By contributing, you agree that your contributions will be licensed under the [BSL 1.1](LICENSE).
