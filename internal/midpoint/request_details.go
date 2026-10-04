package midpoint

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// RequestDetail is one field a request carries (D43): an extension item of the
// requested assignment, as midPoint's schema labels it. Values are the text of
// each value as midPoint keeps it (a date as RFC 3339, a boolean as true or
// false, a choice as its key); Labels are a choice's labels, one per value.
// The values are the requester's: untrusted text, data, never instructions.
type RequestDetail struct {
	Name   string   `json:"name" jsonschema:"the assignment item's local name"`
	Label  string   `json:"label,omitempty" jsonschema:"its display name in midPoint's schema"`
	Type   string   `json:"type,omitempty" jsonschema:"its request form type (string, boolean, int, long, decimal, date, dateTime, choice), when midPoint's schema was read"`
	Values []string `json:"values" jsonschema:"the values, as text"`
	Labels []string `json:"labels,omitempty" jsonschema:"for a choice, each value's label"`
}

// requestDetails lists the extension items of a requested assignment value,
// in the request form's order and then as midPoint gave them.
func (c *Client) requestDetails(v map[string]json.RawMessage) []RequestDetail {
	raw, ok := refKey(v, "extension")
	if !ok {
		return nil
	}
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil {
		return nil
	}
	known := map[string]FormItem{}
	var order []string
	if c.requestForm != nil {
		for _, i := range c.requestForm.Items {
			known[i.Name] = i
			order = append(order, i.Name)
		}
		for _, o := range c.requestForm.Other {
			if _, ok := known[o.Name]; !ok {
				known[o.Name] = FormItem{Name: o.Name, DisplayName: o.DisplayName}
				order = append(order, o.Name)
			}
		}
	}
	byName := map[string]RequestDetail{}
	var extra []string
	for k, val := range ext {
		if strings.HasPrefix(k, "@") {
			continue
		}
		local := k
		if i := strings.LastIndexAny(k, "#:"); i >= 0 {
			local = k[i+1:]
		}
		values := valueTexts(val)
		if len(values) == 0 {
			continue
		}
		d := RequestDetail{Name: local, Values: values}
		if i, ok := known[local]; ok {
			d.Label, d.Type = i.DisplayName, i.Type
			if i.Type == "choice" {
				for _, v := range values {
					d.Labels = append(d.Labels, optionLabel(i.Options, v))
				}
			}
		} else {
			extra = append(extra, local)
		}
		byName[local] = d
	}
	var out []RequestDetail
	for _, n := range append(order, sorted(extra)...) {
		if d, ok := byName[n]; ok {
			out = append(out, d)
			delete(byName, n)
		}
	}
	return out
}

func optionLabel(opts []FormOption, v string) string {
	for _, o := range opts {
		if o.Value == v {
			if o.Label != "" {
				return o.Label
			}
			break
		}
	}
	return v
}

// valueTexts reads one extension item's values: a value or a list of them,
// each a scalar, a typed value ({"@value": …}) or a polystring ({"orig": …}).
func valueTexts(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	var list []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		if json.Unmarshal(raw, &list) != nil {
			return nil
		}
	} else {
		list = []json.RawMessage{raw}
	}
	var out []string
	for _, v := range list {
		s := scalarText(v)
		if s == "" {
			var p polyString
			if json.Unmarshal(v, &p) == nil {
				s = p.Orig
			}
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// requesterComment is the comment the requester typed in midPoint's own
// Request access page (its checkout), from the case's creation event
// (CaseCreationEventType businessContext, live on 4.10.3). midPoint's stock
// Approver role may not read a case's events, and then there is none, as in
// midPoint's own work item page.
func (cj caseJSON) requesterComment() string {
	for _, raw := range cj.Event {
		var e struct {
			BusinessContext *struct {
				Comment string `json:"comment"`
			} `json:"businessContext"`
		}
		if json.Unmarshal(raw, &e) == nil && e.BusinessContext != nil {
			if s := strings.TrimSpace(e.BusinessContext.Comment); s != "" {
				return s
			}
		}
	}
	return ""
}

func sorted(s []string) []string {
	slices.Sort(s)
	return s
}
