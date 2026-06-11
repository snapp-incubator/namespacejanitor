# NamespaceJanitor — Local Development Guide

Run, test, and debug the NamespaceJanitor operator on a local Kind cluster.

---

## Prerequisites

| Tool | Minimum Version | Check |
|------|----------------|-------|
| Docker Desktop | any | `docker info` |
| Kind | any | `kind version` |
| kubectl | 1.28+ | `kubectl version --client` |
| Go | 1.25+ | `go version` |
| kustomize | (auto-installed by Makefile) | — |

```bash
alias oc=kubectl
```

---

## 1. Create a Kind Cluster

```bash
kind create cluster --name kind
oc config use-context kind-kind
oc get nodes
```

---

## 2. Configuration System

All operator settings are in a single YAML file. No CLI flags for notifications or lifecycle — everything is in the config.

### Config Files

| File | Read by | Thresholds | Kafka | Mattermost |
|------|---------|------------|-------|------------|
| `config/manager/configmap.yaml` | In-cluster pod (mounted as ConfigMap) | 72h / 336h / 720h / 816h | configurable | configurable |
| `config/test/config.yaml` | Unit tests (`make test`) | 2s / 4s / 6s / 8s | off | off |
| `config/local.yaml` | `make run` / `go run` on your machine | 2m / 5m / 7m / 9m | off | your webhook |

### Config Structure

```yaml
lifecycle:
  creationNotification: true     # send notification on first sight
  yellowThreshold: "72h"         # Go duration: "72h", "1440m", "2s"
  redThreshold: "336h"
  finalWarningThreshold: "720h"
  deleteThreshold: "816h"
notifications:
  mattermost:
    webhook: "https://..."
  kafka:
    broker: "kafka.kafka.svc.cluster.local:9092"
    topic: "namespace-janitor-notifications"
    group: "namespace-janitor-group"
```

### How It Loads

```
--config=/path/to/file.yaml  →  read file  →  parse YAML  →  validate  →  use defaults if missing
```

- Default path: `/etc/namespacejanitor/config.yaml`
- If file not found: production defaults (72h/336h/720h/816h)
- Validation: `yellow < red < finalWarning < delete`

### Changing Config

**In-cluster:** Edit the ConfigMap, restart the pod:
```bash
oc edit configmap namespacejanitor-config -n namespacejanitor-operator-system
oc rollout restart deployment namespacejanitor-controller-manager -n namespacejanitor-operator-system
```

**Local:** Edit `config/local.yaml`, restart the process.

---

## 3. Deployment Methods

### Method A: Run Locally (`make run`)

Best for fast iteration. The binary runs on your host, connects to Kind via kubeconfig.

```bash
# 1. Install CRDs
make install

# 2. Deploy RBAC + ConfigMap + Deployment
make deploy

# 3. Scale down the in-cluster pod (we run locally)
oc scale deployment namespacejanitor-controller-manager \
  -n namespacejanitor-operator-system --replicas=0

# 4. Run with local config
go run ./cmd/main.go --config=config/local.yaml
```

Expected:
```
INFO  Operator configuration loaded  {"yellowThreshold": "2m0s", "redThreshold": "5m0s", ...}
INFO  starting manager
INFO  Starting Controller  {"controller": "namespacejanitor", ...}
```

### Method B: Docker Image in Kind

Best for testing the real container.

```bash
# 1. Build
make docker-build IMG=namespacejanitor:dev

# 2. Load into Kind
kind load docker-image namespacejanitor:dev --name kind

# 3. Update image reference
# Edit config/manager/kustomization.yaml:
#   newName: namespacejanitor
#   newTag: dev

# 4. Update ConfigMap for short thresholds
# Edit config/manager/configmap.yaml:
#   yellowThreshold: "2m"
#   redThreshold: "5m"
#   ...

# 5. Deploy
make deploy

# 6. Verify
oc get pods -n namespacejanitor-operator-system
oc logs -f deployment/namespacejanitor-controller-manager -n namespacejanitor-operator-system
```

---

## 4. Test the Lifecycle

### Create a Test Namespace

```bash
oc create ns test-janitor
oc label ns test-janitor snappcloud.io/team=unknown
```

### Watch the Lifecycle

```bash
oc get ns test-janitor --show-labels
```

With `config/local.yaml` thresholds (2m/5m/7m/9m):

| Time | Event | Label |
|------|-------|-------|
| T+0 | 🆕 Creation notification | `creation-notified=true` |
| T+2m | ⚠️ Yellow flag | `flag=yellow` |
| T+5m | 🔴 Red flag | `flag=red` |
| T+7m | 🚨 Final warning | `final-warning=sent` |
| T+9m | 💀 Namespace deleted | — |

### Verify CR Auto-Creation

```bash
oc get namespacejanitor -n test-janitor
# NAME                    AGE
# test-janitor-janitor    ...

oc get namespacejanitor test-janitor-janitor -n test-janitor -o yaml
```

### Test Team Claim

```bash
oc create ns test-claim
oc label ns test-claim snappcloud.io/team=unknown
sleep 130  # wait for yellow flag
oc label ns test-claim snappcloud.io/team=payments-team --overwrite
oc get ns test-claim --show-labels
# flag label should be removed
```

---

## 5. Make Commands Reference

### Code Generation

```bash
make generate     # Regenerate DeepCopy methods (after changing api/v1alpha1/ structs)
make manifests    # Regenerate CRD YAML + RBAC ClusterRole (after changing kubebuilder markers)
```

**When to run `make generate`:**
After adding/removing/changing fields in `NamespaceJanitorSpec` or `NamespaceJanitorStatus`:
```go
type NamespaceJanitorSpec struct {
    AdditionalRecipients []string `json:"additionalRecipients,omitempty"`
    // Add new field here → run make generate
}
```
Regenerates: `api/v1alpha1/zz_generated.deepcopy.go`

**When to run `make manifests`:**
After changing kubebuilder markers on types or controller:
```go
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:subresource:status
```
Regenerates: `config/crd/bases/*.yaml`, `config/rbac/role.yaml`

**`make test` calls both automatically:**
```makefile
test: manifests generate fmt vet envtest
```

### Build & Run

```bash
make build                     # Build binary to bin/manager
make run                       # Run locally (calls generate, fmt, vet first)
make docker-build IMG=<tag>    # Build Docker image
make docker-push IMG=<tag>     # Push Docker image
```

### Deploy & Remove

```bash
make install     # Install CRDs only
make deploy      # Install CRDs + RBAC + ConfigMap + Deployment
make undeploy    # Remove all operator resources
make uninstall   # Remove CRDs only
```

### Testing

```bash
make test        # Unit tests with envtest (no cluster needed)
make test-e2e    # E2E tests against Kind cluster
```

### Code Quality

```bash
make fmt         # go fmt ./...
make vet         # go vet ./...
make lint        # golangci-lint run
make lint-fix    # golangci-lint run --fix
```

### Full Workflow

```
Edit types.go (add field)
    │
    ▼
make generate    ← regenerate DeepCopy
    │
    ▼
make manifests   ← regenerate CRD YAML
    │
    ▼
make install     ← apply new CRD to cluster
    │
    ▼
make run         ← test locally
```

Or simply:
```bash
make test   # does generate + manifests + fmt + vet + run tests
```

---

## 6. Notifications

### Mattermost

Set in the config file:
```yaml
notifications:
  mattermost:
    webhook: "https://YOUR_DOMAIN/hooks/YOUR_HOOK_ID"
```

Test the webhook:
```bash
curl -X POST \
  -H "Content-Type: application/json" \
  -d '{"text":"Test from NamespaceJanitor","username":"NamespaceJanitor"}' \
  "https://YOUR_DOMAIN/hooks/YOUR_HOOK_ID"
```

### Kafka

```yaml
notifications:
  kafka:
    broker: "kafka.kafka.svc.cluster.local:9092"
    topic: "namespace-janitor-notifications"
    group: "namespace-janitor-group"
```

Read Kafka messages:
```bash
oc exec -n kafka deploy/kafka -- kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic namespace-janitor-notifications \
  --from-beginning --timeout-ms 5000
```

### Both Channels

When both are configured, notifications are sent to both independently. If one fails, the other still delivers.

### No Notifications

When both are empty, the operator runs silently:
```
INFO  No notification channels configured. Running without notifications.
```

---

## 7. Debugging

```bash
# Operator logs
oc logs -f deployment/namespacejanitor-controller-manager -n namespacejanitor-operator-system

# Check events
oc get events -A --sort-by=.lastTimestamp | grep -i janitor

# Inspect namespace
oc describe ns <name>

# Inspect CR
oc get namespacejanitor <name>-janitor -n <name> -o yaml

# Check ConfigMap
oc get configmap namespacejanitor-config -n namespacejanitor-operator-system -o yaml

# Check CRDs
oc get crd | grep namespacejanitor

# Check RBAC
oc get clusterrole | grep namespacejanitor
oc get serviceaccount -n namespacejanitor-operator-system
```

---

## 8. Cleanup

```bash
oc delete ns test-janitor test-claim --ignore-not-found
make undeploy
make uninstall
kind delete cluster --name kind
```
