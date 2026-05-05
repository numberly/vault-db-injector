# Injection Modes

**Audience:** Application developer

As a developer, you don't choose the delivery mode — the operator sets
that at install time. What changes is what `kubectl get pod -o yaml`
shows versus what `kubectl exec -- env` shows inside the running
container. Knowing the difference matters when credentials don't appear
where you expect them.

!!! note
    "Delivery mode" (NRI vs webhook) and "annotation mode" (`classic`
    vs `uri`) are orthogonal. You choose the annotation mode via
    `<dbname>.mode`; the operator controls the delivery mode. Both
    annotation modes work with both delivery modes.

## Webhook mode (legacy)

In webhook mode, the injector fetches real credentials from Vault
during pod admission and writes them directly into the pod's env vars
before the PodSpec is stored.

What you see in the running pod:

```bash
$ kubectl -n team-myapp get pod myapp -o yaml | grep -A5 env:
        env:
        - name: DB_USER
          value: vault-myapp-prod-1746345600-x8k2j9ab
        - name: DB_PASS
          value: A1B2-c3d4-E5F6-g7h8
```

The real username and password are visible in the PodSpec, in etcd,
and in Kubernetes audit logs.

## NRI mode (canonical)

In NRI mode, the webhook places opaque placeholder strings in the env
vars at admission. A node-local NRI plugin intercepts `CreateContainer`
and substitutes the real credentials before `runc` executes — the app
process starts with the real values already in its environment.

What `kubectl get pod -o yaml` shows:

```bash
$ kubectl -n team-myapp get pod myapp -o yaml | grep -A5 env:
        env:
        - name: DB_USER
          value: __VDBI_PH_3a7f1c9b2e4d6a8f0b1c3d5e7f9a2b4c6d8e0f1a3b5c7d9e1f3a5b7c9d1e3f5a___
        - name: DB_PASS
          value: __VDBI_PH_8e2f4a6c0b8d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e___
```

What `kubectl exec -- env` shows inside the running container:

```bash
$ kubectl -n team-myapp exec myapp -- env | grep DB_
DB_USER=vault-myapp-prod-1746345600-x8k2j9ab
DB_PASS=A1B2-c3d4-E5F6-g7h8
```

The real credentials never appear in any persisted Kubernetes resource.

## How to tell which mode is active

```bash
kubectl -n team-myapp get pod myapp -o yaml | grep VDBI_PH
```

A match means NRI mode is active. No match means webhook (legacy) mode.

## Troubleshooting placeholder strings in the running container

If `kubectl exec -- env` shows placeholder strings rather than real
credentials, the NRI plugin on that node did not perform the
substitution. Common causes:

- The plugin DaemonSet pod is not running on the node where the pod was scheduled.
- The pod was admitted without the `vault-db-injector: "true"` label, then the label was added post-admission (labels are read at admission time only).
- The node was not registered with containerd NRI.

See [troubleshooting](troubleshooting.md) for diagnosis steps.
