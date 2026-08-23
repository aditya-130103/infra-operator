# Operations Guide

## Prerequisites

- `kubectl` configured against a cluster (kind or real)
- Docker running locally
- AWS credentials with EC2 permissions (`ec2:CreateVpc`, `ec2:DescribeVpcs`, `ec2:DeleteVpc`)
- `kind` CLI (for local clusters)

---

## 1. Build and Deploy

### Local development (make run)

Runs the controller on your machine against the cluster in `~/.kube/config`.
No image build needed.

```bash
# Install CRDs
make install

# Run controller locally
make run
```

### Deploy as pods in cluster

```bash
# Build image
make docker-build IMG=infra-operator:latest

# Load into kind (skip if using a real cluster with a registry)
kind load docker-image infra-operator:latest --name <your-cluster-name>

# Deploy via kustomize
make deploy IMG=infra-operator:latest
```

> **kind note**: After `make deploy`, patch the controller deployment to use
> `imagePullPolicy: Never` so it uses the locally-loaded image instead of
> trying to pull from Docker Hub:
> ```bash
> kubectl patch deployment infra-operator-controller-manager \
>   -n infra-operator-system \
>   --type=json \
>   -p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"}]'
> kubectl rollout status deployment/infra-operator-controller-manager \
>   -n infra-operator-system
> ```

---

## 2. Create AWS Credentials Secret

```bash
kubectl create secret generic aws-creds \
  --from-literal=AWS_ACCESS_KEY_ID=<your-key-id> \
  --from-literal=AWS_SECRET_ACCESS_KEY=<your-secret-key> \
  -n default
```

---

## 3. Create AWSCreds CR

```bash
kubectl apply -f config/samples/core_v1alpha1_awscreds.yaml
```

Verify it is Ready:
```bash
kubectl get awscreds -n default
# NAME              PHASE   AGE
# awscreds-sample   Ready   5s
```

---

## 4. Create a VPC

```bash
kubectl apply -f config/samples/ec2_v1alpha1_vpc.yaml
```

Watch the VPC phase progress:
```bash
kubectl get vpc -n default -w
# NAME         PHASE      VPCID                    AGE
# vpc-sample   Creating                            2s
# vpc-sample   Available  vpc-0abc1234def56789     12s
```

---

## 5. Delete a VPC

```bash
kubectl delete vpc vpc-sample -n default
```

The controller will:
1. Set `DeletionTimestamp`
2. Call `ec2.DeleteVpc` in AWS
3. Remove the finalizer
4. K8s garbage collects the object

---

## 6. Verify in AWS

```bash
aws ec2 describe-vpcs \
  --filters "Name=tag:Name,Values=my-infra-vpc" \
  --region ap-south-2
```

---

## 7. View controller logs

```bash
# When running as pods
kubectl logs -n infra-operator-system \
  deployment/infra-operator-controller-manager -f

# When running locally (make run)
# logs print directly to terminal
```

---

## 8. Uninstall

```bash
# Remove all VPC CRs first (triggers deletion of AWS VPCs)
kubectl delete vpc --all -n default

# Remove CRDs and RBAC
make undeploy

# Or just remove CRDs
make uninstall
```

---

## 9. Run unit tests

No cluster or AWS credentials needed. Tests use `envtest` (in-process API
server) and a `fakeEC2` stub for all AWS calls.

```bash
# First time only — downloads envtest binaries
make setup-envtest

# Run all unit tests
make test
```

See [Testing Guide](testing.md) for what each test covers.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `AWSCreds` stuck `NotFound` | Secret doesn't exist | Create the secret in the correct namespace |
| `AWSCreds` stuck `NotReady` | Secret missing keys | Check `kubectl describe secret aws-creds` |
| VPC stuck `Pending` | AWSCreds not Ready | Fix AWSCreds first |
| VPC stuck `Creating` | AWS provisioning slowly | Wait — controller polls every 30s |
| VPC stuck `Failed` | AWS API error | Check controller logs for the error message |
| `kubectl delete vpc` hangs | Finalizer not removed | AWS DeleteVpc is failing — check logs |
