# Deployment

Docker, Kubernetes (Kustomize), and GitOps assets for the service.

```
ops/
├── docker/otel-collector.yaml       local OTel collector config (compose)
├── k8s/
│   ├── base/                        environment-agnostic manifests
│   │   ├── deployment.yaml           probes, securityContext, resources
│   │   ├── service.yaml
│   │   ├── hpa.yaml
│   │   └── kustomization.yaml         configMapGenerator (hashed → auto-rollout)
│   └── overlays/
│       ├── dev/                      LOG_LEVEL=debug, full sampling, dev ns
│       ├── staging/                  prod-like, 50% sampling
│       └── prod/                     10% sampling, ExternalSecret, HPA 3–20
└── argocd/application.yaml           optional ArgoCD Application (prod)
```

## Rendering & applying

```bash
make k8s-render ENV=dev      # or staging | prod
make k8s-apply  ENV=prod
```

## How env-var changes reach running pods (the important part)

The base uses a Kustomize **`configMapGenerator`**, not a static ConfigMap. Kustomize appends a **content hash** to the ConfigMap name (`go-template-config-<hash>`) and rewrites the Deployment's `envFrom` reference to match. So:

```
edit an env value in an overlay  →  merge PR
  →  ArgoCD/Flux syncs  →  new content hash  →  new ConfigMap name
  →  Deployment spec changes  →  Kubernetes rolling update  →  new value live
```

This is why a committed config change actually restarts pods with the new value — **with no operator**. A plain (unhashed) ConfigMap would update in the cluster but leave pods running the old env until a manual restart; that footgun is avoided here.

Overlays override individual keys with `behavior: merge`, so each environment only states its differences; the hash is recomputed from the merged result.

## Secrets

Secrets never live in git in plaintext. `overlays/prod/external-secret.yaml` declares an **ExternalSecret** (External Secrets Operator) — only *references* are committed; ESO materializes the `go-template-secrets` Secret from your backing store (AWS Secrets Manager, Vault, GCP SM, …). The Deployment mounts it via `envFrom … secretRef … optional: true`, so pods stay schedulable even before ESO reconciles.

**Secret rotation caveat:** the content-hash trick covers ConfigMaps but **not** the ESO-created Secret (Kustomize doesn't generate it, so its name doesn't change on rotation). If you need a rotated secret to roll pods automatically, add [Stakater Reloader](https://github.com/stakater/Reloader) and annotate the Deployment:

```yaml
metadata:
  annotations:
    reloader.stakater.com/auto: "true"
```

Swap ESO for SOPS or sealed-secrets freely — the Deployment depends only on the resulting Secret *name*, not on how it was produced.

## Repo strategy — read before going to real production

This `ops/` folder is a **working reference that runs day one**. For a single service or a small team, keeping it in the app repo is fine.

**At scale, the professional pattern is repo separation:**

| Repo | Owns |
|------|------|
| **App repo** (this one) | source, tests, Dockerfile, CI that builds + pushes the image. Optionally the Kustomize **`base`** (it versions with the code it deploys). |
| **GitOps / config repo** (separate) | the **overlays**, secret wiring, and the ArgoCD/Flux `Application`s. Platform team owns it; app teams PR into it. |

Why: smaller blast radius and cleaner RBAC (deploy changes ≠ code changes), app CI stays out of config tweaks, one pane showing every environment across services, and image promotion (dev→staging→prod) done by moving a tag in the config repo — decoupled from app merges (ArgoCD Image Updater / Flux image automation).

**Migration when you get there:** keep `k8s/base/` here; lift `k8s/overlays/` + `argocd/` + `external-secret.yaml` into your GitOps repo and point one ArgoCD `Application` per environment at the matching overlay path. What's in this folder is exactly the seed you copy out — don't treat the in-repo overlays as the production source of truth once you've extracted them.
