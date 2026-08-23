# infra-operator

A Kubernetes operator that provisions and manages AWS infrastructure resources
from Kubernetes custom resources. Create a `VPC` CR — the operator creates a
real AWS VPC. Delete it — the operator deletes the VPC from AWS before removing
the Kubernetes object.

Follows the Crossplane ProviderConfig pattern: credentials are managed
separately via an `AWSCreds` CR, decoupling credential management from resource
definitions.

---

## Prerequisites

- Go v1.24+
- Docker v20+
- kubectl v1.28+
- A Kubernetes cluster (kind for local dev, or any real cluster)
- AWS credentials with `ec2:CreateVpc`, `ec2:DescribeVpcs`, `ec2:DeleteVpc` permissions

---

## Quick Start

### 1. Install CRDs

```bash
make install
```

### 2. Run the controller locally

```bash
make run
```

### 3. Create credentials

```bash
kubectl create secret generic aws-creds \
  --from-literal=AWS_ACCESS_KEY_ID=<your-key-id> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<your-secret-key> \
  -n default

kubectl apply -f config/samples/core_v1alpha1_awscreds.yaml
```

### 4. Create a VPC

```bash
kubectl apply -f config/samples/ec2_v1alpha1_vpc.yaml
kubectl get vpc -n default -w
```

### 5. Delete a VPC

```bash
kubectl delete vpc vpc-sample -n default
```

The controller calls `ec2.DeleteVpc` before removing the Kubernetes object.

---

## Deploy as pods in cluster

```bash
make docker-build IMG=infra-operator:latest
kind load docker-image infra-operator:latest --name <cluster-name>
make deploy IMG=infra-operator:latest
```

See [Operations Guide](docs/operations/deploy.md) for the full deploy walkthrough including the `imagePullPolicy` patch required for kind.

---

## Unit Tests

No cluster or AWS credentials needed — tests use an in-process API server and a fake EC2 stub.

```bash
make setup-envtest   # first time only
make test
```

Coverage: ~64% AWSCreds controller, ~71% VPC controller.

---

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture Overview](docs/architecture/overview.md) | Components, credential flow, reconcile lifecycle |
| [AWSCreds API Reference](docs/api/awscreds.md) | CRD fields, status, conditions |
| [VPC API Reference](docs/api/vpc.md) | CRD fields, status, conditions |
| [AWSCreds Controller](docs/controllers/awscreds-controller.md) | Reconcile logic, Secret validation |
| [VPC Controller](docs/controllers/vpc-controller.md) | Reconcile logic, finalizer, drift detection, EC2API interface |
| [Operations Guide](docs/operations/deploy.md) | Build, deploy, debug |
| [Testing Guide](docs/operations/testing.md) | Unit test suite, fakeEC2, coverage |
| [Design ADR-001](docs/specs/ADR-001-infra-operator-design.md) | Original design decision record |
