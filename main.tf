terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0.1"
    }
  }
}

provider "docker" {}

resource "docker_image" "localstack" {
  name         = "localstack/localstack"
  keep_locally = false
}

resource "docker_container" "localstack" {
  image = docker_image.localstack.image_id
  name  = "localstack"

  ports {
    internal = 4566
    external = 4566
  }

  env = [
    "SERVICES=s3,dynamodb,sqs,sns",
    "DEFAULT_REGION=us-east-1",
    "DEBUG=1"
  ]

  healthcheck {
    test        = ["CMD-SHELL", "curl --silent --fail http://localhost:4566/_localstack/health || exit 1"]
    interval    = "10s"
    retries     = 5
    start_period = "15s"
    timeout     = "10s"
  }
}
