package main

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strazahq/midpoint-mcp-server/internal/midpoint"
)

// registerHealthTools installs list_recent_errors, a read-only tool outside
// the write gate. It runs on the caller's own midPoint rights.
func registerHealthTools(server *mcp.Server, client *midpoint.Client) {
	addTool(server, &mcp.Tool{
		Name:  "list_recent_errors",
		Title: "List recent errors",
		Description: "Answer \"what failed in the last hours?\" from five sources, each checked on its own: " +
			"tasks whose last run in the window ended in a fatal or partial error; accounts and entitlements " +
			"with a failed operation in the window (midPoint keeps only the last few operation records per " +
			"object, so older failures can be gone); dead accounts and accounts with pending operations, now; " +
			"systems (resources) that are not up, now; and audit records of failed executions in the window, " +
			"which need script access and are skipped when midPoint refuses it. A source midPoint refuses shows " +
			"as refused in its own section and the others still answer. Runs on the caller's own midPoint " +
			"rights, so each person sees only what midPoint lets them see. At most 20 systems are searched. " +
			untrustedTextNote,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recentErrorsInput) (*mcp.CallToolResult, recentErrorsOutput, error) {
		res := client.RecentErrors(ctx, midpoint.RecentErrorsQuery{Hours: in.Hours, Limit: in.Limit})
		summary := recentErrorsSummary(res)
		return text(recentErrorsText(summary, res)), recentErrorsOutput{Summary: summary, RecentErrors: res}, nil
	})
}

type recentErrorsInput struct {
	Hours int `json:"hours,omitempty" jsonschema:"how far back to look, in hours; default 24, max 168"`
	Limit int `json:"limit,omitempty" jsonschema:"items per section; default 10, max 50"`
}

type recentErrorsOutput struct {
	Summary string `json:"summary" jsonschema:"the first line of the text"`
	midpoint.RecentErrors
}

// --- summary (line 1) ---

// recentErrorsSummary is line 1: the window's counts, then the state now.
func recentErrorsSummary(r midpoint.RecentErrors) string {
	kinds := failedKinds(r.FailedAccounts.Items)
	accounts := countPhrase(r.FailedAccounts.SectionHead, len(r.FailedAccounts.Items),
		kindNoun(kinds, false)+" with failed operations", kindNoun(kinds, true)+" with failed operations",
		"accounts with failed operations")
	now := []string{brokenPhrase(r.BrokenAccounts), systemsPhrase(r.Systems)}
	if r.Systems.Status == midpoint.SectionOK && r.Systems.Total == 0 {
		// Accounts are searched system by system: none visible, none checked.
		accounts = "no accounts checked"
		now = []string{systemsPhrase(r.Systems)}
	}
	window := []string{
		countPhrase(r.FailedTasks.SectionHead, len(r.FailedTasks.Items), "failed task", "failed tasks", "failed tasks"),
		accounts,
	}
	return fmt.Sprintf("In the last %d h: %s; audit: %s. Now: %s.",
		r.Hours, strings.Join(window, ", "), auditPhrase(r.Audit), strings.Join(now, ", "))
}

// countPhrase is "3 failed tasks", "more than 10 failed tasks", or what kept
// the section from answering.
func countPhrase(h midpoint.SectionHead, n int, one, many, topic string) string {
	if p := notOK(h); p != "" {
		return topic + ": " + p
	}
	noun := many
	if n == 1 {
		noun = one
	}
	if h.More {
		return fmt.Sprintf("more than %d %s", n, many)
	}
	if n == 0 {
		return "no " + many
	}
	return fmt.Sprintf("%d %s", n, noun)
}

// notOK says why a section did not answer, or "" when it did.
func notOK(h midpoint.SectionHead) string {
	switch h.Status {
	case midpoint.SectionRefused:
		return "not allowed for you"
	case midpoint.SectionFailed:
		return "could not check"
	case midpoint.SectionSkipped:
		return "skipped (needs script access)"
	}
	return ""
}

func brokenPhrase(b midpoint.BrokenAccountsSection) string {
	if p := notOK(b.SectionHead); p != "" {
		return "broken accounts: " + p
	}
	if b.Dead == 0 && b.Pending == 0 {
		return "no dead accounts or pending operations"
	}
	kinds := brokenKinds(b.Items)
	more := ""
	if b.More {
		more = "at least "
	}
	var parts []string
	if b.Dead > 0 {
		parts = append(parts, fmt.Sprintf("%s%d dead %s", more, b.Dead, kindNoun(kinds, b.Dead != 1)))
	}
	if b.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%s%d %s with pending operations", more, b.Pending, kindNoun(kinds, b.Pending != 1)))
	}
	return strings.Join(parts, ", ")
}

func systemsPhrase(s midpoint.SystemsSection) string {
	if p := notOK(s.SectionHead); p != "" {
		return "systems: " + p
	}
	if s.Total == 0 {
		return "no systems visible to you"
	}
	var out string
	switch {
	case len(s.Items) > 0:
		out = fmt.Sprintf("%d of %d systems not up", len(s.Items), s.Total)
	case s.Untested == s.Total:
		out = fmt.Sprintf("%d systems", s.Total)
		if s.Total == 1 {
			out = "1 system"
		}
	case s.Total == 1:
		out = "the 1 system is up"
	case s.Untested > 0:
		out = fmt.Sprintf("%d of %d systems up", s.Up, s.Total)
	default:
		out = fmt.Sprintf("all %d systems up", s.Total)
	}
	if s.Untested > 0 {
		out += fmt.Sprintf(" (%d not tested yet)", s.Untested)
	}
	if s.Capped {
		out += " (only the first 20 checked)"
	}
	return out
}

func auditPhrase(a midpoint.AuditErrorsSection) string {
	if p := notOK(a.SectionHead); p != "" {
		return p
	}
	return countPhrase(a.SectionHead, len(a.Items), "error", "errors", "")
}

// kindNoun names a set of shadow kinds in plain words.
func kindNoun(kinds map[string]bool, plural bool) string {
	s := ""
	if plural {
		s = "s"
	}
	switch {
	case kinds["entitlement"] && !kinds["account"]:
		return "entitlement" + s
	case kinds["entitlement"]:
		return "account" + s + " or entitlement" + s
	}
	return "account" + s
}

func failedKinds(items []midpoint.FailedAccount) map[string]bool {
	k := map[string]bool{}
	for _, a := range items {
		k[a.Kind] = true
	}
	return k
}

func brokenKinds(items []midpoint.BrokenAccount) map[string]bool {
	k := map[string]bool{}
	for _, a := range items {
		k[a.Kind] = true
	}
	return k
}

// --- the text ---

// recentErrorsText is the tool's text: line 1, then a group line per section
// (written even when empty) with the section's items under it.
func recentErrorsText(summary string, r midpoint.RecentErrors) string {
	t := newListText(summary)

	t.group(groupLine("Failed tasks", r.FailedTasks.SectionHead))
	for _, task := range r.FailedTasks.Items {
		t.item(task.Name,
			textField{"status", task.Status},
			textField{"state", task.State},
			textField{"finished", task.Finished},
			textField{"owner", task.OwnerName},
			textField{"oid", task.OID},
			textField{"ownerOid", task.OwnerOID},
		)
		t.untrusted(fieldMessage, fromObjectRecord, task.Message)
	}

	t.group(groupLine("Accounts with failed operations", r.FailedAccounts.SectionHead))
	for _, a := range r.FailedAccounts.Items {
		t.item(a.Name,
			textField{"kind", a.Kind},
			textField{"resource", a.ResourceName},
			textField{"owner", a.OwnerName},
			textField{"status", a.Status},
			textField{"time", a.Time},
			textField{"change", a.Change},
			textField{"oid", a.OID},
			textField{"resourceOid", a.ResourceOID},
			textField{"ownerOid", a.OwnerOID},
		)
		t.untrusted(fieldMessage, fromObjectRecord, a.Message)
	}

	t.group(groupLine("Broken accounts now", r.BrokenAccounts.SectionHead))
	for _, a := range r.BrokenAccounts.Items {
		dead := ""
		if a.Dead {
			dead = "yes"
		}
		pending := ""
		if a.PendingOps > 0 {
			pending = fmt.Sprint(a.PendingOps)
		}
		t.item(a.Name,
			textField{"kind", a.Kind},
			textField{"resource", a.ResourceName},
			textField{"dead", dead},
			textField{"died", a.Died},
			textField{"pending", pending},
			textField{"pendingStatus", a.PendingStatus},
			textField{"pendingSince", a.PendingSince},
			textField{"oid", a.OID},
			textField{"resourceOid", a.ResourceOID},
		)
	}

	t.group(groupLine("Systems not up now", r.Systems.SectionHead))
	for _, s := range r.Systems.Items {
		t.item(s.Name,
			textField{"status", s.Status},
			textField{"since", s.Since},
			textField{"oid", s.OID},
		)
		t.untrusted(fieldMessage, fromResourceRecord, s.Message)
	}

	t.group(groupLine("Audit errors", r.Audit.SectionHead))
	for _, a := range r.Audit.Items {
		// The target names the item; a record without one is named by its
		// event, which is then not repeated.
		event := a.EventType
		if a.Target == "" {
			event = ""
		}
		t.item(cmp.Or(a.Target, a.EventType),
			textField{"event", event},
			textField{"outcome", a.Outcome},
			textField{"time", a.Timestamp},
			textField{"initiator", a.Initiator},
			textField{"channel", shortChannel(a.Channel)},
		)
		t.untrusted(fieldMessage, fromAuditRecord, a.Message)
	}
	return t.String()
}

// groupLine is a section's group line: its title, and why it did not answer
// or that some of its searches failed.
func groupLine(title string, h midpoint.SectionHead) string {
	if h.Status == midpoint.SectionSkipped {
		return title + " (skipped, needs script access):"
	}
	if p := notOK(h); p != "" {
		return title + " (" + p + "):"
	}
	if len(h.Notes) > 0 && strings.Contains(strings.Join(h.Notes, "\n"), "search failed") {
		return title + " (some searches failed):"
	}
	return title + ":"
}

// shortChannel drops the namespace of a channel URI ("…#rest" is "rest").
func shortChannel(ch string) string {
	if i := strings.LastIndex(ch, "#"); i >= 0 {
		return ch[i+1:]
	}
	return ch
}
