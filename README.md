# helm-ttl

A Helm plugin that manages TTL (time-to-live) for Helm releases. When a TTL is set, the plugin creates a Kubernetes CronJob that automatically uninstalls the release at the scheduled time and cleans up after itself. This prevents abandoned releases from consuming cluster resources.

## Installation

```bash
helm plugin install https://github.com/josegonzalez/helm-ttl
```

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

## Usage

```text
helm ttl COMMAND [ARGS...] [FLAGS...]
```

| Command | Description |
| ------- | ----------- |
| `set`   | Set a TTL on a Helm release |
| `get`   | Get the current TTL for a release |
| `unset` | Remove TTL from a release |
| `run`   | Immediately execute the TTL action |
| `cleanup-rbac` | Delete orphaned RBAC resources |

### Global Flags

These flags are available on all subcommands:

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-n, --namespace` | `HELM_NAMESPACE` or `default` | Override the release namespace |
| `--kube-context` | `HELM_KUBECONTEXT` | Override the Kubernetes context |
| `--kubeconfig` | `KUBECONFIG` | Path to kubeconfig file |
| `--driver` | `HELM_DRIVER` or `secrets` | Helm storage driver |

Flag values take priority over environment variables.

### Environment Variables

| Variable | Flag Override | Description |
| -------- | ------------ | ----------- |
| `HELM_NAMESPACE` | `-n, --namespace` | Release namespace (set by Helm) |
| `HELM_KUBECONTEXT` | `--kube-context` | Kubernetes context to use |
| `HELM_DRIVER` | `--driver` | Helm storage driver (default: `secrets`) |
| `KUBECONFIG` | `--kubeconfig` | Path to kubeconfig file |

## Commands

### `helm ttl set RELEASE DURATION [flags]`

Set a TTL for a Helm release. Creates a CronJob that will uninstall the release when the TTL expires.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--service-account` | `default` | Service account for the CronJob |
| `--create-service-account` | `false` | Create the service account (in the CronJob namespace) and RBAC resources |
| `--helm-image` | vendored | Helm container image |
| `--kubectl-image` | vendored | kubectl container image |
| `--cronjob-namespace` | release namespace | Namespace for the CronJob |
| `--delete-namespace` | `false` | Also delete the release namespace after uninstalling |

**Examples:**

```bash
# Set a TTL on a release (auto-creates service account and RBAC)
helm ttl set my-release 24h --create-service-account

# Set TTL using days shorthand
helm ttl set my-release 7d --create-service-account

# Set TTL using natural language
helm ttl set my-release "next monday" --create-service-account

# Set TTL with custom service account
helm ttl set my-release 2h --service-account my-sa

# Set TTL targeting a specific release namespace
helm ttl set my-release 24h --create-service-account -n staging

# Set TTL in a different namespace for the CronJob
helm ttl set my-release 7d --create-service-account --cronjob-namespace ops

# Set TTL and delete the release namespace on expiry
helm ttl set my-release 30d --create-service-account --cronjob-namespace ops --delete-namespace
```

### `helm ttl get RELEASE [flags]`

Get the current TTL for a release.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-o, --output` | `text` | Output format: text, yaml, json |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

**Examples:**

```bash
# Get the current TTL for a release
helm ttl get my-release

# Get TTL for a release in a specific namespace
helm ttl get my-release -n staging

# Get TTL in JSON format
helm ttl get my-release -o json

# Get TTL in YAML format
helm ttl get my-release -o yaml

# Get TTL when the CronJob is in a different namespace than the release
helm ttl get my-release -n staging --cronjob-namespace ops
```

### `helm ttl unset RELEASE [flags]`

Remove TTL from a release by deleting the CronJob and cleaning up RBAC resources.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

**Examples:**

```bash
# Remove TTL from a release
helm ttl unset my-release

# Remove TTL when the CronJob is in a different namespace than the release
helm ttl unset my-release -n staging --cronjob-namespace ops
```

### `helm ttl run RELEASE [flags]`

Immediately execute the TTL action for a release. This performs the same operations that the CronJob would: uninstall the release, optionally delete the namespace, delete the CronJob, and clean up RBAC resources.

A TTL must already be set for the release (via `helm ttl set`).

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--cronjob-namespace` | release namespace | Namespace where the CronJob lives |

**Examples:**

```bash
# Immediately execute TTL for a release
helm ttl run my-release

# Execute TTL for a release with CronJob in a different namespace
helm ttl run my-release --cronjob-namespace ops

# Immediately execute TTL with cross-namespace setup
helm ttl run my-release -n staging --cronjob-namespace ops
```

### `helm ttl cleanup-rbac [flags]`

Delete orphaned ServiceAccount and RBAC resources whose CronJobs have already fired or been deleted.

**Flags:**

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--dry-run` | `false` | Print what would be deleted without deleting |
| `-A, --all-namespaces` | `false` | Search all namespaces for orphaned resources |

**Examples:**

```bash
# Clean up orphaned RBAC resources (dry run)
helm ttl cleanup-rbac --dry-run

# Clean up orphaned RBAC resources across all namespaces
helm ttl cleanup-rbac --all-namespaces
```

## Duration Formats

Durations are tried in this order:

1. **Go durations:** `30m`, `2h`, `2h30m`, `24h`, `168h`
2. **Days shorthand:** `7d`, `30d`
3. **Human-readable durations:** `6 hours`, `3 days`, `2 weeks`, `30 mins`
4. **Natural language:** `tomorrow`, `next monday`, `in 2 hours`

## RBAC

### Plugin Permissions

The `helm ttl` CLI itself needs Kubernetes API access to
manage CronJobs and RBAC resources. The permissions required
depend on which commands and flags you use.

**Minimal (read-only `get`, or `set` without
`--create-service-account`):**

```yaml
rules:
  - apiGroups: ["batch"]
    resources: ["cronjobs"]
    verbs: ["get", "create", "update", "delete"]
  - apiGroups: [""]
    resources: ["serviceaccounts"]
    verbs: ["get"]
```

**Recommended (all commands with
`--create-service-account`):**

```yaml
rules:
  - apiGroups: ["batch"]
    resources: ["cronjobs"]
    verbs: ["get", "create", "update", "delete"]
  - apiGroups: [""]
    resources: ["serviceaccounts"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "create", "update", "delete"]
```

**Full (adds `--delete-namespace` and
`cleanup-rbac --all-namespaces`):**

```yaml
# Role (namespaced permissions from above, plus)
rules:
  - apiGroups: ["batch"]
    resources: ["cronjobs"]
    verbs: ["get", "create", "update", "delete"]
  - apiGroups: [""]
    resources: ["serviceaccounts"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["roles", "rolebindings"]
    verbs: ["get", "list", "create", "update", "delete"]

# ClusterRole (cluster-scoped permissions)
rules:
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources: ["clusterroles", "clusterrolebindings"]
    verbs: ["get", "list", "create", "update", "delete"]
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["list", "delete"]
```

> These are permissions for the user or service account
> running the `helm ttl` CLI, not the CronJob pods it
> creates. See below for CronJob pod permissions.

### CronJob Pod Permissions

#### Automatic RBAC Creation

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

> The ServiceAccount is always created in the CronJob namespace, since that is where the CronJob pod runs.

#### RBAC Cleanup

RBAC resources are **not** automatically deleted when the CronJob fires. They remain as inert orphans. To clean them up:

- **Before TTL fires:** `helm ttl unset RELEASE` (cleans up everything)
- **After TTL fires:** `helm ttl cleanup-rbac` (finds and deletes orphaned RBAC)

#### Manual RBAC Setup

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

## Tutorial

### Setting a TTL on a release

After deploying a Helm release, set a TTL to have it automatically cleaned up:

```bash
helm ttl set my-release 7d --create-service-account
```

This creates a CronJob that will uninstall `my-release` in 7 days. The `--create-service-account` flag sets up the ServiceAccount and RBAC resources the CronJob needs.

### Checking a TTL

Verify the TTL is set and see when it will fire:

```bash
helm ttl get my-release
```

### Updating a TTL

Run `set` again to update the expiration time:

```bash
helm ttl set my-release 14d --create-service-account
```

### Removing a TTL

If you decide to keep a release, remove the TTL. This deletes the CronJob and cleans up RBAC resources:

```bash
helm ttl unset my-release
```

### Cross-namespace setup

When the CronJob should run in a different namespace than the release (e.g., a shared `ops` namespace):

```bash
helm ttl set my-release 7d --create-service-account \
  -n staging --cronjob-namespace ops
```

To also delete the release namespace when the TTL fires:

```bash
helm ttl set my-release 30d --create-service-account \
  -n staging --cronjob-namespace ops \
  --delete-namespace
```

### Cleaning up after TTL fires

After a CronJob fires, the RBAC resources it used remain as inert orphans. Clean them up with:

```bash
# Preview what would be deleted
helm ttl cleanup-rbac --dry-run

# Delete orphaned RBAC resources
helm ttl cleanup-rbac
```

## Limitations

- **Maximum TTL:** ~11 months (cron has no year field)
- **RBAC cleanup:** CronJobs do not clean up their own RBAC resources after firing
- **`--delete-namespace`** is only allowed when the CronJob namespace differs from the release namespace
- **Resource name length:** Combined `<release>-<namespace>-ttl` must be <= 52 characters

## License

MIT
