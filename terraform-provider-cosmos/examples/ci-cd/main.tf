terraform {
  required_providers {
    cosmos = {
      source = "cosmos-cloud.io/azukaar/cosmos"
    }
  }
}

provider "cosmos" {
  base_url = "https://cosmos.example.com"
  token    = var.cosmos_token
}

variable "cosmos_token" {
  type      = string
  sensitive = true
}

variable "github_token" {
  type      = string
  sensitive = true
}

# S3-compatible storage on the nodes tagged "storage"
resource "cosmos_object_storage" "main" {
  name = "main"
  tags = ["storage"]

  max_storage_gb_per_node = 500

  # Only the keys set here change; the rest of the job configuration is kept
  jobs = jsonencode({
    scrubEnabled = true
  })

  backup = {
    repository = "/mnt/backups/object-storage-meta"
  }
}

# Docker registry the builds push to, stored in the object storage above
resource "cosmos_registry" "images" {
  name              = "images"
  type              = "docker"
  host              = "registry.example.com"
  storage_backend   = "seaweedfs"
  storage_seaweedfs = cosmos_object_storage.main.name
}

# Token for pulling the images from outside the cluster
resource "cosmos_registry_token" "pull" {
  registry = cosmos_registry.images.name
  name     = "external-pull"
  scopes   = ["pull"]
}

# Postgres for the app
resource "cosmos_database" "app" {
  name = "appdb"

  backup = {
    repository       = "/mnt/backups/appdb"
    retention_policy = "--keep-daily 7 --keep-weekly 4"
  }
}

resource "cosmos_database_logical" "app" {
  instance = cosmos_database.app.name
  database = "app"
}

# Build the repository on every push to main and deploy it
resource "cosmos_ci_project" "app" {
  name     = "my-app"
  repo_url = "https://github.com/example/my-app"
  token    = var.github_token
  registry = cosmos_registry.images.name
  branches = ["main"]

  secrets = [
    {
      name  = "DATABASE_URL"
      value = cosmos_database_logical.app.url
    },
  ]

  deploy = jsonencode({
    enabled = true
    name    = "myapp"
    environments = {
      main = {
        name       = "production"
        host       = "app.example.com"
        autoDeploy = true
      }
    }
    template = {
      replicas = 2
      port     = 3000
    }
  })
}

# A function served from a package published in an npm registry
resource "cosmos_registry" "packages" {
  name              = "packages"
  type              = "npm"
  host              = "npm.example.com"
  storage_backend   = "seaweedfs"
  storage_seaweedfs = cosmos_object_storage.main.name
}

resource "cosmos_function" "hello" {
  name     = "hello"
  runtime  = "node22"
  registry = cosmos_registry.packages.name
  package  = "hello-fn"
  handler  = "handler"

  route = jsonencode({
    Host = "hello.example.com"
  })
}
