# Testing Guide

## Why this operator has tests

Every time the reconcile logic changes, there are two risks:

1. **Regression** — something that worked before silently breaks. Nobody re-tests
   `isVPCNotFound` by hand after a refactor of `fetchEC2Client`.
2. **Scale** — manual testing means spinning up a kind cluster, applying CRs,
   waiting for AWS API calls, checking logs. That costs minutes per scenario.
   With 14 scenarios across two controllers, that's impractical to repeat on
   every PR.

The test suite runs all 17 scenarios in under 15 seconds with no cluster and no
AWS credentials.

---

## Tools and why each was chosen

### Standard `testing` package

Go's built-in test runner. Every test function named `Test*` in a `_test.go`
file is automatically discovered and run by `go test`. The runner creates a
`*testing.T` instance per test and passes it in — your test reports results
back through it:

```go
func TestIsVPCNotFound(t *testing.T) {
    result := isVPCNotFound(nil)
    if result != false {
        t.Errorf("expected false, got %v", result)   // mark failed, keep running
    }
}
```

`t.Errorf` marks the test failed and keeps going (collects all failures).
`t.Fatalf` marks failed and stops immediately (use when later checks would panic).

### Ginkgo v2 — BDD test framework

Kubebuilder scaffolds Ginkgo by default. Instead of `Test*` functions, tests
are structured as nested `Describe` / `Context` / `It` blocks that read like
a specification:

```go
Describe("AWSCreds Controller", func() {
    Context("when the referenced Secret does not exist", func() {
        It("sets phase=NotFound and reason=SecretNotFound", func() {
            // assertion here
        })
    })
})
```

Read out loud: *"AWSCreds Controller — when the referenced Secret does not
exist — it sets phase=NotFound."* This doubles as living documentation: anyone
on the team can read the test names to understand how the controller behaves
without reading the Go code.

| Block | Purpose |
|-------|---------|
| `Describe` | The component under test |
| `Context` | The scenario / condition |
| `It` | What should happen in that scenario |
| `BeforeEach` | Setup that runs before every `It` in scope |
| `AfterEach` | Cleanup that runs after every `It` in scope |

### Gomega — matcher library

Gomega replaces manual `if result != expected { t.Errorf(...) }` with
composable matchers:

```go
// standard testing
if ac.Status.Phase != "Ready" {
    t.Errorf("expected Ready, got %s", ac.Status.Phase)
}

// Gomega equivalent
Expect(ac.Status.Phase).To(Equal("Ready"))
```

On failure, Gomega prints the full structure of what it got vs what it
expected — no need to manually format the error message. Matchers compose:

```go
Expect(ac.Status.Conditions).To(ContainElement(
    SatisfyAll(
        HaveField("Reason", "SecretFound"),
        HaveField("Status", metav1.ConditionTrue),
    ),
))
```

### envtest — in-process Kubernetes API server

Controller tests call `k8sClient.Create`, `k8sClient.Get`,
`k8sClient.Status().Update` — real Kubernetes API calls. `envtest` starts a
real API server process in memory at test suite start and shuts it down when
tests finish. No kind cluster, no kubeconfig needed.

CRD validation runs against this server — the region enum on the VPC spec, the
`minLength` on `spec.name`, are all enforced exactly as they would be in
production.

`suite_test.go` owns the envtest lifecycle:

```go
var _ = BeforeSuite(func() {
    testEnv = &envtest.Environment{
        CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
    }
    cfg, err = testEnv.Start()       // starts API server
    k8sClient, err = client.New(cfg, ...)
})

var _ = AfterSuite(func() {
    testEnv.Stop()                   // shuts it down
})
```

### fakeEC2 — fake AWS client

The VPC controller calls `CreateVpc`, `DescribeVpcs`, and `DeleteVpc`. Running
real AWS calls in tests would:

- Require real credentials on every developer machine and CI runner
- Create real AWS resources (50 VPCs/day across a team) with real costs
- Make tests slow (AWS API latency) and flaky (network conditions)

`fakeEC2` is a struct that satisfies the `EC2API` interface. It holds canned
return values set by each test — no network, no credentials, instant response:

```go
type fakeEC2 struct {
    createOut   *awsec2.CreateVpcOutput
    createErr   error
    describeOut *awsec2.DescribeVpcsOutput
    describeErr error
    deleteErr   error
}

func (f *fakeEC2) CreateVpc(...) (*awsec2.CreateVpcOutput, error) {
    return f.createOut, f.createErr
}
```

This pattern is called **Dependency Injection** — instead of `VPCReconciler`
building its own EC2 client internally, the client is injected from outside via
the `NewEC2Client` factory field. The `EC2API` interface is the **seam** that
makes injection possible. Without the interface, the code would be locked to
the real `*awsec2.Client` and untestable without AWS.

Production (`NewEC2Client == nil`) → falls through to `buildEC2Client` → real AWS client.
Tests (`NewEC2Client != nil`) → returns `fakeEC2` → no AWS calls.

---

## Test isolation

Every `It` block runs with a clean state. `BeforeEach` creates the objects each
scenario needs; `AfterEach` deletes them. Without this, test 2 would find
objects left by test 1 and either fail to create (already exists) or assert
against stale state.

```
BeforeEach → create AWSCreds + Secret
It         → reconcile → assert phase
AfterEach  → delete AWSCreds + Secret
BeforeEach → create AWSCreds (next scenario, clean slate)
It         → reconcile → assert different phase
AfterEach  → delete
```

---

## Running tests

```bash
# First time only — downloads envtest binaries (~50MB)
make setup-envtest

# Run all tests (excludes e2e)
make test

# Verbose output — shows each It block by name
go test ./internal/... -v

# Run only AWSCreds tests
go test ./internal/controller/... -v

# Run only VPC tests
go test ./internal/controller/ec2/... -v
```

---

## AWSCreds controller — test cases

File: `internal/controller/awscreds_controller_test.go`

The AWSCreds controller makes no AWS API calls. It only validates that the
referenced Kubernetes Secret exists and contains the required credential keys.
All 6 cases are tested purely through envtest — no fakeEC2 needed.

| Scenario | Setup | Expected phase | Expected condition reason |
|----------|-------|---------------|--------------------------|
| AWSCreds object not found | Reconcile for non-existent name | — | No error, no side effects |
| Secret does not exist | AWSCreds created, no Secret | `NotFound` | `SecretNotFound` |
| Secret missing `AWS_ACCESS_KEY_ID` | Secret has only `AWS_SECRET_ACCESS_KEY` | `NotReady` | `SecretMissingField` |
| Secret missing `AWS_SECRET_ACCESS_KEY` | Secret has only `AWS_ACCESS_KEY_ID` | `NotReady` | `SecretMissingField` |
| Secret has empty values | Both keys present, both empty strings | `NotReady` | `SecretEmptyFields` |
| Secret is fully valid | Both keys present and non-empty | `Ready` | `SecretFound` |

---

## VPC controller — test cases

File: `internal/controller/ec2/vpc_controller_test.go`

The VPC controller calls AWS. All AWS calls go through `fakeEC2`. The
Kubernetes API calls go through envtest.

Each test that needs credentials creates:
- A `Secret` with fake (but non-empty) key values
- An `AWSCreds` object with `status.phase=Ready` (set via `Status().Update`)
- A `VPC` CR with valid `spec.region` and `spec.name`

The finalizer tests run two reconcile calls: the first adds the finalizer (step
3 in the reconcile loop always runs first on a new object), the second runs the
path being tested.

| Scenario | Setup | fakeEC2 config | Expected outcome |
|----------|-------|----------------|-----------------|
| First reconcile — no finalizer | New VPC, no prior reconcile | none (not reached) | Finalizer `vpc.ec2.infra.example.com/finalizer` added to object |
| AWSCreds not found | VPC references non-existent AWSCreds | none | `phase=Pending`, reason=`AWSCredsNotReady` |
| AWSCreds not ready | AWSCreds exists with `phase=NotReady` | none | `phase=Pending`, reason=`AWSCredsNotReady` |
| CreateVpc succeeds | Valid creds, no VpcID in status | `createOut` returns `vpc-abc123` | `status.vpcId=vpc-abc123`, `phase=Creating`, reason=`VPCCreating` |
| CreateVpc fails | Valid creds, no VpcID in status | `createErr` returns error | `phase=Failed`, reason=`CreateFailed` |
| DescribeVpcs — VPC available | VpcID set in status | `describeOut` returns `state=available` | `phase=Available`, `Ready=True`, reason=`VPCAvailable` |
| DescribeVpcs — VPC pending | VpcID set in status | `describeOut` returns `state=pending` | `phase=Creating`, `Ready=False`, reason=`VPCNotReady` |
| Drift detected | VpcID set in status | `describeErr` returns `InvalidVpcID.NotFound` | `status.vpcId` cleared, `phase=Pending`, reason=`VPCNotFound` |
| Deletion — happy path | VpcID set, `kubectl delete` called | `deleteErr=nil` | Finalizer removed, object garbage collected |
| Deletion — credentials not ready | VpcID set, no AWSCreds, `kubectl delete` called | none | `phase=Deleting`, reason=`AWSCredsNotReady`, finalizer kept |
| Deletion — DeleteVpc fails | VpcID set, valid creds, `kubectl delete` called | `deleteErr` returns AWS error | `phase=Deleting`, reason=`DeleteFailed`, finalizer kept |

---

## How to add a new test case

**1. Identify what you're testing** — pick one reconcile path, one scenario.
One `It` block = one scenario. Don't test multiple things in a single `It`.

**2. Add a `Context` + `It` inside the right `Describe` block.**

**3. Set up only what the scenario needs in `BeforeEach`.** The `AfterEach`
cleanup at the outer scope already handles deletion — don't duplicate it.

**4. For VPC tests that need AWS calls, configure `fakeEC2`:**

```go
// test that DescribeVpcs returning a non-transient error sets phase=Failed
Context("step 5 — DescribeVpcs returns a real AWS error", func() {
    BeforeEach(func() {
        createSecret()
        createReadyAWSCreds()
        createVPC()
    })

    It("sets phase=Failed and reason=DescribeFailed", func() {
        fake := &fakeEC2{describeErr: fmt.Errorf("RequestExpired: request has expired")}
        r := newReconciler(fake)
        _, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName}) // add finalizer

        v := &ec2v1alpha1.VPC{}
        Expect(k8sClient.Get(ctx, namespacedName, v)).To(Succeed())
        v.Status.VpcID = "vpc-existing"
        Expect(k8sClient.Status().Update(ctx, v)).To(Succeed())

        v = reconcile1(r)
        Expect(v.Status.Phase).To(Equal("Failed"))
        Expect(v.Status.Conditions).To(ContainElement(
            HaveField("Reason", "DescribeFailed"),
        ))
    })
})
```

**5. Run `make test` and verify the new case appears in the output.**

---

## Coverage

```bash
make test   # generates cover.out
go tool cover -func=cover.out   # per-function breakdown
go tool cover -html=cover.out   # visual HTML report
```

| Package | Coverage |
|---------|---------|
| `internal/controller` (AWSCreds) | ~63% |
| `internal/controller/ec2` (VPC) | ~71% |

**Intentionally uncovered:**

| Code | Why not tested |
|------|---------------|
| `SetupWithManager` | Wires controller to the manager — exercised at runtime, not meaningful to unit test |
| `buildEC2Client` | Thin wrapper around the AWS SDK config loader — would require real credentials or a local AWS mock server to test meaningfully |
