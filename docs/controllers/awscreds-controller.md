# AWSCreds Controller

## Purpose

Validates that the Kubernetes Secret referenced by an `AWSCreds` CR exists and
contains the required AWS credential keys. Sets a `Ready` condition so the VPC
controller can gate on it before making AWS API calls.

**Makes zero AWS API calls. Creates zero resources.**

---

## Reconcile Logic

```
1. Fetch AWSCreds object
   └── NotFound → return nil (deleted, nothing to do)

2. Fetch Secret from spec.credentials.secretRef
   └── NotFound   → phase=NotFound,  Ready=False, reason=SecretNotFound
                     return ctrl.Result{}

3. Check Secret.Data has AWS_ACCESS_KEY_ID
   └── Missing    → phase=NotReady,  Ready=False, reason=SecretMissingField

4. Check Secret.Data has AWS_SECRET_ACCESS_KEY
   └── Missing    → phase=NotReady,  Ready=False, reason=SecretMissingField

5. Check both values are non-empty
   └── Empty      → phase=NotReady,  Ready=False, reason=SecretEmptyFields

6. All checks pass
   └── phase=Ready, Ready=True, reason=SecretFound
       return ctrl.Result{}
```

After every state change, `r.Status().Update()` writes the phase and condition
back to etcd before returning.

---

## Return values

| Situation | Return value | Why |
|-----------|-------------|-----|
| Secret not found | `ctrl.Result{}` | Watch on Secret will fire when it's created |
| Keys missing / empty | `ctrl.Result{}` | Watch on Secret will fire when it's updated |
| All good | `ctrl.Result{}` | No polling needed — watch stream handles re-triggers |
| Status update failed | `ctrl.Result{}, err` | etcd write failure — retry with backoff |

No `RequeueAfter` is used. The watch on Secrets replaces polling.

---

## Secret Watch

`SetupWithManager` adds a watch on `corev1.Secret` via
`handler.EnqueueRequestsFromMapFunc`. When any Secret changes, the map function
lists all `AWSCreds` objects and returns reconcile requests for those whose
`spec.credentials.secretRef` matches the changed Secret.

```
Secret "aws-creds" deleted
  → map function finds AWSCreds "prod-creds" references it
  → enqueues reconcile for "prod-creds"
  → reconciler runs, Secret not found → phase=NotFound
```

---

## RBAC

```
awscreds:       get, list, watch, create, update, patch, delete
awscreds/status: get, update, patch
awscreds/finalizers: update
secrets:        get, list, watch
```

---

## File

`internal/controller/awscreds_controller.go`
