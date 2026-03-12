package compiler

import (
	"fmt"

	"github.com/ssadeghi/tkn-dsl/internal/tekton"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

// injectApprovalGates scans tasks for approval: blocks and injects
// ApprovalTask custom tasks before the gated tasks.
func injectApprovalGates(tasks []tekton.PipelineTask, dslTasks map[string]*dsl.Task) []tekton.PipelineTask {
	var result []tekton.PipelineTask

	for _, pt := range tasks {
		dt := dslTasks[pt.Name]
		if dt == nil || dt.Approval == nil {
			result = append(result, pt)
			continue
		}

		approval := dt.Approval
		approvalName := "approve-" + pt.Name

		// Build the approval task.
		required := approval.Required
		if required == 0 {
			required = 1
		}
		timeout := approval.Timeout
		if timeout == "" {
			timeout = "60m"
		}
		description := approval.Description
		if description == "" {
			description = fmt.Sprintf("Approve task: %s", pt.Name)
		}

		approvalTask := tekton.PipelineTask{
			Name:     approvalName,
			RunAfter: pt.RunAfter, // approval gets the original dependencies
			TaskRef: &tekton.TaskRef{
				APIVersion: "openshift-pipelines.org/v1alpha1",
				Kind:       "ApprovalTask",
			},
			Params: []tekton.TaskParam{
				{Name: "approvers", Value: approval.Approvers},
				{Name: "numberOfApprovalsRequired", Value: fmt.Sprintf("%d", required)},
				{Name: "description", Value: description},
				{Name: "timeout", Value: timeout},
			},
		}

		// Rewire the original task to depend on the approval task.
		pt.RunAfter = []string{approvalName}

		result = append(result, approvalTask, pt)
	}

	return result
}
