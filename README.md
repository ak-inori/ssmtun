# ssmtun

Supervised AWS Systems Manager port-forwarding tunnels to RDS, Aurora, ElastiCache or anything else reachable from an SSM-managed bastion.

`ssmtun` replaces the shell script most teams end up writing around `aws ssm start-session`:

- **Named targets** in YAML, shared through `~/.config/ssmtun/config.d/` or a project-level `.ssmtun.yaml`
- **One process per tunnel** with an end-to-end health probe every 15 seconds and automatic reconnect with backoff
- **AWS SSO login** built in (device-code flow, token cache compatible with the AWS CLI)
- **Clean shutdown**: Ctrl-C or `ssmtun down` ends the SSM session and never leaves `session-manager-plugin` orphans holding your ports
- **No AWS CLI needed**; only the [Session Manager plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)

```
$ ssmtun up app-prd-ro app-dev
2 tunnel(s) starting; press Ctrl-C to stop all
10:21:04 [app-dev] up: 127.0.0.1:20001 -> dev-app.xxxx.ap-northeast-1.rds.amazonaws.com:5432 via i-0123456789abcdef0 (app), session johndoe-abc123
10:21:05 [app-prd-ro] up: 127.0.0.1:30011 -> prd-app.cluster-ro-xxxx.ap-northeast-1.rds.amazonaws.com:5432 via i-0fedcba9876543210 (app), session johndoe-def456
```

## Install

```sh
brew install --cask session-manager-plugin   # once
brew install ak-inori/tap/ssmtun
```

Or with Go: `go install github.com/ak-inori/ssmtun/cmd/ssmtun@latest`.

## Configure

```sh
ssmtun config init      # writes ~/.config/ssmtun/config.yaml
ssmtun doctor           # checks plugin, config and AWS profiles
```

```yaml
defaults:
  health_interval: 15s
  probe_timeout: 5s

targets:
  app-dev:
    profile: app                       # profile in ~/.aws/config; SSO profiles work
    instance: i-0123456789abcdef0      # SSM-managed bastion, or: instance_tag: Name=bastion
    host: dev-app.xxxx.ap-northeast-1.rds.amazonaws.com
    port: 5432
    local_port: 20001
    engine: postgres                   # postgres | mysql | tcp (guessed from port)
    color: cyan                        # log prefix color (auto-assigned like docker compose when omitted)
```

Files are merged in this order, later files overriding earlier ones per target:

1. `~/.config/ssmtun/config.yaml`
2. `~/.config/ssmtun/config.d/*.yaml` — drop a team-managed file here
3. `./.ssmtun.yaml`, searched upward from the current directory — commit one to a project repo

## Use

| Command | What it does |
|---|---|
| `ssmtun up <target>...` | Start tunnels in the foreground; Ctrl-C stops them all. Log prefixes are colored per target (`--no-color` or `NO_COLOR` to disable) |
| `ssmtun up -d <target>...` | Start in the background and return once each tunnel is up |
| `ssmtun status` | Show running tunnels, uptime and reconnect counts |
| `ssmtun down <target>... \| --all` | Stop background tunnels (`--force` also kills strangers on the port) |
| `ssmtun login [profile...]` | Refresh SSO credentials for the profiles your targets use |
| `ssmtun targets` | List configured targets |
| `ssmtun doctor` | Check prerequisites |
| `ssmtun completion fish\|zsh\|bash` | Shell completion with target names |

Point your database client at `127.0.0.1:<local_port>` and keep it there; the port stays stable across reconnects.

## How it stays up

Session Manager ends port-forwarding sessions after an idle period (20 minutes by default) and occasionally drops the WebSocket under them. Each `ssmtun` worker:

1. Calls `ssm:StartSession` with the `AWS-StartPortForwardingSessionToRemoteHost` document and runs `session-manager-plugin` as a direct child, the same way the AWS CLI does.
2. Every `health_interval`, connects through the tunnel and performs a protocol-level handshake: a PostgreSQL `CancelRequest` (answered by a silent close, nothing is logged server-side) or reading the MySQL greeting. This proves the whole path works and counts as traffic for the idle timer.
3. After two consecutive failed probes, or as soon as the plugin exits, tears the session down and reconnects with exponential backoff (1 s to 30 s).
4. When credentials expire it reports `auth_required` and keeps retrying; run `ssmtun login` in another terminal and it picks the new token up.

State lives in `~/.local/state/ssmtun/`; detached workers log to `<target>.log` there.

## Requirements

- `session-manager-plugin` 1.2.463.0 or newer on your PATH
- Bastion running SSM Agent 3.1.1476.0 or newer (older agents drop remote-host sessions after 30 s idle)
- IAM permissions for `ssm:StartSession` (with the document and target), `ssm:TerminateSession`, `sts:GetCallerIdentity`, and `ec2:DescribeInstances` when using `instance_tag`

## License

MIT
