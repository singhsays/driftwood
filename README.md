# driftwood

Turn config drift into git history.

driftwood periodically connects to the devices and servers in your homelab - routers, firewalls, hypervisors, NAS - and saves their configuration to a git repository. When a config changes, you get a new commit. Nothing is committed if there are no changes.

## How it works

- Fetches on demand (`driftwood run`) or continuously (`driftwood daemon`) on any host that can reach your devices over SSH
- Each device has a **model** — a tiny recipe for fetching its config (run a command, grab a file)
- Fetched configs are hashed; a commit is made **only when something changed**
- Everything can be pushed to a git remote

driftwood deliberately backs up *configuration, not data*. It won't save your VM disks or your database, pair it with a proper backup tool for that.

## Quick start

Build:

```sh
go build -o driftwood ./cmd/driftwood
```

Describe your devices in `config.yaml`:

```yaml
defaults:
  user: root
  interval: 24h
  keyfile: ~/.ssh/id_ed25519

nodes:
  - name: switch
    model: routeros
    host: switch.lan

  - name: gateway
    model: pfsense
    host: gateway.lan

  - name: proxmox
    model: proxmox
    host: pve.lan

  - name: nas
    model: generic
    host: nas.lan
    commands: ["cat /etc/version"]
```

Fetch everything once and see what you get:

```sh
driftwood run --config config.yaml
```

That's it. Point it at a git repo (see below) to keep the history.

## Built-in models

| Model | Device | Fetches |
|---|---|---|
| `routeros` | MikroTik | `/export` |
| `pfsense` | pfSense | `/conf/config.xml` |
| `proxmox` | Proxmox VE host | network, cluster, hosts config |
| `pihole` | Pi-hole v6 | FTL settings dump |
| `pct-config` | Proxmox guests | CT/VM definitions |
| `generic` | anything with SSH | commands you specify |

Devices need SSH key auth from the host running driftwood. Host keys are trusted on first use and recorded in `~/.driftwood/known_hosts`.

## Saving history to a git repo

Configure where the configs should go:

```yaml
git:
  remote: git@github.com:you/homelab-config.git
  branch: main
  keyfile: ~/.ssh/driftwood_deploy_key
  name: driftwood
  email: driftwood@yourhost
```

Each node's artifacts are committed under `nodes/<name>/`, with a commit only when a config actually changed. Browse the repo like any git history - diffs tell you exactly what moved, and when.

> Binary artifacts (databases, export archives) are stored and hash-compared,
> but not diffed. Files containing secrets can be encrypted before commit.

## License

MIT
