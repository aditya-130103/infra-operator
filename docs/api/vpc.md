# VPC API Reference

**Group**: `ec2.infra.example.com`  
**Version**: `v1alpha1`  
**Kind**: `VPC`  
**Scope**: Namespaced

---

## Purpose

`VPC` represents a real AWS VPC. The operator creates, observes, and deletes
the VPC in AWS to match the desired state declared in the CR spec.

---

## Spec

```yaml
spec:
  name: my-infra-vpc          # Name tag visible in AWS console
  cidrBlock: 10.0.0.0/16      # VPC CIDR block
  region: ap-south-2          # AWS region
  awsCredsRef:
    name: prod-creds           # AWSCreds object to use for this VPC
    namespace: default
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.name` | string | yes | Value set as the `Name` tag on the AWS VPC — visible in console |
| `spec.cidrBlock` | string | yes | IPv4 CIDR block for the VPC (e.g. `10.0.0.0/16`) |
| `spec.region` | string (enum) | yes | AWS region to create the VPC in |
| `spec.awsCredsRef.name` | string | yes | Name of the `AWSCreds` object to use |
| `spec.awsCredsRef.namespace` | string | yes | Namespace of the `AWSCreds` object |

### Supported regions

All current AWS regions are supported, including:
`us-east-1`, `us-east-2`, `us-west-1`, `us-west-2`, `ca-central-1`,
`ca-west-1`, `eu-west-1`, `eu-west-2`, `eu-west-3`, `eu-central-1`,
`eu-central-2`, `eu-north-1`, `eu-south-1`, `eu-south-2`, `ap-south-1`,
`ap-south-2`, `ap-northeast-1`, `ap-northeast-2`, `ap-northeast-3`,
`ap-southeast-1` through `ap-southeast-5`, `ap-east-1`, `sa-east-1`,
`me-south-1`, `me-central-1`, `af-south-1`, `il-central-1`, `mx-central-1`

---

## Status

```yaml
status:
  vpcId: vpc-0abc1234def567890
  phase: Available
  conditions:
    - type: Ready
      status: "True"
      reason: VPCAvailable
      message: "VPC is available in AWS"
```

| Field | Description |
|-------|-------------|
| `status.vpcId` | AWS VPC ID assigned after creation. Empty until VPC is created. |
| `status.phase` | Current lifecycle phase |

### Phase values

| Phase | Meaning |
|-------|---------|
| `Pending` | VPC not yet created, or creds not ready, or drift detected (re-creating) |
| `Creating` | `CreateVpc` called, waiting for AWS to report `available` |
| `Available` | VPC exists in AWS and state is `available` |
| `Failed` | `CreateVpc` returned an error, or `DescribeVpcs` returned a non-transient error |
| `Deleting` | Deletion in progress (DeletionTimestamp set, finalizer being processed) |

### Condition reasons

| Reason | Status | Description |
|--------|--------|-------------|
| `VPCAvailable` | True | VPC is available in AWS |
| `VPCCreating` | False | VPC creation call made, provisioning in progress |
| `VPCNotReady` | False | VPC exists but state is not yet `available` |
| `VPCNotFound` | False | VPC was found in status but not in AWS (drift detected, will re-create) |
| `CreateFailed` | False | AWS `CreateVpc` API call returned an error |
| `AWSCredsNotReady` | False | Referenced `AWSCreds` not found or not in `Ready` phase |
| `DescribeFailed` | False | AWS `DescribeVpcs` returned a non-NotFound error |
| `DeleteFailed` | False | AWS `DeleteVpc` returned an error; finalizer remains until retry succeeds |

---

## Example

```yaml
apiVersion: ec2.infra.example.com/v1alpha1
kind: VPC
metadata:
  name: my-vpc
  namespace: default
spec:
  name: my-infra-vpc
  cidrBlock: 10.0.0.0/16
  region: ap-south-2
  awsCredsRef:
    name: prod-creds
    namespace: default
```

---

## Behavior notes

- **Finalizer** — `vpc.ec2.infra.example.com/finalizer` is added on first
  reconcile. The VPC CR cannot be deleted until the AWS VPC is successfully
  deleted. If the AWS API call fails, the finalizer stays and the object
  remains in the cluster.
- **Drift detection** — the controller polls AWS every 30 seconds via
  `DescribeVpcs`. If the VPC is not found (deleted manually in the console),
  the controller clears `status.vpcId` and re-creates it within 10 seconds.
- **No VPC found on create** — if `status.vpcId` is empty, the controller
  always attempts to create. There is no idempotency check against existing
  AWS VPCs with the same CIDR — running two VPC CRs with the same CIDR will
  create two separate VPCs in AWS.
