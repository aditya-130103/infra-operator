# AWSCreds API Reference

**Group**: `core.infra.example.com`  
**Version**: `v1alpha1`  
**Kind**: `AWSCreds`  
**Scope**: Namespaced

---

## Purpose

`AWSCreds` is a validated reference to a Kubernetes Secret that holds AWS
credentials. It does not create any AWS resources. It acts as a gateway —
other resource CRDs (e.g. `VPC`) reference an `AWSCreds` object and the
operator checks it is `Ready` before making any AWS API calls.

---

## Spec

```yaml
spec:
  credentials:
    secretRef:
      name: aws-creds          # name of the pre-existing Secret
      namespace: default       # namespace where the Secret lives
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.credentials.secretRef.name` | string | yes | Name of the Secret holding AWS credentials |
| `spec.credentials.secretRef.namespace` | string | yes | Namespace of the Secret |

---

## Secret format

The referenced Secret must contain exactly these keys:

```bash
kubectl create secret generic aws-creds \
  --from-literal=AWS_ACCESS_KEY_ID=AKIA... \
  --from-literal=AWS_SECRET_ACCESS_KEY=xxx...
```

| Key | Required | Description |
|-----|----------|-------------|
| `AWS_ACCESS_KEY_ID` | yes | IAM user or role access key ID |
| `AWS_SECRET_ACCESS_KEY` | yes | IAM user or role secret access key |

---

## Status

```yaml
status:
  phase: Ready
  conditions:
    - type: Ready
      status: "True"
      reason: SecretFound
      message: "Secret found and has valid AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY"
```

### Phase values

| Phase | Meaning |
|-------|---------|
| `Ready` | Secret exists, both keys present and non-empty |
| `NotReady` | Secret exists but missing or empty keys |
| `NotFound` | Referenced Secret does not exist |

### Condition reasons

| Reason | Phase | Description |
|--------|-------|-------------|
| `SecretFound` | Ready | All checks passed |
| `SecretNotFound` | NotFound | Secret does not exist in the referenced namespace |
| `SecretMissingField` | NotReady | Secret exists but missing `AWS_ACCESS_KEY_ID` or `AWS_SECRET_ACCESS_KEY` |
| `SecretEmptyFields` | NotReady | Keys exist but values are empty strings |

---

## Example

```yaml
apiVersion: core.infra.example.com/v1alpha1
kind: AWSCreds
metadata:
  name: prod-creds
  namespace: default
spec:
  credentials:
    secretRef:
      name: aws-creds
      namespace: default
```

---

## Behavior notes

- The controller watches the referenced Secret via `EnqueueRequestsFromMapFunc`.
  If the Secret is deleted or updated, the `AWSCreds` object is automatically
  re-reconciled and its status updated.
- The controller makes no AWS API calls. It is purely a validator.
- A `VPC` CR that references an `AWSCreds` with `phase != Ready` will requeue
  every 10 seconds until the credentials become ready.
