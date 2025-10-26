// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ecs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YakDriver/regexache"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	awstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/hashicorp/terraform-provider-aws/internal/actionwait"
	"github.com/hashicorp/terraform-provider-aws/internal/framework"
	"github.com/hashicorp/terraform-provider-aws/names"
)

const (
	updateServicePollInterval = 15 * time.Second
)

// @Action(aws_ecs_update_service, name="Update Service")
func newUpdateServiceAction(_ context.Context) (action.ActionWithConfigure, error) {
	return &updateServiceAction{}, nil
}

type updateServiceAction struct {
	framework.ActionWithModel[updateServiceModel]
}

type updateServiceModel struct {
	framework.WithRegionModel
	ClusterName                     types.String `tfsdk:"cluster_name"`
	ServiceName                     types.String `tfsdk:"service_name"`
	TaskDefinition                  types.String `tfsdk:"task_definition"`
	DesiredCount                    types.Int64  `tfsdk:"desired_count"`
	ForceNewDeployment              types.Bool   `tfsdk:"force_new_deployment"`
	HealthCheckGracePeriodSeconds   types.Int64  `tfsdk:"health_check_grace_period_seconds"`
	Timeout                         types.Int64  `tfsdk:"timeout"`
}

func (a *updateServiceAction) Schema(ctx context.Context, req action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Updates an Amazon ECS service. This action can force new deployments, update task definitions, adjust desired counts, and modify service configuration.",
		Attributes: map[string]schema.Attribute{
			"cluster_name": schema.StringAttribute{
				Description: "The name or ARN of the ECS cluster that hosts the service",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexache.MustCompile(`^[a-zA-Z0-9\-_]+$|^arn:aws:ecs:[a-z0-9\-]+:[0-9]{12}:cluster/[a-zA-Z0-9\-_]+$`),
						"must be a valid cluster name or ARN",
					),
				},
			},
			"service_name": schema.StringAttribute{
				Description: "The name or ARN of the service to update",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexache.MustCompile(`^[a-zA-Z0-9\-_]+$|^arn:aws:ecs:[a-z0-9\-]+:[0-9]{12}:service/[a-zA-Z0-9\-_/]+$`),
						"must be a valid service name or ARN",
					),
				},
			},
			"task_definition": schema.StringAttribute{
				Description: "The task definition ARN to update the service to use",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexache.MustCompile(`^arn:aws:ecs:[a-z0-9\-]+:[0-9]{12}:task-definition/[a-zA-Z0-9\-_]+:[0-9]+$`),
						"must be a valid task definition ARN",
					),
				},
			},
			"desired_count": schema.Int64Attribute{
				Description: "The number of instantiations of the task definition to place and keep running in the service",
				Optional:    true,
				Validators: []validator.Int64{
					int64validator.AtLeast(0),
				},
			},
			"force_new_deployment": schema.BoolAttribute{
				Description: "Whether to force a new deployment of the service",
				Optional:    true,
			},
			"health_check_grace_period_seconds": schema.Int64Attribute{
				Description: "The period of time that the ECS service scheduler ignores unhealthy Elastic Load Balancing target health checks after a task has first started",
				Optional:    true,
				Validators: []validator.Int64{
					int64validator.Between(0, 2147483647),
				},
			},
			names.AttrTimeout: schema.Int64Attribute{
				Description: "Maximum time in seconds to wait for the service update to complete (default: 1200)",
				Optional:    true,
				Validators: []validator.Int64{
					int64validator.Between(60, 3600),
				},
			},
		},
	}
}

func (a *updateServiceAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	var config updateServiceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn := a.Meta().ECSClient(ctx)

	clusterName := config.ClusterName.ValueString()
	serviceName := config.ServiceName.ValueString()

	timeout := 20 * time.Minute
	if !config.Timeout.IsNull() {
		timeout = time.Duration(config.Timeout.ValueInt64()) * time.Second
	}

	tflog.Info(ctx, "Starting ECS update service action", map[string]any{
		"cluster_name":           clusterName,
		"service_name":           serviceName,
		"has_task_definition":    !config.TaskDefinition.IsNull(),
		"has_desired_count":      !config.DesiredCount.IsNull(),
		"force_new_deployment":   config.ForceNewDeployment.ValueBool(),
		"timeout_seconds":        int64(timeout.Seconds()),
	})

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("Starting update for ECS service %s in cluster %s...", serviceName, clusterName),
	})

	// Check current service state
	service, err := findServiceByName(ctx, conn, clusterName, serviceName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Service Not Found",
			fmt.Sprintf("ECS service %s was not found in cluster %s: %s", serviceName, clusterName, err),
		)
		return
	}

	// Check for existing deployment
	if hasActiveDeployment(service) {
		resp.SendProgress(action.InvokeProgressEvent{
			Message: fmt.Sprintf("ECS service %s has an active deployment, waiting for completion...", serviceName),
		})

		_, err = waitForServiceStable(ctx, conn, clusterName, serviceName, timeout)
		if err != nil {
			resp.Diagnostics.AddError(
				"Existing Deployment Failed",
				fmt.Sprintf("Existing deployment for service %s failed: %s", serviceName, err),
			)
			return
		}
	}

	// Build update input
	input := &ecs.UpdateServiceInput{
		Cluster: aws.String(clusterName),
		Service: aws.String(serviceName),
	}

	if !config.TaskDefinition.IsNull() {
		input.TaskDefinition = config.TaskDefinition.ValueStringPointer()
	}

	if !config.DesiredCount.IsNull() {
		input.DesiredCount = aws.Int32(int32(config.DesiredCount.ValueInt64()))
	}

	if !config.ForceNewDeployment.IsNull() && config.ForceNewDeployment.ValueBool() {
		input.ForceNewDeployment = true
	}

	if !config.HealthCheckGracePeriodSeconds.IsNull() {
		input.HealthCheckGracePeriodSeconds = aws.Int32(int32(config.HealthCheckGracePeriodSeconds.ValueInt64()))
	}

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("Sending update request to ECS service %s...", serviceName),
	})

	// Update service
	_, err = conn.UpdateService(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update Service",
			fmt.Sprintf("Could not update ECS service %s: %s", serviceName, err),
		)
		return
	}

	// Wait for deployment to complete
	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("ECS service %s update initiated, waiting for deployment to complete...", serviceName),
	})

	_, err = waitForServiceStable(ctx, conn, clusterName, serviceName, timeout)
	if err != nil {
		var timeoutErr *actionwait.TimeoutError
		var failureErr *actionwait.FailureStateError
		var unexpectedErr *actionwait.UnexpectedStateError

		if errors.As(err, &timeoutErr) {
			resp.Diagnostics.AddError(
				"Timeout Waiting for Service Update",
				fmt.Sprintf("ECS service %s did not reach stable state within %s", serviceName, timeout),
			)
		} else if errors.As(err, &failureErr) {
			resp.Diagnostics.AddError(
				"Service Update Failed",
				fmt.Sprintf("ECS service %s deployment failed: %s", serviceName, err),
			)
		} else if errors.As(err, &unexpectedErr) {
			resp.Diagnostics.AddError(
				"Unexpected Service State",
				fmt.Sprintf("ECS service %s entered unexpected state: %s", serviceName, err),
			)
		} else {
			resp.Diagnostics.AddError(
				"Error Waiting for Service Update",
				fmt.Sprintf("Error while waiting for ECS service %s: %s", serviceName, err),
			)
		}
		return
	}

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("ECS service %s has been successfully updated and is stable", serviceName),
	})

	tflog.Info(ctx, "ECS update service action completed successfully", map[string]any{
		"cluster_name": clusterName,
		"service_name": serviceName,
	})
}

// Helper functions

func findServiceByName(ctx context.Context, conn *ecs.Client, cluster, service string) (*awstypes.Service, error) {
	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{service},
	}

	output, err := conn.DescribeServices(ctx, input)
	if err != nil {
		return nil, err
	}

	if len(output.Services) == 0 {
		return nil, fmt.Errorf("service not found")
	}

	return &output.Services[0], nil
}

func hasActiveDeployment(service *awstypes.Service) bool {
	for _, deployment := range service.Deployments {
		if aws.ToString(deployment.Status) == "PRIMARY" && deployment.RolloutState == "IN_PROGRESS" {
			return true
		}
	}
	return false
}

func isServiceStable(service *awstypes.Service) bool {
	for _, deployment := range service.Deployments {
		if aws.ToString(deployment.Status) == "PRIMARY" {
			return deployment.RolloutState == "COMPLETED" &&
				deployment.RunningCount == deployment.DesiredCount
		}
	}
	return false
}

func getDeploymentStatus(service *awstypes.Service) (string, int32, int32, int32) {
	for _, deployment := range service.Deployments {
		if aws.ToString(deployment.Status) == "PRIMARY" {
			return string(deployment.RolloutState),
				deployment.RunningCount,
				deployment.PendingCount,
				deployment.DesiredCount
		}
	}
	return "UNKNOWN", 0, 0, 0
}

func waitForServiceStable(ctx context.Context, conn *ecs.Client, cluster, service string, timeout time.Duration) (*awstypes.Service, error) {
	result, err := actionwait.WaitForStatus(ctx, func(ctx context.Context) (actionwait.FetchResult[*awstypes.Service], error) {
		svc, err := findServiceByName(ctx, conn, cluster, service)
		if err != nil {
			return actionwait.FetchResult[*awstypes.Service]{}, fmt.Errorf("describing service: %w", err)
		}

		rolloutState, _, _, _ := getDeploymentStatus(svc)
		
		var status actionwait.Status
		if isServiceStable(svc) {
			status = "STABLE"
		} else if rolloutState == "FAILED" {
			status = "FAILED"
		} else {
			status = "UPDATING"
		}

		return actionwait.FetchResult[*awstypes.Service]{
			Status: status,
			Value:  svc,
		}, nil
	}, actionwait.Options[*awstypes.Service]{
		Timeout:          timeout,
		Interval:         actionwait.FixedInterval(updateServicePollInterval),
		ProgressInterval: 30 * time.Second,
		SuccessStates:    []actionwait.Status{"STABLE"},
		TransitionalStates: []actionwait.Status{"UPDATING"},
		FailureStates:    []actionwait.Status{"FAILED"},
	})
	
	if err != nil {
		return nil, err
	}
	
	return result.Value, nil
}
