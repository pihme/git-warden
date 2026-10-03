# Contributing

Source-available under [PolyForm Noncommercial 1.0.0](LICENSE). Other licenses can be negotiated with the copyright holder.

## Contributions

This project does not accept outside code contributions at the moment, so that its licensing stays in one hand. Issues, bug reports and ideas are very welcome: please [open an issue](https://github.com/pihme/git-warden/issues/new/choose).

## Good issues

Pick the matching [issue form](https://github.com/pihme/git-warden/issues/new/choose) (bug report, feature request, documentation, question). A good report names the Git Warden version or commit, your OS and runtime versions, what you ran, what you expected and what happened.

## Security and secrets

The tracker is public. Never paste tokens, keys, credentials or real service responses into an issue. Please report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).

## Build and test locally

Requirements: Go 1.24 or newer and `git` 2.42 or newer. [gitleaks](https://github.com/gitleaks/gitleaks) 8.x on `PATH` is optional for the tests: without it, the secret-scan tests are skipped (a running Push Guard needs it, see [Requirements](README.md#requirements)). [git-everref](https://github.com/daojyun/git-everref) v1.0.0 on `PATH` is optional too: without it, the Pull Guard's end-to-end tests are skipped (a running Pull Guard needs it; `scripts/install-everref.sh` installs the pinned release). Nothing is downloaded for you; install these yourself. The tests are offline; they create temporary Git repositories and need no accounts, tokens or network.

```bash
go vet ./...
go test ./...
go build -o push-guard ./cmd/push-guard
go build -o pull-guard ./cmd/pull-guard
```
