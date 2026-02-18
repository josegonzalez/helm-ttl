# helm-ttl

A Helm plugin that manages TTL (time-to-live) for Helm releases. When a TTL is set, the plugin creates a Kubernetes CronJob that automatically uninstalls the release at the scheduled time and cleans up after itself. This prevents abandoned releases from consuming cluster resources.

## Installation

```bash
helm plugin install https://github.com/josegonzalez/helm-ttl
```

## Usage

```bash
# Set a TTL on a release (auto-creates service account and RBAC)
helm ttl set my-release 24h --create-service-account

# Set TTL using days shorthand
helm ttl set my-release 7d --create-service-account

# Set TTL using natural language
helm ttl set my-release "next monday" --create-service-account

# Set TTL with custom service account
helm ttl set my-release 2h --service-account my-sa

# Set TTL in a different namespace for the CronJob
helm ttl set my-release 7d --create-service-account --cronjob-namespace ops

# Set TTL and delete the release namespace on expiry
helm ttl set my-release 30d --create-service-account --cronjob-namespace ops --delete-namespace

# Get the current TTL for a release
helm ttl get my-release

# Get TTL in JSON format
helm ttl get my-release -o json

# Get TTL in YAML format
helm ttl get my-release -o yaml

# Remove TTL from a release
helm ttl unset my-release

# Immediately execute TTL for a release
helm ttl run my-release

# Execute TTL for a release with CronJob in a different namespace
helm ttl run my-release --cronjob-namespace ops

# Clean up orphaned RBAC resources (dry run)
helm ttl cleanup-rbac --dry-run

# Clean up orphaned RBAC resources across all namespaces
helm ttl cleanup-rbac --all-namespaces
```

## Commands

### `helm ttl set RELEASE DURATION [flags]`

Set a TTL for a Helm release. Creates a CronJob that will uninstall the release when the TTL expires.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--service-account` | `default` | Service account for the CronJob |
| `--create-service-account` | `false` | Create the service account and RBAC resources |
| `--helm-image` | vendored | Helm container image |
| `--kubectl-image` | vendored | kubectl container image |
| `--cronjob-namespace` | release namespace | Namespace for the CronJob |
| `--delete-namespace` | `false` | Also delete the release namespace after uninstalling |

### `helm ttl get RELEASE [flags]`

Get the current TTL for a release.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-o, --output` | `text` | Output format: text, yaml, json |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

### `helm ttl unset RELEASE [flags]`

Remove TTL from a release by deleting the CronJob and cleaning up RBAC resources.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

### `helm ttl run RELEASE [flags]`

Immediately execute the TTL action for a release. This performs the same operations that the CronJob would: uninstall the release, optionally delete the namespace, delete the CronJob, and clean up RBAC resources.

A TTL must already be set for the release (via `helm ttl set`).

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

### `helm ttl cleanup-rbac [flags]`

Delete orphaned ServiceAccount and RBAC resources whose CronJobs have already fired or been deleted.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--dry-run` | `false` | Print what would be deleted without deleting |
| `-A, --all-namespaces` | `false` | Search all namespaces for orphaned resources |

## Duration Formats

Durations are tried in this order:

1. **Go durations:** `30m`, `2h`, `2h30m`, `24h`, `168h`
2. **Days shorthand:** `7d`, `30d`
3. **Human-readable durations:** `6 hours`, `3 days`, `2 weeks`, `30 mins`
4. **Natural language:** `tomorrow`, `next monday`, `in 2 hours`

## Environment Variables

| Variable | Description |
| -------- | ----------- |
| `HELM_NAMESPACE` | Release namespace (set by Helm) |
| `HELM_KUBECONTEXT` | Kubernetes context to use |
| `HELM_DRIVER` | Helm storage driver (default: `secrets`) |
| `KUBECONFIG` | Path to kubeconfig file |

## RBAC

### Automatic RBAC Creation

Use `--create-service-account` to automatically create the minimum required RBAC resources. The plugin handles three scenarios:

**Same namespace** (CronJob and release in the same namespace):

- ServiceAccount, Role (secrets + cronjobs access), RoleBinding

**Cross-namespace** (CronJob in a different namespace):

- ServiceAccount in CronJob namespace
- Role + RoleBinding in release namespace (secrets access)
- Role + RoleBinding in CronJob namespace (cronjobs access for self-cleanup)

**Cross-namespace with `--delete-namespace`:**

- Everything from cross-namespace, plus:
- ClusterRole + ClusterRoleBinding (namespaces access)

### RBAC Cleanup

RBAC resources are **not** automatically deleted when the CronJob fires. They remain as inert orphans. To clean them up:

- **Before TTL fires:** `helm ttl unset RELEASE` (cleans up everything)
- **After TTL fires:** `helm ttl cleanup-rbac` (finds and deletes orphaned RBAC)

### Manual RBAC Setup

If you prefer to manage RBAC yourself, create a ServiceAccount with the following permissions and pass it via `--service-account`:

```yaml
# Minimum required for same-namespace TTL
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "delete"]
  - apiGroups: ["batch"]
    resources: ["cronjobs"]
    verbs: ["get", "delete"]
```

## Limitations

- **Maximum TTL:** ~11 months (cron has no year field)
- **RBAC cleanup:** CronJobs do not clean up their own RBAC resources after firing
- **`--delete-namespace`** is only allowed when the CronJob namespace differs from the release namespace
- **Resource name length:** Combined `<release>-<namespace>-ttl` must be <= 52 characters

## Building from Source

```bash
# Build
make build

# Run tests
make test

# Run tests with coverage check (95% minimum)
make cover

# Lint
make lint

# Install into Helm plugins directory
make install
```

## License

MIT
