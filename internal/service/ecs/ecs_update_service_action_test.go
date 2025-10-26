// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ecs_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	sdkacctest "github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/hashicorp/terraform-provider-aws/internal/acctest"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/names"
)

func TestAccECSUpdateServiceAction_basic(t *testing.T) {
	ctx := acctest.Context(t)
	rName := sdkacctest.RandomWithPrefix(acctest.ResourcePrefix)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(ctx, t) },
		ErrorCheck:               acctest.ErrorCheck(t, names.ECSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_14_0),
		},
		CheckDestroy: testAccCheckServiceDestroy(ctx),
		Steps: []resource.TestStep{
			{
				Config: testAccUpdateServiceActionConfig_basic(rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUpdateServiceActionCompleted(ctx, rName),
				),
			},
		},
	})
}

func TestAccECSUpdateServiceAction_taskDefinition(t *testing.T) {
	ctx := acctest.Context(t)
	rName := sdkacctest.RandomWithPrefix(acctest.ResourcePrefix)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(ctx, t) },
		ErrorCheck:               acctest.ErrorCheck(t, names.ECSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_14_0),
		},
		CheckDestroy: testAccCheckServiceDestroy(ctx),
		Steps: []resource.TestStep{
			{
				Config: testAccUpdateServiceActionConfig_taskDefinition(rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUpdateServiceActionCompleted(ctx, rName),
					testAccCheckServiceUsesTaskDefinition(ctx, rName, fmt.Sprintf("%s-updated", rName)),
				),
			},
		},
	})
}

func TestAccECSUpdateServiceAction_desiredCount(t *testing.T) {
	ctx := acctest.Context(t)
	rName := sdkacctest.RandomWithPrefix(acctest.ResourcePrefix)

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(ctx, t) },
		ErrorCheck:               acctest.ErrorCheck(t, names.ECSServiceID),
		ProtoV5ProviderFactories: acctest.ProtoV5ProviderFactories,
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_14_0),
		},
		CheckDestroy: testAccCheckServiceDestroy(ctx),
		Steps: []resource.TestStep{
			{
				Config: testAccUpdateServiceActionConfig_desiredCount(rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckUpdateServiceActionCompleted(ctx, rName),
					testAccCheckServiceDesiredCount(ctx, rName, 2),
				),
			},
		},
	})
}

func testAccCheckUpdateServiceActionCompleted(ctx context.Context, clusterName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		conn := acctest.Provider.Meta().(*conns.AWSClient).ECSClient(ctx)

		// Verify service exists and is stable
		input := &ecs.DescribeServicesInput{
			Cluster:  &clusterName,
			Services: []string{clusterName}, // service name same as cluster name in tests
		}

		output, err := conn.DescribeServices(ctx, input)
		if err != nil {
			return fmt.Errorf("error describing ECS service: %w", err)
		}

		if len(output.Services) == 0 {
			return fmt.Errorf("ECS service not found")
		}

		service := output.Services[0]

		// Check that service has a stable deployment
		for _, deployment := range service.Deployments {
			if aws.ToString(deployment.Status) == "PRIMARY" {
				if deployment.RolloutState != "COMPLETED" {
					return fmt.Errorf("service deployment not completed, current state: %s", deployment.RolloutState)
				}
				if deployment.RunningCount != deployment.DesiredCount {
					return fmt.Errorf("service not at desired count: running=%d, desired=%d", 
						deployment.RunningCount, deployment.DesiredCount)
				}
				return nil
			}
		}

		return fmt.Errorf("no primary deployment found")
	}
}

func testAccCheckServiceUsesTaskDefinition(ctx context.Context, clusterName, taskDefFamily string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		conn := acctest.Provider.Meta().(*conns.AWSClient).ECSClient(ctx)

		input := &ecs.DescribeServicesInput{
			Cluster:  &clusterName,
			Services: []string{clusterName},
		}

		output, err := conn.DescribeServices(ctx, input)
		if err != nil {
			return fmt.Errorf("error describing ECS service: %w", err)
		}

		if len(output.Services) == 0 {
			return fmt.Errorf("ECS service not found")
		}

		service := output.Services[0]
		if service.TaskDefinition == nil {
			return fmt.Errorf("service has no task definition")
		}

		// Check if task definition contains the expected family name
		taskDefArn := *service.TaskDefinition
		if !contains(taskDefArn, taskDefFamily) {
			return fmt.Errorf("service not using expected task definition family %s, current: %s", 
				taskDefFamily, taskDefArn)
		}

		return nil
	}
}

func testAccCheckServiceDesiredCount(ctx context.Context, clusterName string, expectedCount int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		conn := acctest.Provider.Meta().(*conns.AWSClient).ECSClient(ctx)

		input := &ecs.DescribeServicesInput{
			Cluster:  &clusterName,
			Services: []string{clusterName},
		}

		output, err := conn.DescribeServices(ctx, input)
		if err != nil {
			return fmt.Errorf("error describing ECS service: %w", err)
		}

		if len(output.Services) == 0 {
			return fmt.Errorf("ECS service not found")
		}

		service := output.Services[0]
		if service.DesiredCount != int32(expectedCount) {
			return fmt.Errorf("service desired count mismatch: expected=%d, actual=%d", 
				expectedCount, service.DesiredCount)
		}

		return nil
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		 indexOf(s, substr) >= 0)))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// Test configurations

func testAccUpdateServiceActionConfig_basic(rName string) string {
	return fmt.Sprintf(`
resource "aws_ecs_cluster" "test" {
  name = %[1]q
}

resource "aws_ecs_task_definition" "test" {
  family = %[1]q
  container_definitions = jsonencode([
    {
      name  = "test"
      image = "nginx:latest"
      memory = 128
      essential = true
    }
  ])
}

resource "aws_ecs_service" "test" {
  name            = %[1]q
  cluster         = aws_ecs_cluster.test.id
  task_definition = aws_ecs_task_definition.test.arn
  desired_count   = 1

  deployment_configuration {
    maximum_percent         = 200
    minimum_healthy_percent = 100
  }
}

action "aws_ecs_update_service" "test" {
  config {
    cluster_name         = aws_ecs_cluster.test.name
    service_name         = aws_ecs_service.test.name
    force_new_deployment = true
  }
}

resource "terraform_data" "trigger" {
  input = "trigger"
  lifecycle {
    action_trigger {
      events  = [before_create, before_update]
      actions = [action.aws_ecs_update_service.test]
    }
  }
}
`, rName)
}

func testAccUpdateServiceActionConfig_taskDefinition(rName string) string {
	return fmt.Sprintf(`
resource "aws_ecs_cluster" "test" {
  name = %[1]q
}

resource "aws_ecs_task_definition" "test" {
  family = %[1]q
  container_definitions = jsonencode([
    {
      name  = "test"
      image = "nginx:latest"
      memory = 128
      essential = true
    }
  ])
}

resource "aws_ecs_task_definition" "updated" {
  family = "%[1]s-updated"
  container_definitions = jsonencode([
    {
      name  = "test"
      image = "nginx:1.21"
      memory = 128
      essential = true
    }
  ])
}

resource "aws_ecs_service" "test" {
  name            = %[1]q
  cluster         = aws_ecs_cluster.test.id
  task_definition = aws_ecs_task_definition.test.arn
  desired_count   = 1

  deployment_configuration {
    maximum_percent         = 200
    minimum_healthy_percent = 100
  }
}

action "aws_ecs_update_service" "test" {
  config {
    cluster_name    = aws_ecs_cluster.test.name
    service_name    = aws_ecs_service.test.name
    task_definition = aws_ecs_task_definition.updated.arn
  }
}

resource "terraform_data" "trigger" {
  input = "trigger"
  lifecycle {
    action_trigger {
      events  = [before_create, before_update]
      actions = [action.aws_ecs_update_service.test]
    }
  }
}
`, rName)
}

func testAccUpdateServiceActionConfig_desiredCount(rName string) string {
	return fmt.Sprintf(`
resource "aws_ecs_cluster" "test" {
  name = %[1]q
}

resource "aws_ecs_task_definition" "test" {
  family = %[1]q
  container_definitions = jsonencode([
    {
      name  = "test"
      image = "nginx:latest"
      memory = 128
      essential = true
    }
  ])
}

resource "aws_ecs_service" "test" {
  name            = %[1]q
  cluster         = aws_ecs_cluster.test.id
  task_definition = aws_ecs_task_definition.test.arn
  desired_count   = 1

  deployment_configuration {
    maximum_percent         = 200
    minimum_healthy_percent = 100
  }
}

action "aws_ecs_update_service" "test" {
  config {
    cluster_name   = aws_ecs_cluster.test.name
    service_name   = aws_ecs_service.test.name
    desired_count  = 2
  }
}

resource "terraform_data" "trigger" {
  input = "trigger"
  lifecycle {
    action_trigger {
      events  = [before_create, before_update]
      actions = [action.aws_ecs_update_service.test]
    }
  }
}
`, rName)
}
