package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

type cancelRequestInput struct {
	CaseOID  string `json:"caseOid" jsonschema:"OID of your open request to withdraw"`
	UserName string `json:"userName" jsonschema:"midPoint name (login) of the person the request is for; empty only when midPoint doesn't show you their name"`
	RoleName string `json:"roleName" jsonschema:"midPoint name of the role (or other object) the request adds or removes; empty only when midPoint doesn't show you its name"`
}

type withdrawal struct {
	Target    *midpoint.ObjectRef `json:"target,omitempty"`
	Object    *midpoint.ObjectRef `json:"object,omitempty"`
	Outcome   string              `json:"outcome" jsonschema:"withdrawn when read back closing or closed, unconfirmed otherwise, preview when writes are disabled"`
	CaseState string              `json:"caseState,omitempty"`
}

type cancelRequestOutput struct {
	viewFields
	writeOutput
	Subject    midpoint.Subject `json:"subject"`
	CaseOID    string           `json:"caseOid"`
	Case       string           `json:"case,omitempty"`
	Withdrawal withdrawal       `json:"withdrawal"`
}

func registerCancelRequest(server *mcp.Server, client *midpoint.Client, allowWrites bool, info serverInfo) {
	addTool(server, &mcp.Tool{Name: "cancel_request", Title: "Withdraw request", Description: "Withdraw your own open approval request. Reads the case and checks its state and requester before writing, even with writes disabled. midPoint enforces cancel authorization. Uses plain REST with no comment; reads back the case to confirm closure. " + nameArgsNote + " Respects the write gate."},
		viewTool("cancel_request", client, info, func(ctx context.Context, _ *mcp.CallToolRequest, in cancelRequestInput) (*mcp.CallToolResult, cancelRequestOutput, error) {
			d, err := client.CheckWithdrawable(ctx, in.CaseOID)
			if err != nil {
				return nil, cancelRequestOutput{}, err
			}
			if err := confirmRef("userName", "person", in.UserName, d.Case.ObjectRef); err != nil {
				return nil, cancelRequestOutput{}, err
			}
			if err := confirmRef("roleName", "role", in.RoleName, d.Case.TargetRef); err != nil {
				return nil, cancelRequestOutput{}, err
			}
			plan, err := client.PlanCancelRequest(in.CaseOID)
			if err != nil {
				return nil, cancelRequestOutput{}, err
			}
			out := cancelRequestOutput{Subject: d.Subject, CaseOID: in.CaseOID, Case: d.Case.Name,
				Withdrawal: withdrawal{Target: d.Case.TargetRef, Object: d.Case.ObjectRef, Outcome: "preview"}}
			label := fmt.Sprintf("%q (%s)", d.Case.Name, in.CaseOID)
			if !allowWrites {
				_, out.writeOutput = previewWrite(plan)
				return text(fmt.Sprintf("DRY RUN — writes disabled. Would withdraw request %s as %s (%s mode) via %s %s.\nSet %s=true to apply.", label, d.Subject.Name, d.Subject.Mode, plan.Method, plan.Endpoint(), midpoint.EnvAllowWrites)), out, nil
			}
			applied, err := client.Apply(ctx, plan)
			if err != nil {
				return nil, cancelRequestOutput{}, err
			}
			out.writeOutput = writeOutput{Applied: true, Summary: plan.Summary, Method: plan.Method, Endpoint: plan.Endpoint(), Result: fmt.Sprintf("status=%d", applied.StatusCode)}
			out.Withdrawal.Outcome = "unconfirmed"
			after, err := client.GetCase(ctx, in.CaseOID)
			if err != nil {
				return text(fmt.Sprintf("Sent the withdrawal of request %s, but the case could not be re-read to confirm its state.", label)), out, nil
			}
			out.Withdrawal.CaseState = after.State
			if after.State == "closing" || after.State == "closed" {
				out.Withdrawal.Outcome = "withdrawn"
				return text(fmt.Sprintf("Withdrew request %s as %s (%s mode): midPoint now shows it as %s.", label, d.Subject.Name, d.Subject.Mode, after.State)), out, nil
			}
			return text(fmt.Sprintf("Sent the withdrawal of request %s, but midPoint still shows it as %s.", label, caseStateText(after.State))), out, nil
		}))
}

// requestsText preserves the list's first line and names every returned case.
func requestsText(line1 string, requests []midpoint.RequestSummary) string {
	t := newListText(line1)
	for _, c := range requests {
		var target, object midpoint.ObjectRef
		var validity midpoint.Validity
		if c.TargetRef != nil {
			target = *c.TargetRef
		}
		if c.ObjectRef != nil {
			object = *c.ObjectRef
		}
		if c.Validity != nil {
			validity = *c.Validity
		}
		name := c.Name
		if name == "" {
			name = c.OID
		}
		t.item(name, textField{"case", c.OID}, textField{"state", c.State}, textField{"outcome", c.Outcome},
			textField{"target", c.Target}, textField{"targetOid", target.OID}, textField{"for", c.Object}, textField{"forOid", object.OID},
			textField{"requested", c.RequestedAt}, textField{"validFrom", validity.ValidFrom}, textField{"validTo", validity.ValidTo}, textField{"waitingFor", refNames(c.WaitingFor)})
	}
	return t.String()
}
