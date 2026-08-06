output "cluster_name" {
  description = "EKS cluster name — pass to `aws eks update-kubeconfig`."
  value       = module.eks.cluster_name
}

output "configure_kubectl" {
  description = "Run this to point kubectl at the new cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "ecr_repository_url" {
  description = "Push the app image here; matches the `images.newTag` rewrite in ops/k8s/overlays/*/kustomization.yaml once you set newName to this URL."
  value       = aws_ecr_repository.app.repository_url
}

output "eso_irsa_role_arn" {
  description = "Annotate the external-secrets ServiceAccount with eks.amazonaws.com/role-arn = this value (Helm value: serviceAccount.annotations)."
  value       = module.eso_irsa.iam_role_arn
}

output "oidc_provider_arn" {
  description = "The cluster's IRSA OIDC provider — needed if you add more IRSA roles (e.g. for the AWS Load Balancer Controller) later."
  value       = module.eks.oidc_provider_arn
}
