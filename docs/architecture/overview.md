# Architecture Overview

## What infra-operator does

`infra-operator` watches Kubernetes custom resources and reconciles them against
real AWS infrastructure. When you create a `VPC` CR, the operator creates a real
AWS VPC. When you delete it, the operator deletes the VPC from AWS before
removing the Kubernetes object.

---

## Components

```
infra-operator
  ├── AWSCreds controller     — validates AWS credential Secrets
  └── VPC controller          — creates/observes/deletes real AWS VPCs
```

Both controllers run in a single binary, sharing one Manager, one cache, and
one watch stream to the API server.

---

## Credential Flow

The operator never holds AWS credentials in code or environment variables when
deployed. Credentials flow through three layers:

```
Administrator
  └── kubectl create secret generic aws-creds \
        --from-literal=AWS_ACCESS_KEY_ID=... \
        --from-literal=AWS_SECRET_ACCESS_KEY=...

AWSCreds CR  ──points to──►  Secret
  └── AWSCreds controller validates Secret exists + keys are non-empty
  └── Sets status.phase = Ready | NotReady | NotFound

VPC CR  ──references──►  AWSCreds CR
  └── VPC controller checks AWSCreds.status.phase == Ready
  └── Reads Secret from AWSCreds.spec.credentials.secretRef
  └── Builds EC2 client with static credentials + spec.region
  └── Calls AWS EC2 API
```

This mirrors the Crossplane ProviderConfig pattern:
- `AWSCreds` = ProviderConfig
- `VPC` = managed resource with `providerConfigRef`

---

## Reconcile Lifecycle

### AWSCreds

```
Watch event fires (AWSCreds created/updated, or referenced Secret changed)
  │
  ▼
Fetch AWSCreds object
  │
  ├── NotFound → return nil (deleted, nothing to do)
  │
  ▼
Fetch referenced Secret
  │
  ├── NotFound   → phase=NotFound,  condition Ready=False
  ├── MissingKey → phase=NotReady,  condition Ready=False
  ├── EmptyValue → phase=NotReady,  condition Ready=False
  └── All good   → phase=Ready,     condition Ready=True
  │
  ▼
r.Status().Update → return ctrl.Result{}
```

### VPC

```
Watch event fires (VPC created/updated, or RequeueAfter timer fires)
  │
  ▼
Fetch VPC object
  │
  ├── NotFound → return nil
  │
  ▼
DeletionTimestamp set?
  ├── YES → fetch creds → ec2.DeleteVpc → remove finalizer → return
  │
  ▼
Finalizer present?
  ├── NO → add finalizer → r.Update → return  (re-reconcile fires immediately)
  │
  ▼
Fetch AWSCreds + Secret → build EC2 client
  │
  ├── AWSCreds NotReady → phase=Pending → RequeueAfter 10s
  │
  ▼
status.VpcID set?
  ├── YES → ec2.DescribeVpcs
  │          ├── NotFound (drift!) → clear VpcID → phase=Pending → RequeueAfter 10s
  │          ├── state=available   → phase=Available → RequeueAfter 30s
  │          └── state=pending     → phase=Creating  → RequeueAfter 30s
  │
  └── NO  → ec2.CreateVpc → store VpcID → phase=Creating → RequeueAfter 10s
```

---

## Key Design Decisions

| Decision | Reason |
|----------|--------|
| Separate AWSCreds CRD | Decouples credential management from resource management — one AWSCreds per AWS account, many VPCs referencing it |
| Finalizer on VPC | Prevents K8s object deletion before AWS VPC is actually deleted — avoids orphaned AWS resources |
| RequeueAfter on VPC | AWS VPCs are external — no watch stream fires when they change. Polling detects drift |
| Option B creds fetch | Deletion path fetches its own creds — decoupled from create/observe path, safer if creds are temporarily broken |
| Static credentials provider | Explicitly uses Secret-sourced creds, never falls back to ambient env/profile — predictable in production |
