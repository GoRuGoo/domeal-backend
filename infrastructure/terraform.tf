terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = " =6.7.0"
    }

    local = {
      source  = "hashicorp/local"
      version = " = 2.5.3"
    }
  }

  required_version = "= 1.12.2"

  backend "s3" {
    bucket       = "domeal-tfstate"
    key          = "domeal/terraform.tfstate"
    region       = "ap-northeast-1"
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region = "ap-northeast-1"
}
