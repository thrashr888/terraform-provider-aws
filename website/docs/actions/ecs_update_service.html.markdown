---
subcategory: "ECS (Elastic Container)"
layout: "aws"
page_title: "AWS: aws_ecs_update_service"
description: |-
  Updates an Amazon ECS service
---

# Action: aws_ecs_update_service

~> **NOTE:** This action is in beta and its interface may change in future versions.

Updates an Amazon ECS service. This action can force new deployments, update task definitions, adjust desired counts, and modify service configuration during Terraform operations.

For more information about Amazon ECS services, see the [Amazon ECS Developer Guide](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs_services.html).

## Example Usage

### Force New Deployment

```terraform
action "aws_ecs_update_service" "example" {
  config {
    cluster_name         = "my-cluster"
    service_name         = "my-service"
    force_new_deployment = true
  }
}

resource "terraform_data" "deployment_trigger" {
  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.aws_ecs_update_service.example]
    }
  }
}
```

### Update Task Definition

```terraform
action "aws_ecs_update_service" "example" {
  config {
    cluster_name    = aws_ecs_cluster.example.name
    service_name    = aws_ecs_service.example.name
    task_definition = aws_ecs_task_definition.updated.arn
  }
}
```

### Scale Service

```terraform
action "aws_ecs_update_service" "example" {
  config {
    cluster_name  = aws_ecs_cluster.example.name
    service_name  = aws_ecs_service.example.name
    desired_count = 5
  }
}
```

### CI/CD Integration

```terraform
action "aws_ecs_update_service" "deploy" {
  config {
    cluster_name                      = var.cluster_name
    service_name                      = var.service_name
    task_definition                   = aws_ecs_task_definition.app.arn
    force_new_deployment              = true
    health_check_grace_period_seconds = 300
    timeout                           = 1800
  }
}

resource "terraform_data" "deployment" {
  triggers_replace = [
    aws_ecs_task_definition.app.revision
  ]

  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.aws_ecs_update_service.deploy]
    }
  }
}
```

### Environment-Specific Updates

```terraform
locals {
  service_config = {
    production = {
      desired_count = 10
      timeout       = 3600
    }
    staging = {
      desired_count = 2
      timeout       = 1200
    }
  }
}

action "aws_ecs_update_service" "example" {
  config {
    cluster_name  = aws_ecs_cluster.example.name
    service_name  = aws_ecs_service.example.name
    desired_count = local.service_config[var.environment].desired_count
    timeout       = local.service_config[var.environment].timeout
  }
}
```

## Argument Reference

The following arguments are required:

* `cluster_name` - (Required) The name or ARN of the ECS cluster that hosts the service.
* `service_name` - (Required) The name or ARN of the service to update.

The following arguments are optional:

* `task_definition` - (Optional) The task definition ARN to update the service to use. Must be a valid task definition ARN format.
* `desired_count` - (Optional) The number of instantiations of the task definition to place and keep running in the service. Must be >= 0.
* `force_new_deployment` - (Optional) Whether to force a new deployment of the service. Defaults to `false`.
* `health_check_grace_period_seconds` - (Optional) The period of time that the ECS service scheduler ignores unhealthy Elastic Load Balancing target health checks after a task has first started. Must be between 0 and 2147483647.
* `timeout` - (Optional) Maximum time in seconds to wait for the service update to complete. Must be between 60 and 3600. Defaults to 1200 (20 minutes).

## Notes

### IAM Permissions

The action requires the following IAM permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecs:UpdateService",
        "ecs:DescribeServices"
      ],
      "Resource": [
        "arn:aws:ecs:*:*:service/*",
        "arn:aws:ecs:*:*:cluster/*"
      ]
    }
  ]
}
```

### Deployment Behavior

- The action waits for any existing deployment to complete before starting a new update
- Progress updates are sent every 30 seconds during deployment
- The action considers a deployment successful when the service reaches a stable state with the desired count
- If a deployment fails, the action returns an error with failure details

### Best Practices

- Use `force_new_deployment` to pull updated container images without changing the task definition
- Set appropriate timeouts based on your service's deployment characteristics
- Use the action in CI/CD pipelines to coordinate application deployments with infrastructure changes
- Monitor the progress updates to track deployment status
