# Contributing to GPUSwarm

Thank you for your interest in contributing to GPUSwarm! We welcome contributions from the community — whether it's bug reports, feature requests, documentation improvements, or code changes.

## 📋 Table of Contents

- [Code of Conduct](#code-of-conduct)
- [How to Contribute](#how-to-contribute)
- [Development Setup](#development-setup)
- [Pull Request Process](#pull-request-process)
- [Code Style](#code-style)
- [Reporting Issues](#reporting-issues)

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## How to Contribute

### 🐛 Reporting Bugs

1. Check [existing issues](https://github.com/partha-mohapatra/GPUSwarm/issues) to avoid duplicates
2. Open a new issue with:
   - Clear, descriptive title
   - Steps to reproduce the bug
   - Expected vs actual behaviour
   - System info (OS, Go version, backend type)
   - Relevant logs or error messages

### 💡 Suggesting Features

1. Open a [GitHub Issue](https://github.com/partha-mohapatra/GPUSwarm/issues/new) with the **enhancement** label
2. Describe the use-case and proposed behaviour
3. Include any mockups, diagrams, or references that help clarify your idea

### 🔧 Submitting Code

1. Fork the repository
2. Create a feature branch from `main`:
   ```bash
   git checkout -b feature/my-awesome-feature
   ```
3. Make your changes
4. Write or update tests as needed
5. Ensure all tests pass
6. Submit a Pull Request

## Development Setup

### Prerequisites

- **Go 1.21+** (check with `go version`)
- **Git**
- A local AI backend for testing (e.g., [Ollama](https://ollama.ai) or [LM Studio](https://lmstudio.ai))

### Building

```bash
# Clone your fork
git clone https://github.com/<your-username>/GPUSwarm.git
cd GPUSwarm

# Run tests
go test ./...

# Build all binaries
./scripts/build-all.sh
```

### Running Locally

```bash
# Start a provider node
./infermeshd-linux-amd64 -mode provider -backend-kind ollama

# Start a client node (separate terminal)
./infermeshd-linux-amd64 -mode client -client-addr :5555

# Launch chat UI
./infermeshd-linux-amd64 chat -daemon-url http://127.0.0.1:5555
```

## Pull Request Process

1. **Keep PRs focused** — one feature or fix per PR
2. **Write clear commit messages** following [conventional commits](https://www.conventionalcommits.org/)
3. **Update documentation** if your change affects user-facing behaviour
4. **Add tests** for new functionality
5. **Ensure CI passes** — all tests and linters must be green
6. **Request a review** — a maintainer will review your PR

### Commit Message Format

```
<type>(<scope>): <description>

[optional body]
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`

Examples:
```
feat(discovery): add mDNS peer discovery fallback
fix(relay): handle connection timeout gracefully
docs(readme): update quick-start instructions
```

## Code Style

- Follow standard [Go conventions](https://go.dev/doc/effective_go) and `gofmt`
- Use meaningful variable and function names
- Add comments for exported functions and complex logic
- Keep functions small and focused
- Handle errors explicitly — don't ignore them

### Linting

```bash
# Run the Go linter
go vet ./...
```

## Reporting Issues

If you encounter a security vulnerability, please **do not** open a public issue. Instead, email the maintainers directly or use GitHub's private vulnerability reporting feature.

---

<p align="center">
  <img src="assets/gpuswarm-hive-64.svg" width="32" alt="GPUSwarm">
  <br>
  <strong>Thank you for helping make GPUSwarm better! 🐝</strong>
</p>
