# VPC Controller — Implementation Plan

> **Status: Implemented.** This was the pre-implementation plan. The actual
> controller is at `internal/controller/ec2/vpc_controller.go`. For the current
> reconcile logic, see `docs/controllers/vpc-controller.md`.

---

## Order of implementation

### 1. STRUCT + SETUP
- VPCReconciler struct (client.Client, Scheme)
- RBAC markers (VPC, VPC/status, VPC/finalizers, AWSCreds, Secrets)
- SetupWithManager — For VPC, Owns nothing (AWS resource, not K8s child)

### 2. FETCH VPC OBJECT
- r.Get → NotFound → nil (deleted, nothing to do)
- real error → return err (backoff)

### 3. DELETION PATH
Must come before any create/observe logic.
- check DeletionTimestamp is set
- check finalizer is present
- call ec2.DeleteVpc(status.VpcId)
- remove finalizer → r.Update

### 4. ADD FINALIZER
First thing on a brand new object.
- check if finalizer is absent
- patch it in → return ctrl.Result{}

### 5. FETCH CREDENTIALS
- r.Get AWSCreds from spec.awsCredsRef
- check AWSCreds.Status.Phase == "Ready"
- r.Get the Secret from AWSCreds.Spec.Credentials.SecretRef
- build ec2.Client (aws.Config with region + static credentials)

### 6. OBSERVE — drift detection
Only when status.VpcId is already set.
- ec2.DescribeVpcs(vpcId)
- NotFound → VPC deleted externally (drift!) → clear vpcId, phase=Pending
- state=available → phase=Available, Ready=True
- state=pending   → phase=Creating, Ready=False
- return RequeueAfter: 30s  ← keep loop alive

### 7. CREATE
Only when status.VpcId is empty.
- ec2.CreateVpc(CidrBlock: spec.CIDRBlock)
- store vpcId in status → phase=Creating
- return RequeueAfter: 10s  ← poll sooner while provisioning

### 8. STATUS WRITE
- r.Status().Update after every state change

---

## Key ordering rules

| Rule | Why |
|------|-----|
| Step 3 (deletion) before step 7 (create) | A deleted object must never trigger a Create |
| Step 6 (observe) before step 7 (create) | If vpcId is already set, skip Create — never duplicate |
| RequeueAfter for expected states | Creds not ready, VPC provisioning — these are normal, not errors |
| err for unexpected failures | AWS API timeout, etcd write failure — these need backoff retry |

---

## Return value cheatsheet

```
ctrl.Result{}                    → sleep until next watch event  (idle)
ctrl.Result{RequeueAfter: 10s}   → poll soon                     (just created, creds not ready)
ctrl.Result{RequeueAfter: 30s}   → poll for drift                (healthy, keep loop alive)
ctrl.Result{}, err               → something broke, backoff      (AWS API failure)
```
