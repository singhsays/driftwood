# driftwood

## Commit conventions

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <description>

[optional body]

[optional footer(s)]
```

Types: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`.

- `feat:` → MINOR bump, `fix:` → PATCH bump (enforced at release time, not per commit)
- `BREAKING CHANGE:` footer (or `!` after type) → MAJOR bump
- Releases are cut by tagging `v<major>.<minor>.<patch>`; the changelog is
  generated from conventional commits (GoReleaser in GitHub Actions, once
  the first public release happens)

## Versioning policy

- Semver. The git tag is the single source of truth; binaries derive their
  version at build time via ldflags (`make build`), with a
  `debug.ReadBuildInfo()` fallback for bare `go build`.
- Pre-1.0: `0.x` — MINOR bumps may break config format or CLI (no stability
  promise until 1.0).
- What counts as the API (MAJOR if changed): `config.yaml` format, CLI
  surface, and the output repo layout (`nodes/<name>/`).

## Principles

- Built-in models encode universal product knowledge, never one operator's
  choices. Personalization belongs in `config.yaml` (`generic` model with
  per-node `commands:`/`files:`).
- Roadmap idea (deferred): YAML-defined model extension (`extends:`) once
  the config format stabilizes near 1.0.

## Scope and non-goals

Driftwood is **pull-based observation**, not push-based configuration
management. It records what devices look like; it never changes them.
Consequences, held as policy:

- **Read-only by construction.** Driftwood never writes to managed
  devices. Its worst case is a failed fetch — never a misconfigured
  device. This differentiates it from Ansible-style config management
  and is not up for negotiation.
- **No push/enforce/remediation mode. Ever.** Restoring a device stays a
  human procedure (runbooks). If automated correction is ever wanted,
  the architecture is "driftwood detects drift → hand off to an external
  tool" — not growing config management inside a backup tool.
- **Models are declarative data, not logic.** Commands, files, globs,
  flags. The moment a model spec needs loops-with-conditionals,
  variables, or templating, that logic belongs in Go (or in a script in
  the operator's repo, executed via the `generic` model). No DSL
  accretion.
- **Targets need nothing but SSH.** No agent, no Python on targets, no
  temp files on managed devices. Network gear and NASes are first-class
  targets, not special cases. One refinement: file-fetching models
  (`files:` entries) additionally require the device's standard SFTP
  subsystem — present by default on OpenSSH-based devices. Devices
  without SFTP can still be backed up via command-based models
  (`generic` with `commands:`).
- **Keep the dependency budget tiny.** Every dependency is a
  supply-chain surface; justify each one.

## Project layout

- `cmd/driftwood/` — CLI entrypoint (kong); keep thin, logic lives below
- `internal/config/` — YAML config
- `internal/model/` — fetch models (exec/files transports; `model.Client`
  interface lets tests use fakes instead of SSH)
- `internal/ssh/` — SSH client (key auth, TOFU known_hosts)
- `internal/runner/` — fetch orchestration, run/daemon loops
- `internal/version/` — build version reporting

## Quality gates

- `make check` = vet + lint + test -race + build. CI runs the same; run it
  before every commit.
- Lint config is `.golangci.yml` (curated: revive, misspell, errorlint,
  gosec, unparam, gocritic, gofumpt). Fix findings; don't suppress.
  The only standing exclusion: gosec G304 (paths from the operator's own
  config file) and misspell for the RouterOS product name.
- `.github/workflows/ci.yml` runs `make check`; `govulncheck.yml` scans
  weekly; dependabot bumps Go modules and Actions.
