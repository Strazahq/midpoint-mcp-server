package midpoint

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RequestForm contains only the configured, supported assignment extension items.
type RequestForm struct {
	Items []FormItem `json:"items"`
}

// FormItem describes one single-valued request field, in configuration order.
type FormItem struct {
	Name          string `json:"name"`
	QName         string `json:"qname"`
	DisplayName   string `json:"displayName,omitempty"`
	Help          string `json:"help,omitempty"`
	Type          string `json:"type"`
	Required      bool   `json:"required"`
	Multiline     bool   `json:"multiline"`
	Justification bool   `json:"justification"`
}

// RequestForm returns the startup schema snapshot, or nil without a form.
func (c *Client) RequestForm() *RequestForm { return c.requestForm }

const xsdNS = "http://www.w3.org/2001/XMLSchema"
const annotationNS = "http://prism.evolveum.com/xml/ns/public/annotation-3"
const commonNS = "http://midpoint.evolveum.com/xml/ns/public/common/common-3"

// schemaNode preserves namespace bindings for QName-valued XSD attributes.
type schemaNode struct {
	name     xml.Name
	attrs    []xml.Attr
	ns       map[string]string
	text     string
	children []*schemaNode
}

func (n *schemaNode) attr(key string) string {
	for _, a := range n.attrs {
		if a.Name.Local == key && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}
func (n *schemaNode) descendants(ns, local string) []*schemaNode {
	var out []*schemaNode
	for _, ch := range n.children {
		if ch.name.Space == ns && ch.name.Local == local {
			out = append(out, ch)
		}
		out = append(out, ch.descendants(ns, local)...)
	}
	return out
}
func (n *schemaNode) qname(s string) xml.Name {
	p, l, ok := strings.Cut(s, ":")
	if !ok {
		return xml.Name{Space: n.ns[""], Local: s}
	}
	return xml.Name{Space: n.ns[p], Local: l}
}
func readSchemaXML(data []byte) (*schemaNode, error) {
	root := &schemaNode{ns: map[string]string{}}
	stack := []*schemaNode{root}
	d := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			if len(root.children) != 1 || strings.TrimSpace(root.text) != "" {
				return nil, fmt.Errorf("invalid schema XML document")
			}
			return root, nil
		}
		if err != nil {
			return nil, fmt.Errorf("invalid schema XML")
		}
		top := stack[len(stack)-1]
		switch v := tok.(type) {
		case xml.StartElement:
			if len(stack) > 100 {
				return nil, fmt.Errorf("schema XML nesting exceeds limit")
			}
			n := &schemaNode{name: v.Name, attrs: v.Attr, ns: map[string]string{}}
			for k, x := range top.ns {
				n.ns[k] = x
			}
			for _, a := range v.Attr {
				if a.Name.Space == "xmlns" {
					n.ns[a.Name.Local] = a.Value
				} else if a.Name.Local == "xmlns" {
					n.ns[""] = a.Value
				}
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.text += string(v)
		}
	}
}

// schemaRead reads only fixed midPoint schema endpoints as the server account.
// It never carries the request principal or follows redirects with credentials.
func (c *Client) schemaRead(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building schema read: %w", withoutURL(err))
	}
	req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	req.Header.Set("Accept", "application/xml")
	hc := *c.http
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading schema %s: %w", path, withoutURL(err))
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &StatusError{Path: path, StatusCode: res.StatusCode, Status: res.Status}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading schema response")
	}
	if len(b) > maxResponseBytes {
		return nil, fmt.Errorf("schema response exceeds size limit")
	}
	return b, nil
}

// LoadRequestForm reads database and file extension schemas once, before serving.
// Unsupported or absent configured fields are skipped with a warning. Failure
// to read a schema source fails startup instead of bypassing required fields.
func (c *Client) LoadRequestForm(ctx context.Context, warn func(string)) error {
	if len(c.cfg.File.Requests.FormItems) == 0 {
		return nil
	}
	if err := c.cfg.File.validate(); err != nil {
		return err
	}
	if warn == nil {
		warn = func(string) {}
	}
	db, err := c.schemaRead(ctx, "/ws/rest/schemas")
	if err != nil {
		return err
	}
	root, err := readSchemaXML(db)
	if err != nil {
		return err
	}
	sources := []*schemaNode{root}
	files, err := c.schemaRead(ctx, "/ws/schema")
	if err != nil {
		return err
	}
	fileRoot, err := readSchemaXML(files)
	if err != nil {
		return err
	}
	for _, f := range fileRoot.descendants(commonNS, "fileName") {
		name := strings.TrimSpace(f.text)
		if name == "" || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(strings.ToLower(name), ".xsd") {
			return fmt.Errorf("invalid schema file name")
		}
		b, err := c.schemaRead(ctx, "/ws/schema/"+url.PathEscape(name))
		if err != nil {
			return err
		}
		r, err := readSchemaXML(b)
		if err != nil {
			return err
		}
		sources = append(sources, r)
	}
	available := map[string]FormItem{}
	for _, src := range sources {
		for _, schema := range src.descendants(xsdNS, "schema") {
			ns := schema.attr("targetNamespace")
			for _, ct := range schema.children {
				if ct.name != (xml.Name{Space: xsdNS, Local: "complexType"}) {
					continue
				}
				assignment := false
				for _, ext := range ct.descendants(annotationNS, "extension") {
					if ext.qname(ext.attr("ref")) == (xml.Name{Space: commonNS, Local: "AssignmentType"}) {
						assignment = true
					}
				}
				if !assignment {
					continue
				}
				for _, seq := range ct.children {
					if seq.name != (xml.Name{Space: xsdNS, Local: "sequence"}) && seq.name != (xml.Name{Space: xsdNS, Local: "all"}) {
						continue
					}
					for _, el := range seq.children {
						if el.name != (xml.Name{Space: xsdNS, Local: "element"}) {
							continue
						}
						typ := el.qname(el.attr("type"))
						max := el.attr("maxOccurs")
						if typ.Space != xsdNS || (max != "" && max != "1") || (seq.attr("maxOccurs") != "" && seq.attr("maxOccurs") != "1") {
							continue
						}
						switch typ.Local {
						case "string", "boolean", "int", "date", "dateTime":
						default:
							continue
						}
						local := el.attr("name")
						if !validLocalName(local) || ns == "" {
							continue
						}
						q := "{" + ns + "}" + local
						item := FormItem{Name: local, QName: q, Type: typ.Local, Required: el.attr("minOccurs") != "0" && seq.attr("minOccurs") != "0", Justification: q == c.cfg.File.Requests.JustificationItem}
						item.Multiline = item.Justification
						for _, n := range el.descendants(annotationNS, "displayName") {
							item.DisplayName = strings.TrimSpace(n.text)
						}
						for _, n := range el.descendants(annotationNS, "help") {
							item.Help = strings.TrimSpace(n.text)
						}
						if item.Help == "" {
							for _, n := range el.descendants(xsdNS, "documentation") {
								item.Help = strings.TrimSpace(n.text)
							}
						}
						available[q] = item
					}
				}
			}
		}
	}
	form := &RequestForm{Items: []FormItem{}}
	for _, q := range c.cfg.File.Requests.FormItems {
		item, ok := available[q]
		if !ok {
			warn(fmt.Sprintf("requests.formItems: skipping %s (not found, unsupported type or multi-valued)", q))
			continue
		}
		form.Items = append(form.Items, item)
	}
	if len(form.Items) > 0 {
		c.requestForm = form
	}
	return nil
}

func fieldError(name, reason string) error {
	return &CodedError{Code: CodeInvalidField, Field: name, Err: fmt.Errorf("invalid request field %s: %s", name, reason)}
}

// ValidateRequestFields returns the nonempty, typed values and their qualified
// extension keys. No field outside the configured schema is accepted.
func (c *Client) ValidateRequestFields(fields map[string]any) (map[string]any, map[string]any, error) {
	items := map[string]FormItem{}
	if c.requestForm != nil {
		for _, i := range c.requestForm.Items {
			items[i.Name] = i
		}
	}
	for k := range fields {
		if _, ok := items[k]; !ok {
			return nil, nil, fieldError(k, "not offered in the request form")
		}
	}
	clean, ext := map[string]any{}, map[string]any{}
	if c.requestForm == nil {
		return clean, ext, nil
	}
	for _, i := range c.requestForm.Items {
		v, ok := fields[i.Name]
		s, isString := v.(string)
		if !ok || v == nil || (isString && strings.TrimSpace(s) == "") {
			if i.Required {
				return nil, nil, fieldError(i.Name, "required")
			}
			continue
		}
		valid := false
		switch i.Type {
		case "string":
			valid = isString
		case "boolean":
			_, valid = v.(bool)
		case "int":
			switch n := v.(type) {
			case float64:
				valid = !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) && n >= math.MinInt32 && n <= math.MaxInt32
			case int:
				valid = int64(n) >= math.MinInt32 && int64(n) <= math.MaxInt32
			}
		case "date":
			_, err := time.Parse("2006-01-02", s)
			valid = isString && err == nil
		case "dateTime":
			_, err := time.Parse(time.RFC3339, s)
			valid = isString && err == nil
		}
		if !valid {
			return nil, nil, fieldError(i.Name, "expected "+i.Type)
		}
		clean[i.Name] = v
		// Fully qualified property names keep fields from different extension
		// namespaces distinct (Prism's namespace#local JSON spelling).
		q, _ := parseQName(i.QName)
		ext[q.Namespace+"#"+q.Local] = v
	}
	return clean, ext, nil
}

// ValidateRequestValidity checks dates in their supplied offset. The caller's
// zone is not a tool argument, so today's boundary is in validFrom's offset.
func ValidateRequestValidity(from, to string, now time.Time) error {
	fail := func(s string) error {
		return &CodedError{Code: CodeInvalidValidity, Err: fmt.Errorf("invalid validity: %s", s)}
	}
	start := now
	if from != "" {
		v, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return fail("validFrom must be RFC 3339 with offset")
		}
		today := now.In(v.Location())
		y, m, d := today.Date()
		if v.Before(time.Date(y, m, d, 0, 0, 0, 0, v.Location())) {
			return fail("start must not be before today")
		}
		start = v
	}
	if to != "" {
		end, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return fail("validTo must be RFC 3339 with offset")
		}
		if !end.After(start) {
			return fail("end must be after start")
		}
		if !end.After(now) {
			return fail("end must be in the future")
		}
	}
	return nil
}

// PlanRequestRoleWithValues validates and adds activation and extension values
// to the same single assignment-add PATCH used by PlanRequestRole.
func (c *Client) PlanRequestRoleWithValues(user, role, from, to string, fields map[string]any) (Plan, map[string]any, error) {
	if err := ValidateRequestValidity(from, to, time.Now()); err != nil {
		return Plan{}, nil, err
	}
	clean, ext, err := c.ValidateRequestFields(fields)
	if err != nil {
		return Plan{}, nil, err
	}
	p, err := c.PlanRequestRole(user, role)
	if err != nil {
		return Plan{}, nil, err
	}
	value := map[string]any{"targetRef": map[string]any{"oid": role, "type": "RoleType"}}
	if from != "" || to != "" {
		a := map[string]string{}
		if from != "" {
			a["validFrom"] = from
		}
		if to != "" {
			a["validTo"] = to
		}
		value["activation"] = a
	}
	if len(ext) > 0 {
		value["extension"] = ext
	}
	p.Body = modifyBody(itemDelta{ModificationType: "add", Path: "assignment", Value: value})
	return p, clean, nil
}
