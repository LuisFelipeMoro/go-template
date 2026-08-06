# ops/aws — Terraform example: deploy to AWS EKS

A minimal, working Terraform stack that provisions what `ops/k8s/` needs to
actually run in AWS: a VPC, an EKS cluster with one managed node group, an
ECR repository for the app image, and an IRSA role for External Secrets
Operator so `ops/k8s/overlays/prod/external-secret.yaml` can read real
secrets from AWS Secrets Manager without static credentials in the cluster.

This is a **reference that runs day one**, the same philosophy as
`ops/k8s/` (see the root `ops/README.md`) — not a hardened production
module. It uses the well-known community modules
(`terraform-aws-modules/vpc`, `terraform-aws-modules/eks`,
`terraform-aws-modules/iam`) rather than hand-rolled resources, so the
VPC/EKS wiring itself is battle-tested; what's specific to this template is
the ECR repo, the IRSA role, and how the outputs plug into `ops/k8s`.

## What it provisions

| File | Resource |
|------|----------|
| `vpc.tf` | VPC, 3 AZs, public + private subnets, one NAT gateway (`single_nat_gateway = true` by default — set `false` for real multi-AZ HA) |
| `eks.tf` | EKS cluster + one managed node group, IRSA enabled |
| `ecr.tf` | ECR repo (immutable tags, scan-on-push) + a lifecycle policy expiring untagged images after 14 days |
| `irsa.tf` | IAM role for External Secrets Operator, scoped to `secretsmanager:GetSecretValue` on `<cluster_name>/*` |

## Prerequisites

- Terraform >= 1.7
- An AWS account + credentials (`aws configure` or an assumed role) with
  permission to create VPCs, EKS clusters, IAM roles, and ECR repos
- `aws` CLI and `kubectl`

## Step by step

```bash
cd ops/aws
terraform init
terraform plan            # review before applying — this creates billable AWS resources
terraform apply
```

Wire the outputs into the rest of the deploy:

```bash
# 1. Point kubectl at the new cluster
$(terraform output -raw configure_kubectl)
# or explicitly:
aws eks update-kubeconfig --region "$(terraform output -raw region 2>/dev/null || echo us-east-1)" \
  --name "$(terraform output -raw cluster_name)"

# 2. Build and push the app image to the new ECR repo
ECR_URL=$(terraform output -raw ecr_repository_url)
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin "$ECR_URL"
cd ../.. && make docker-build
docker tag go-template:latest "$ECR_URL:v1.0.0"
docker push "$ECR_URL:v1.0.0"

# 3. Point the prod overlay at the new image (one-time; commit this)
#    Edit ops/k8s/overlays/prod/kustomization.yaml's `images:` entry:
#      images:
#        - name: go-template
#          newName: <ecr_repository_url from step 2>
#          newTag: v1.0.0

# 4. Install External Secrets Operator and annotate its ServiceAccount with
#    the IRSA role this stack created (Helm chart default namespace/name
#    match variables.tf's eso_namespace/eso_service_account defaults):
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets \
  -n external-secrets --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"="$(terraform output -raw eso_irsa_role_arn)"

# 5. Create the ClusterSecretStore the ExternalSecret references
#    (external-secret.yaml's secretStoreRef: cluster-secret-store) — see
#    https://external-secrets.io/latest/provider/aws-secrets-manager/ for the
#    manifest; it needs no static credentials, IRSA covers auth.

# 6. Apply the app
cd ../.. && make k8s-apply ENV=prod
```

Or skip step 6 and let ArgoCD own it instead — apply
`ops/argocd/application.yaml` against the new cluster once ArgoCD is
installed (`kubectl apply -f ops/argocd/application.yaml`); it watches
`ops/k8s/overlays/prod` and syncs on every merge to `main`.

## Tearing down

```bash
cd ops/aws
terraform destroy
```

This deletes the cluster, node group, VPC, and ECR repo. **ECR images are
deleted with it** — nothing here is protected against `destroy`; don't run
it against a stack backing real production traffic without a snapshot plan.

## Adapting for GCP / Azure

Same shape, different provider: `terraform-google-modules/kubernetes-engine`
(GKE) or `Azure/aks` (AKS) instead of `terraform-aws-modules/eks`, Workload
Identity instead of IRSA for the External Secrets Operator role, Artifact
Registry / ACR instead of ECR. `ops/k8s/` itself needs no changes — it's
already cloud-agnostic; only this `ops/aws/` stack (or its GCP/Azure
equivalent) and the `ClusterSecretStore` provider block are cloud-specific.
