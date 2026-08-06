variable "region" {
  description = "AWS region to provision into."
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "Name shared by the EKS cluster, ECR repo, and resource tags."
  type        = string
  default     = "go-template"
}

variable "environment" {
  description = "Deployment environment tag (dev|staging|prod) — mirrors ops/k8s/overlays/<environment>."
  type        = string
  default     = "prod"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC. Sized for a single small-to-medium EKS cluster."
  type        = string
  default     = "10.0.0.0/16"
}

variable "single_nat_gateway" {
  description = "Use one NAT gateway for all private subnets instead of one per AZ. Cheaper; less available. Set false for real prod HA."
  type        = bool
  default     = true
}

variable "kubernetes_version" {
  description = "EKS control-plane version."
  type        = string
  default     = "1.31"
}

variable "node_instance_types" {
  description = "Instance types for the managed node group."
  type        = list(string)
  default     = ["t3.medium"]
}

variable "node_min_size" {
  description = "Minimum node count. The app's own scaling is HPA-owned (see ops/k8s/base/hpa.yaml) — this bounds the cluster, not the app."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Maximum node count."
  type        = number
  default     = 4
}

variable "node_desired_size" {
  description = "Starting node count."
  type        = number
  default     = 2
}

variable "eso_namespace" {
  description = "Namespace External Secrets Operator runs in — must match the ServiceAccount annotated with the IRSA role output by this stack."
  type        = string
  default     = "external-secrets"
}

variable "eso_service_account" {
  description = "External Secrets Operator's ServiceAccount name (the Helm chart default)."
  type        = string
  default     = "external-secrets"
}
