# ADR-001 — infra-operator Initial Design

**Date**: 2026-08-20  
**Status**: Implemented  
**Superseded by**: See `docs/architecture/overview.md` for current architecture, `docs/api/` for current CRD reference

---

## Overview

`infra-operator` is a Kubernetes operator that provisions and manages AWS
infrastructure resources. It mirrors the Crossplane ProviderConfig pattern:
a credential reference CRD (`AWSCreds`) acts as a validated gateway to an AWS
account, and resource CRDs (starting with `VPC`) reference it.

---

## Goals

- Create, observe, and delete real AWS VPCs from a Kubernetes CR
- Practice finalizers (safe deletion of external resources)
- Practice `RequeueAfter` (polling-based drift detection for external resources)
- Understand the Crossplane ProviderConfig pattern by building a simplified version

---

## API Groups

| Group | Purpose |
|-------|---------|
| `core.infra.example.com/v1alpha1` | Credential management (`AWSCreds`) |
| `ec2.infra.example.com/v1alpha1` | EC2 resources (`VPC`) |

Separate groups mean EC2 resources are cleanly namespaced under `ec2.*` as
the operator grows (Subnet, SecurityGroup, etc. would all land here).

---

## CRDs

### AWSCreds — `core.infra.example.com/v1alpha1`

Validated reference to a manually-created Kubernetes Secret holding AWS
credentials. Does not create any AWS resources.

```yaml
apiVersion: core.infra.example.com/v1alpha1
kind: AWSCreds
metadata:
  name: prod-creds
  namespace: infra-operator-system
spec:
  credentials:
    secretRef:
      name: aws-creds          # pre-existing Secret name
      namespace: infra-operator-system
status:
  phase: Ready | NotReady
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: SecretFound | SecretNotFound | SecretMissingField | SecretEmptyFields
      message: "..."
```

**The Secret must contain these keys:**
```
AWS_ACCESS_KEY_ID
AWS_SECRET_ACCESS_KEY
```

The Secret is created manually by the operator administrator:
```bash
kubectl create secret generic aws-creds \
  --from-literal=AWS_ACCESS_KEY_ID=AKIA... \
  --from-literal=AWS_SECRET_ACCESS_KEY=xxx \
  -n infra-operator-system
```

---

### VPC — `ec2.infra.example.com/v1alpha1`

Represents a real AWS VPC. References an `AWSCreds` object for credentials.

```yaml
apiVersion: ec2.infra.example.com/v1alpha1
kind: VPC
metadata:
  name: my-vpc
  namespace: default
spec:
  region: us-east-1
  cidrBlock: 10.0.0.0/16
  awsCredsRef:
    name: prod-creds
    namespace: infra-operator-system
status:
  vpcId: vpc-0abc1234          # set after creation, empty until then
  phase: Pending | Creating | Available | Failed | Deleting
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: VPCAvailable | VPCCreating | VPCNotFound | VPCNotReady | AWSCredsNotReady | CreateFailed | DescribeFailed | DeleteFailed
      message: "..."
```

---

## Controllers

### AWSCreds Controller

**Trigger**: watches `AWSCreds` objects. Also watches Secrets it does not own
(via `ctrl.Watches`) so changes to the referenced Secret requeue the AWSCreds.

**Reconcile logic**:
```
1. Fetch AWSCreds object — if NotFound, return nil (deleted, nothing to do)
2. r.Get the referenced Secret
   - NotFound → set phase=NotFound, Ready=False, reason=SecretNotFound, return ctrl.Result{}
3. Check Secret.Data has AWS_ACCESS_KEY_ID
   - Missing → set phase=NotReady, Ready=False, reason=SecretMissingField, return ctrl.Result{}
4. Check Secret.Data has AWS_SECRET_ACCESS_KEY
   - Missing → set phase=NotReady, Ready=False, reason=SecretMissingField, return ctrl.Result{}
5. Check both values are non-empty
   - Empty → set phase=NotReady, Ready=False, reason=SecretEmptyFields, return ctrl.Result{}
6. All checks pass → set phase=Ready, Ready=True, reason=SecretFound
   return ctrl.Result{}   ← no polling needed, watch stream handles updates
```

**No AWS API calls. No resource creation. Pure validation.**

Return value: `ctrl.Result{}` — sleep until next watch event. The watch on
the Secret ensures re-reconciliation if the Secret changes or is deleted.

---

### VPC Controller

**Trigger**: watches `VPC` objects. Owns no K8s children (Deployment, Service etc.) —
the only child is the real AWS VPC.

**Finalizer**: `vpc.ec2.infra.example.com/finalizer`

Added on first reconcile. Ensures the AWS VPC is deleted before the K8s object
is removed.

**Reconcile logic**:

```
1. Fetch VPC object — if NotFound, return nil

2. DELETION PATH — DeletionTimestamp is set:
   a. If finalizer not present → already cleaned up, return nil
   b. fetchEC2Client (own creds fetch — decoupled from create/observe path)
      - creds error → phase=Deleting, reason=AWSCredsNotReady, RequeueAfter 10s
   c. ec2.DeleteVpc(status.VpcId)
      - isVPCNotFound(err) → VPC already gone, continue to remove finalizer
      - other err → phase=Deleting, reason=DeleteFailed, return err (backoff retry)
   d. Remove finalizer → return ctrl.Result{}

3. ADD FINALIZER — if not present, patch it in, return ctrl.Result{}

4. FETCH CREDENTIALS:
   a. r.Get AWSCreds from spec.awsCredsRef
   b. Check AWSCreds.Status.Phase == "Ready"
      - Not ready → phase=Pending, reason=AWSCredsNotReady, RequeueAfter 10s
   c. r.Get the Secret from AWSCreds.Spec.Credentials.SecretRef
   d. Build EC2 client with creds + spec.Region via NewEC2Client factory

5. OBSERVE — if status.VpcId is set:
   a. ec2.DescribeVpcs(vpcId)
      - isVPCNotFound(err) → drift! clear vpcId, phase=Pending, RequeueAfter 10s
      - other err → phase=Failed, reason=DescribeFailed, return err (backoff)
   b. VPC found, state=available → phase=Available, Ready=True, RequeueAfter 30s
   c. VPC found, state=pending  → phase=Creating, Ready=False, RequeueAfter 30s

6. CREATE — status.VpcId is empty:
   a. ec2.CreateVpc(CidrBlock, Name tag)
      - err → phase=Failed, reason=CreateFailed, return err (backoff)
   b. Store vpcId in status, phase=Creating, RequeueAfter 10s
```

**Return value summary**:
```
ctrl.Result{}                    → sleep until watch event    (idle/done)
ctrl.Result{RequeueAfter: 10s}   → poll soon                  (creds not ready, just created)
ctrl.Result{RequeueAfter: 30s}   → poll for drift             (healthy, keep loop alive)
ctrl.Result{}, err               → something broke, backoff   (AWS API failure)
```

---

## Project Layout

```
infra-operator/
  api/
    v1alpha1/                    ← AWSCreds CRD types (core.infra.example.com)
      awscreds_types.go
      groupversion_info.go
      zz_generated.deepcopy.go
    ec2/v1alpha1/                ← VPC CRD types (ec2.infra.example.com)
      vpc_types.go
      groupversion_info.go
      zz_generated.deepcopy.go
  internal/controller/
    awscreds_controller.go
    ec2/
      vpc_controller.go
  cmd/main.go                    ← one manager, two controllers registered
  config/
    crd/                         ← generated CRD manifests
    rbac/                        ← generated RBAC manifests
    manager/                     ← controller Deployment manifest
    default/                     ← kustomize default overlay
    samples/
      core_v1alpha1_awscreds.yaml
      ec2_v1alpha1_vpc.yaml
  docs/
    architecture/overview.md
    api/awscreds.md
    api/vpc.md
    controllers/awscreds-controller.md
    controllers/vpc-controller.md
    operations/deploy.md
    operations/testing.md
    specs/ADR-001-infra-operator-design.md
```

---

## Credential Flow (end to end)

```
Administrator:
  kubectl create secret generic aws-creds \
    --from-literal=AWS_ACCESS_KEY_ID=AKIA... \
    --from-literal=AWS_SECRET_ACCESS_KEY=xxx \
    -n infra-operator-system

  kubectl apply -f awscreds.yaml   # AWSCreds points to aws-creds secret

AWSCreds controller:
  validates Secret exists + has correct keys → Ready=True

User:
  kubectl apply -f vpc.yaml        # VPC references prod-creds AWSCreds

VPC controller:
  1. reads AWSCreds → checks Ready=True
  2. reads Secret → gets raw creds
  3. builds ec2.Client(region=us-east-1, creds=...)
  4. ec2.CreateVpc("10.0.0.0/16") → vpc-0abc1234
  5. polls every 30s for drift
```
