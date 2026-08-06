# Image registry for the app. Prod pins an immutable tag
# (ops/k8s/overlays/prod/kustomization.yaml's images.newTag) — tag mutability
# is set to IMMUTABLE here so that pin can't be silently overwritten.
resource "aws_ecr_repository" "app" {
  name                 = var.cluster_name
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

# Expire untagged images after 14 days; keep every tagged (immutable) image —
# those are the ones a Deployment or a rollback might still reference.
resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 14 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 14
        }
        action = { type = "expire" }
      }
    ]
  })
}
