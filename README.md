# namespacejanitor
## --- Not Completed ---
A Kubernetes operator designed to monitor and manage namespace lifecycle based on customizable policies. It ensures proper ownership Team assignment, enforces user quotas, and automates namespace cleanup.

## Project Goal

The Namespace Janitor operator monitors newly created namespaces with `snappcloud.io/team: unknown` label that assign with cluster-configs and enforces a lifecycle policy:
- Applies flags and notifications based on the namespace's age and label status.
- Automatically cleans up unused namespaces after a configurable period.

## Features

### Namespace Lifecycle Management
- **7 days after creation**: If still labeled `unknown`, apply `snappcloud.io/flag: yellow` and send notification.
- **14 days after creation**: If still `unknown` and `yellow`, update to `snappcloud.io/flag: red` and send updated notification.
- **16 days after creation**: If still `unknown` and `red`, delete the namespace and send final notification.

### User Quota Management
- Limits users to creating a maximum of 3 namespaces with the `unknown` label simultaneously.
- The quota check occurs during namespace creation.


### Core Components

#### 1. Namespace Controller
- Watches namespace events.
- Watches namespace CR
- Manages lifecycle timers using `creationTimestamp` and `RequeueAfter`.
- Updates namespace labels/annotations for state persistence.
- Interacts with a message broker for notifications.

#### 2. Validating Admission Webhook
- Intercepts namespace `CREATE` requests.
- Enforces the 3-namespace quota per user by querying existing `unknown` namespaces.
- Admits or denies requests based on quota availability.

#### 3. Namespace Policy CRD
- Custom Resource Definition (`CRD`) for declarative policy configuration.
- Fields include:
  - `additionalRecipients` for notification targets.
  - `deletationTimeExtend` for custom deletion timing.


### Namespace Policy CRD
```yaml  
apiVersion: snappcloud.snappcloud.io/v1alpha1
kind: NamespaceJanitor
metadata:
  labels:
    app.kubernetes.io/name: namespacejanitor
    app.kubernetes.io/managed-by: kustomize
  name: namespacejanitor-sample
spec:
  additionalRecipients:
    - "test@example.com"
    - "test2@example.com"
  deletionTimeExtend: 30 # days
