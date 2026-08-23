# VPC Controller

## Purpose

Creates, observes, and deletes real AWS VPCs to match the desired state
declared in `VPC` CRs. Uses the `AWSCreds` CR as a credential gateway.
Implements finalizers for safe deletion and `RequeueAfter` for drift detection.

---

## Reconcile Logic (Option B — separate creds per path)

```
1. Fetch VPC object
   └── NotFound → return nil

2. DELETION PATH (DeletionTimestamp set)
   └── Finalizer absent → return nil (already cleaned up)
   └── fetchEC2Client (own creds fetch)
       └── creds error → phase=Deleting, reason=AWSCredsNotReady → RequeueAfter 10s
   └── status.VpcID set → ec2.DeleteVpc
       ├── isVPCNotFound(err) → VPC already gone, continue to remove finalizer
       └── other error → phase=Deleting, reason=DeleteFailed → return err (retry)
   └── Remove finalizer → r.Update → return nil

3. ADD FINALIZER
   └── Finalizer absent → append → r.Update → return ctrl.Result{}
       (r.Update triggers immediate re-reconcile via watch stream)

4. FETCH CREDENTIALS (for create/observe path)
   └── fetchEC2Client:
       a. r.Get AWSCreds from spec.awsCredsRef
       b. Check AWSCreds.status.phase == "Ready"
          └── Not ready → return err → backoff (caller sets phase=Pending)
       c. r.Get Secret from AWSCreds.spec.credentials.secretRef
       d. Build ec2.Client(region=spec.region, staticCreds)

5. OBSERVE (status.VpcID set)
   └── ec2.DescribeVpcs(vpcId)
       ├── isVPCNotFound(err) → DRIFT — VPC deleted in AWS console
       │     AWS returns a 400 InvalidVpcID.NotFound error (not an empty list).
       │     clear VpcID, phase=Pending → RequeueAfter 10s (re-create soon)
       ├── other error → phase=Failed, Ready=False, reason=DescribeFailed → return err
       ├── state=available → phase=Available, Ready=True → RequeueAfter 30s
       └── state=pending   → phase=Creating,  Ready=False → RequeueAfter 30s

6. CREATE (status.VpcID empty)
   └── ec2.CreateVpc(CidrBlock, Name tag)
       ├── error → phase=Failed, Ready=False → return err (backoff)
       └── success → store VpcID, phase=Creating → RequeueAfter 10s
```

---

## Finalizer

**Name**: `vpc.ec2.infra.example.com/finalizer`

Added on the first reconcile of a new object (step 3). Prevents Kubernetes
from garbage collecting the VPC CR until the AWS VPC is deleted.

```
kubectl delete vpc my-vpc
  → K8s sets DeletionTimestamp (does NOT delete yet)
  → controller reconciles
  → calls ec2.DeleteVpc
  → on success: removes finalizer
  → K8s now garbage collects the object
```

If `ec2.DeleteVpc` fails (AWS error), the finalizer stays. The object remains
in the cluster. The controller retries with exponential backoff. This is
intentional — it prevents silently orphaning AWS resources.

---

## Drift Detection

The controller never returns `ctrl.Result{}` when a VPC is healthy. It always
returns `RequeueAfter: 30s`, keeping the polling loop alive.

```
Every 30 seconds:
  ec2.DescribeVpcs(status.VpcID)
  → still there      → update phase, RequeueAfter 30s
  → 400 NotFound     → drift detected, clear VpcID, RequeueAfter 10s → next reconcile re-creates it
  → other AWS error  → phase=Failed, return err (backoff)
```

This catches manual deletions via AWS console, CLI, or other automation.

---

## Return values

| Situation | Return | Why |
|-----------|--------|-----|
| Object deleted | `nil` | Nothing to do |
| Finalizer added | `ctrl.Result{}` | Watch fires immediately on metadata update |
| Creds not ready (create/observe) | `RequeueAfter: 10s, nil` | Normal transient state — don't backoff, just retry soon |
| Creds not ready (deletion) | `RequeueAfter: 10s, nil` | Same — retry until creds become available to delete |
| Just created | `RequeueAfter: 10s, nil` | Poll sooner while VPC is provisioning |
| VPC available | `RequeueAfter: 30s, nil` | Drift detection loop |
| VPC not yet available | `RequeueAfter: 30s, nil` | Keep polling while AWS state is pending |
| AWS API failure (DescribeVpcs, DeleteVpc) | `ctrl.Result{}, err` | Backoff retry |

---

## Testability

The controller uses an `EC2API` interface instead of a direct `*awsec2.Client` so that tests can inject a fake without making real AWS calls.

```go
type EC2API interface {
    CreateVpc(...)  (*awsec2.CreateVpcOutput,  error)
    DescribeVpcs(...) (*awsec2.DescribeVpcsOutput, error)
    DeleteVpc(...)  (*awsec2.DeleteVpcOutput,  error)
}
```

`VPCReconciler` has an optional `NewEC2Client` factory field:

```go
type VPCReconciler struct {
    client.Client
    Scheme       *runtime.Scheme
    NewEC2Client func(ctx context.Context, secret *corev1.Secret, region string) (EC2API, error)
}
```

- **Production** (`NewEC2Client == nil`): `fetchEC2Client` falls through to `buildEC2Client`, which creates a real `*awsec2.Client` with static credentials.
- **Tests** (`NewEC2Client != nil`): the factory returns a `fakeEC2` stub that returns canned outputs for `CreateVpc`, `DescribeVpcs`, and `DeleteVpc`.

---

## Helper functions

| Function / type | Purpose |
|-----------------|---------|
| `EC2API` | Interface covering the three EC2 operations used by the controller — enables fake injection in tests |
| `buildEC2Client` | Builds a real `*awsec2.Client` from a Secret + region using the static credentials provider |
| `fetchEC2Client` | Fetches `AWSCreds` → checks Ready → fetches Secret → calls `NewEC2Client` (or `buildEC2Client`) |
| `containsString` | Checks if a finalizer string exists in the finalizers slice |
| `removeString` | Returns the finalizers slice with the given finalizer removed |
| `isVPCNotFound` | Unwraps a `smithy.APIError` and checks for `InvalidVpcID.NotFound` error code |

---

## RBAC

```
vpcs:              get, list, watch, create, update, patch, delete
vpcs/status:       get, update, patch
vpcs/finalizers:   update
secrets:           get, list, watch
awscreds:          get, list, watch
```

---

## File

`internal/controller/ec2/vpc_controller.go`
