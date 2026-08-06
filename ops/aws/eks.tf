locals {
  cluster_name = "${var.cluster_name}-${var.environment}"
}

# EKS cluster + one managed node group. enable_irsa wires the OIDC provider
# this stack's irsa.tf uses to grant External Secrets Operator scoped access
# to Secrets Manager — no long-lived AWS credentials in the cluster.
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = local.cluster_name
  cluster_version = var.kubernetes_version

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  enable_irsa = true

  cluster_endpoint_public_access = true

  eks_managed_node_groups = {
    default = {
      instance_types = var.node_instance_types
      min_size       = var.node_min_size
      max_size       = var.node_max_size
      desired_size   = var.node_desired_size

      # Matches the Deployment's runAsNonRoot + seccomp RuntimeDefault
      # baseline in ops/k8s/base/deployment.yaml — AL2023 ships a recent
      # kernel with seccomp/AppArmor profiles available out of the box.
      ami_type = "AL2023_x86_64_STANDARD"
    }
  }
}
