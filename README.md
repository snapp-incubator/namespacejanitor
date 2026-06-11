# NamespaceJanitor

A Kubernetes operator that enforces namespace ownership policies and automates namespace lifecycle management.

Namespaces created with `snappcloud.io/team: unknown` are considered unmanaged. NamespaceJanitor continuously monitors these namespaces and applies lifecycle flags, notifications, and cleanup according to configurable thresholds.

---

## Lifecycle

```
Day 0     Namespace created with team=unknown      🆕  Creation notification
Day 3     Yellow flag applied                       ⚠️  Notification sent
Day 14    Red flag applied                          🔴  Notification sent
Day 30    Final warning sent                        🚨  "Deletion in 24 hours"
Day 34    Namespace deleted                         💀  Final notification
```

When a namespace is claimed (`team` changes from `unknown`), all flags are removed and lifecycle management stops.

---

## Architecture

```
cmd/main.go                         Entrypoint — loads config, wires notifiers, starts manager
internal/controller/                Reconciliation logic (state machine)
internal/notification/              Kafka + Mattermost notification delivery
api/v1alpha1/                       CRD type definitions (NamespaceJanitor)
config/                             Kustomize manifests, CRD, RBAC, ConfigMap
```

### Controller Responsibilities

- Watches `Namespace` and `NamespaceJanitor` resources
- Auto-creates `NamespaceJanitor` CRs for relevant namespaces
- Applies lifecycle flags: `""` → `yellow` → `red` → `final-warning` → delete
- Sends notifications at each state transition (idempotent — once per transition)
- Cleans up flags when a team claims ownership

---

## Configuration

All operator settings live in a single YAML config file.

### Config File Structure

```yaml
lifecycle:
  creationNotification: true
  yellowThreshold: "72h"
  redThreshold: "336h"
  finalWarningThreshold: "720h"
  deleteThreshold: "816h"
notifications:
  mattermost:
    webhook: "https://YOUR_DOMAIN/hooks/YOUR_HOOK_ID"
  kafka:
    broker: "kafka.kafka.svc.cluster.local:9092"
    topic: "namespace-janitor-notifications"
    group: "namespace-janitor-group"
```

### Config Files

| File | Purpose | Thresholds |
|------|---------|------------|
| `config/manager/configmap.yaml` | In-cluster production config | 72h / 336h / 720h / 816h |
| `config/test/config.yaml` | Unit tests (envtest) | 2s / 4s / 6s / 8s |
| `config/local.yaml` | Local dev with `make run` | 2m / 5m / 7m / 9m |

### How the Operator Loads Config

```
--config flag  →  file path  →  parse YAML  →  validate thresholds  →  use defaults if missing
```

- Default path: `/etc/namespacejanitor/config.yaml` (mounted from ConfigMap)
- If file not found: uses built-in production defaults
- Validation: `yellow < red < finalWarning < delete`

### Changing Config in Production

```bash
# Edit the ConfigMap
oc edit configmap namespacejanitor-config -n namespacejanitor-operator-system

# Restart the pod
oc rollout restart deployment namespacejanitor-controller-manager \
  -n namespacejanitor-operator-system
```

---

## CRD — NamespaceJanitor

```yaml
apiVersion: namespacejanitor.snappcloud.io/v1alpha1
kind: NamespaceJanitor
metadata:
  name: <namespace-name>-janitor
  namespace: <namespace-name>
spec:
  additionalRecipients:
    - "team-lead@example.com"
```

- **Auto-created** by the controller for each relevant namespace
- Teams can patch their CR to add `additionalRecipients`
- **Namespaced** scope — one CR per namespace

---

## Notifications

Two independent channels, both optional:

| Channel | Config Key | Use case |
|---------|-----------|----------|
| **Mattermost** | `notifications.mattermost.webhook` | Team chat alerts |
| **Kafka** | `notifications.kafka.broker` | Event bus / downstream systems |

When both are configured, notifications are sent to **both** independently. If one fails, the other still sends. If neither is configured, the operator runs silently.

### Notification Events

| Action | Emoji | Severity | Mattermost Color |
|--------|-------|----------|-----------------|
| `NamespaceCreated` | 🆕 | LOW | — |
| `AppliedyellowFlag` | ⚠️ | MEDIUM | 🟡 Yellow |
| `AppliedredFlag` | 🔴 | HIGH | 🟠 Orange |
| `FinalWarning` | 🚨 | HIGH | 🔴 Red |
| `DeletingNamespace` | 💀 | CRITICAL | 🔴 Red |
| `NamespaceClaimed` | ✅ | LOW | 🟢 Green |

---

## Deployment

### Production (ArgoCD Apps-of-Apps)

NamespaceJanitor is deployed via `newcluster-bootstrap` as a `kustomize_app`:

```yaml
# cloud/helm/newcluster-bootstrap/values.yaml
kustomize_apps:
  - name: namespacejanitor
    repo: https://github.com/snapp-incubator/namespacejanitor
```

This runs `kustomize build config/default` which deploys:
- CRD, RBAC, ServiceAccount
- ConfigMap with operator configuration
- Deployment (3 replicas, leader election)
- Metrics Service

Per-cluster config overrides are done by editing the ConfigMap or using kustomize overlays.

### Local Kind Cluster

See [LOCAL_DEV.md](LOCAL_DEV.md) for full instructions.

Quick start:

```bash
# Build and load image
make docker-build IMG=namespacejanitor:dev
kind load docker-image namespacejanitor:dev --name kind

# Edit image ref in config/manager/kustomization.yaml
# Deploy
make deploy
```

### Run Locally (without Docker)

```bash
make install          # Install CRDs
make deploy           # Deploy RBAC + ConfigMap
oc scale deployment namespacejanitor-controller-manager \
  -n namespacejanitor-operator-system --replicas=0
go run ./cmd/main.go --config=config/local.yaml
```

---

## Make Commands

### Development

| Command | Description |
|---------|-------------|
| `make generate` | Regenerate `DeepCopy` methods from `+kubebuilder:object:root=true` markers |
| `make manifests` | Regenerate CRD YAML and RBAC from Go markers |
| `make fmt` | Run `go fmt` on all packages |
| `make vet` | Run `go vet` on all packages |
| `make lint` | Run `golangci-lint` |
| `make lint-fix` | Run `golangci-lint` with auto-fix |

### Build

| Command | Description |
|---------|-------------|
| `make build` | Build the operator binary to `bin/manager` |
| `make docker-build IMG=<tag>` | Build the Docker image |
| `make docker-push IMG=<tag>` | Push the Docker image |
| `make run` | Run the operator locally (uses kubeconfig) |

### Deploy

| Command | Description |
|---------|-------------|
| `make install` | Install CRDs into the cluster |
| `make uninstall` | Remove CRDs from the cluster |
| `make deploy` | Deploy operator (CRDs + RBAC + ConfigMap + Deployment) |
| `make undeploy` | Remove all operator resources |

### Test

| Command | Description |
|---------|-------------|
| `make test` | Run unit tests with envtest (auto-runs `manifests`, `generate`, `fmt`, `vet` first) |
| `make test-e2e` | Run E2E tests against a Kind cluster |

### When to Use `make generate`

Run after changing structs in `api/v1alpha1/`:

```go
// +kubebuilder:object:root=true
type NamespaceJanitor struct { ... }
```

This regenerates `api/v1alpha1/zz_generated.deepcopy.go`.

```bash
make generate
```

### When to Use `make manifests`

Run after changing kubebuilder markers:

```go
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:subresource:status
```

This regenerates:
- `config/crd/bases/*.yaml` (CRD schema)
- `config/rbac/role.yaml` (ClusterRole)

```bash
make manifests
```

### `make test` Already Calls Both

```makefile
test: manifests generate fmt vet envtest
```

So `make test` always ensures generated code and manifests are up to date.

---

## Repository Structure

```
├── api/v1alpha1/                   CRD type definitions
│   ├── namespacejanitor_types.go   Spec + Status structs
│   └── zz_generated.deepcopy.go    Auto-generated DeepCopy methods
├── cmd/
│   └── main.go                     Entrypoint — config loading, notifier wiring
├── internal/
│   ├── controller/
│   │   ├── config.go               OperatorConfig, LoadConfig(), Duration type
│   │   ├── stages.go               LifecycleStage enum + metadata
│   │   ├── namespacejanitor_controller.go   Reconcile loop + state machine
│   │   └── *_test.go               Unit tests (envtest)
│   └── notification/
│       ├── mattermost.go           Mattermost webhook notifier
│       ├── kafka.go                Kafka notifier (via eventbus)
│       ├── multi.go                Fan-out to multiple channels
│       └── format.go               Humanized action labels, age formatting
├── config/
│   ├── crd/bases/                  Generated CRD YAML
│   ├── rbac/                       Generated RBAC YAML
│   ├── manager/
│   │   ├── manager.yaml            Deployment manifest
│   │   ├── configmap.yaml          Operator ConfigMap (production defaults)
│   │   └── kustomization.yaml      Image override
│   ├── default/                    Main kustomize overlay
│   ├── test/config.yaml            Test thresholds (2s/4s/6s/8s)
│   └── local.yaml                  Local dev thresholds (2m/5m/7m/9m)
├── Dockerfile                      Multi-stage build (golang → alpine)
├── Makefile                        Build, test, deploy targets
├── LOCAL_DEV.md                    Local development guide
└── DEPLOYMENT_PLAN.md              Production deployment via ArgoCD
```
