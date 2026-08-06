# IAM role External Secrets Operator assumes via IRSA (no static AWS
# credentials in the cluster) so it can read the app's prod secrets straight
# from Secrets Manager. Wires up ops/k8s/overlays/prod/external-secret.yaml's
# `cluster-secret-store` — that ClusterSecretStore's serviceAccountRef must
# point at the ServiceAccount this role is bound to (see outputs.tf).
module "eso_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name = "${local.cluster_name}-external-secrets"

  attach_external_secrets_policy = true
  external_secrets_secrets_manager_arns = [
    "arn:aws:secretsmanager:${var.region}:*:secret:${var.cluster_name}/*",
  ]

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["${var.eso_namespace}:${var.eso_service_account}"]
    }
  }
}
