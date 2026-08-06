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
├── argocd/application.yaml           optional ArgoCD Application (prod)
└── aws/                              Terraform example: VPC + EKS + ECR + IRSA — see ops/aws/README.md
```

## Rendering & applying

```bash
make k8s-render ENV=dev      # or staging | prod
make k8s-apply  ENV=prod
```

These two commands assume a cluster already exists and your `kubectl`
context already points at it. Two ways to get there — pick one:

### Option A — local cluster (fastest way to see it running)

```bash
# 1. Create a local cluster (kind: https://kind.sigs.k8s.io)
brew install kind          # or: go install sigs.k8s.io/kind@latest
kind create cluster --name go-template

# 2. Build the image and load it straight into kind (no registry needed)
make docker-build
kind load docker-image go-template:latest --name go-template

# 3. Point the dev overlay at that local tag and apply it
#    ops/k8s/overlays/dev/kustomization.yaml already sets images.newTag: dev —
#    retag what you built, or just point newTag at `latest` for this run.
docker tag go-template:latest go-template:dev
kubectl create namespace go-template-dev --dry-run=client -o yaml | kubectl apply -f -
make k8s-apply ENV=dev

# 4. Verify
kubectl -n go-template-dev get pods
kubectl -n go-template-dev port-forward svc/go-template 8080:8080 &
curl localhost:8080/healthz
```

`kind delete cluster --name go-template` tears it down. This path never
touches ArgoCD or a real cloud — it's the fastest way to confirm the
manifests are internally consistent (probes, resource limits, ConfigMap
wiring) before pointing anything at a real cluster.

### Option B — a real cluster via ArgoCD (the GitOps path this repo is built for)

Needs an actual Kubernetes cluster already running somewhere (EKS, GKE, AKS,
or self-managed) — `ops/aws/` provisions one on AWS end to end if you don't
have one; see `ops/aws/README.md`.

```bash
# 1. Point kubectl at your cluster (e.g. after ops/aws: see its README step 1)

# 2. Install ArgoCD
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# 3. Point the Application at YOUR fork/repo (application.yaml's repoURL
#    currently points at this template's own repo — change it first) and apply it
kubectl apply -f ops/argocd/application.yaml

# 4. Watch it sync (or use the ArgoCD UI: kubectl -n argocd port-forward svc/argocd-server 8080:443)
kubectl -n argocd get application go-template-prod -w
```

From here, every deploy is `git push` — ArgoCD's `selfHeal`/`prune` keep the
cluster matching `ops/k8s/overlays/prod` automatically (see "How env-var
changes reach running pods" below for why a config change actually rolls
the pods, not just updates a ConfigMap no one reads again).

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
