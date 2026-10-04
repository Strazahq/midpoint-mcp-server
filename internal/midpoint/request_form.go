package midpoint

import (
	"cmp"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RequestForm is the request's fields: the assignment extension items of
// midPoint's schema (D43), in midPoint's display order. midPoint decides on
// Send who must or may fill what (D42); the form only offers the inputs.
type RequestForm struct {
	Items []FormItem `json:"items"`
	// Other names the assignment items a request can't fill here (a
	// reference or a structured value); midPoint's own page can.
	Other []FormOther `json:"other,omitempty"`
}

// FormItem is one request field.
type FormItem struct {
	Name        string `json:"name"`
	QName       string `json:"qname"`
	DisplayName string `json:"displayName,omitempty"`
	Help        string `json:"help,omitempty"`
	// Type is string, boolean, int (32 bits), long (whole numbers a view can
	// hold exactly), decimal, date, dateTime or choice.
	Type     string       `json:"type"`
	Required bool         `json:"required"`
	Multiple bool         `json:"multiple"`
	Options  []FormOption `json:"options,omitempty"`

	xsdType string // the XSD type the values are checked against
	order   int    // a:displayOrder, 0 when unset
}

// FormOption is one value of a choice: an enumeration's value or a lookup
// table row's key, with its label.
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// FormOther is an assignment item the form can't offer.
type FormOther struct {
	Name        string `json:"name"`
	QName       string `json:"qname"`
	DisplayName string `json:"displayName,omitempty"`
}

// RequestForm returns the startup schema snapshot, or nil when midPoint's
// schema has no assignment fields or could not be read.
func (c *Client) RequestForm() *RequestForm { return c.requestForm }

const typesNS = "http://prism.evolveum.com/xml/ns/public/types-3"

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

// LoadRequestForm reads midPoint's assignment extension schema once, before
// serving (D43): database-stored schemas and file-based ones, as the server's
// own account. Every assignment item becomes a request field, except those
// hidden from people (a:displayHint hidden, operational, ignored, or not
// creatable). An item the form can't fill is named in Other. A schema that
// can't be read leaves the form empty, with a warning: midPoint still decides
// on Send, and a deployment whose account may not read schemas starts.
func (c *Client) LoadRequestForm(ctx context.Context, warn func(string)) error {
	if warn == nil {
		warn = func(string) {}
	}
	if len(c.cfg.File.Requests.FormItems) > 0 || c.cfg.File.Requests.JustificationItem != "" {
		warn("requests.formItems and requests.justificationItem are no longer used: request fields come from midPoint's assignment schema (D43)")
	}
	sources, err := c.schemaSources(ctx)
	if err != nil {
		warn(fmt.Sprintf("request fields: midPoint's schema could not be read (%v); requests are sent without fields, and midPoint decides on Send", err))
		return nil
	}
	form := &RequestForm{Items: []FormItem{}}
	seen := map[string]bool{}
	for _, src := range sources {
		for _, schema := range src.descendants(xsdNS, "schema") {
			ns := schema.attr("targetNamespace")
			if ns == "" {
				continue
			}
			for _, ct := range schema.children {
				if ct.name != (xml.Name{Space: xsdNS, Local: "complexType"}) || !extendsAssignment(ct) {
					continue
				}
				for _, el := range sequenceElements(ct) {
					local := el.attr("name")
					if !validLocalName(local) || hiddenItem(el) {
						continue
					}
					if seen[local] {
						warn(fmt.Sprintf("request fields: skipping {%s}%s, another assignment item has the same name", ns, local))
						continue
					}
					seen[local] = true
					item, ok := c.formItem(ctx, schema, el, ns, local, warn)
					if !ok {
						form.Other = append(form.Other, FormOther{Name: local, QName: item.QName, DisplayName: item.DisplayName})
						continue
					}
					form.Items = append(form.Items, item)
				}
			}
		}
	}
	sort.SliceStable(form.Items, func(i, j int) bool { return displayBefore(form.Items[i].order, form.Items[j].order) })
	if len(form.Items) > 0 || len(form.Other) > 0 {
		c.requestForm = form
	}
	return nil
}

// displayBefore orders by a:displayOrder, items without one last.
func displayBefore(a, b int) bool {
	switch {
	case a == 0:
		return false
	case b == 0:
		return true
	}
	return a < b
}

// schemaSources reads the database-stored schemas and every file-based one.
func (c *Client) schemaSources(ctx context.Context) ([]*schemaNode, error) {
	db, err := c.schemaRead(ctx, "/ws/rest/schemas")
	if err != nil {
		return nil, err
	}
	root, err := readSchemaXML(db)
	if err != nil {
		return nil, err
	}
	sources := []*schemaNode{root}
	files, err := c.schemaRead(ctx, "/ws/schema")
	if err != nil {
		return nil, err
	}
	fileRoot, err := readSchemaXML(files)
	if err != nil {
		return nil, err
	}
	for _, f := range fileRoot.descendants(commonNS, "fileName") {
		name := strings.TrimSpace(f.text)
		if name == "" || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(strings.ToLower(name), ".xsd") {
			return nil, fmt.Errorf("invalid schema file name")
		}
		b, err := c.schemaRead(ctx, "/ws/schema/"+url.PathEscape(name))
		if err != nil {
			return nil, err
		}
		r, err := readSchemaXML(b)
		if err != nil {
			return nil, err
		}
		sources = append(sources, r)
	}
	return sources, nil
}

func extendsAssignment(ct *schemaNode) bool {
	for _, ext := range ct.descendants(annotationNS, "extension") {
		if ext.qname(ext.attr("ref")) == (xml.Name{Space: commonNS, Local: "AssignmentType"}) {
			return true
		}
	}
	return false
}

// sequenceElements are a complex type's elements, of its sequence or all.
func sequenceElements(ct *schemaNode) []*schemaNode {
	var out []*schemaNode
	for _, seq := range ct.children {
		if seq.name != (xml.Name{Space: xsdNS, Local: "sequence"}) && seq.name != (xml.Name{Space: xsdNS, Local: "all"}) {
			continue
		}
		for _, el := range seq.children {
			if el.name == (xml.Name{Space: xsdNS, Local: "element"}) {
				out = append(out, el)
			}
		}
	}
	return out
}

// annotation returns the trimmed text of an element's a:<local>, or "".
func annotation(el *schemaNode, local string) string {
	for _, n := range el.descendants(annotationNS, local) {
		if t := strings.TrimSpace(n.text); t != "" {
			return t
		}
	}
	return ""
}

// hiddenItem reports an item midPoint keeps from people: hidden, operational,
// ignored, or one a new assignment can't carry (a:access without create).
func hiddenItem(el *schemaNode) bool {
	if strings.EqualFold(annotation(el, "displayHint"), "hidden") || annotation(el, "operational") == "true" ||
		annotation(el, "ignore") == "true" || strings.EqualFold(annotation(el, "processing"), "ignore") {
		return true
	}
	access := el.descendants(annotationNS, "access")
	if len(access) == 0 {
		return false
	}
	for _, a := range access {
		switch strings.TrimSpace(a.text) {
		case "create", "add":
			return false
		}
	}
	return true
}

// formItem describes one assignment item. ok is false for an item the form
// can't fill; the item still carries its names.
func (c *Client) formItem(ctx context.Context, schema, el *schemaNode, ns, local string, warn func(string)) (FormItem, bool) {
	item := FormItem{Name: local, QName: "{" + ns + "}" + local, DisplayName: annotation(el, "displayName"), Help: annotation(el, "help")}
	if item.Help == "" {
		for _, n := range el.descendants(xsdNS, "documentation") {
			item.Help = strings.TrimSpace(n.text)
		}
	}
	if o, err := strconv.Atoi(annotation(el, "displayOrder")); err == nil && o > 0 {
		item.order = o
	}
	item.Required = el.attr("minOccurs") != "0"
	max := el.attr("maxOccurs")
	item.Multiple = max == "unbounded" || (max != "" && max != "0" && max != "1")

	typ := el.qname(el.attr("type"))
	switch {
	case typ.Space == xsdNS:
		item.xsdType = typ.Local
		switch typ.Local {
		case "string", "normalizedString", "token", "anyURI":
			item.Type = "string"
		case "boolean":
			item.Type = "boolean"
		case "int", "short", "byte":
			item.Type = "int"
		case "long", "integer", "nonNegativeInteger", "positiveInteger":
			item.Type = "long"
		case "decimal", "double", "float":
			item.Type = "decimal"
		case "date", "dateTime":
			item.Type = typ.Local
		default:
			return item, false
		}
	case typ == xml.Name{Space: typesNS, Local: "PolyStringType"}:
		item.Type, item.xsdType = "string", "string"
	case typ.Space == ns:
		opts, ok := enumerationOptions(schema, typ.Local)
		if !ok {
			return item, false
		}
		item.Type, item.xsdType, item.Options = "choice", "string", opts
	default:
		return item, false
	}
	if item.Type == "boolean" && item.Multiple {
		return item, false
	}
	for _, ref := range el.descendants(annotationNS, "valueEnumerationRef") {
		if item.Type != "string" {
			break
		}
		opts, err := c.lookupOptions(ctx, ref.attr("oid"))
		if err != nil {
			warn(fmt.Sprintf("request fields: %s offers free text, its lookup table could not be read (%v)", item.QName, err))
			break
		}
		item.Type, item.Options = "choice", opts
	}
	return item, true
}

// enumerationOptions reads a simple type of the schema that restricts a
// string to listed values; its labels are a:label, a:displayName or the
// value's documentation.
func enumerationOptions(schema *schemaNode, name string) ([]FormOption, bool) {
	for _, st := range schema.children {
		if st.name != (xml.Name{Space: xsdNS, Local: "simpleType"}) || st.attr("name") != name {
			continue
		}
		var opts []FormOption
		for _, e := range st.descendants(xsdNS, "enumeration") {
			label := annotation(e, "label")
			if label == "" {
				label = annotation(e, "displayName")
			}
			if label == "" {
				for _, d := range e.descendants(xsdNS, "documentation") {
					label = strings.TrimSpace(d.text)
				}
			}
			opts = append(opts, FormOption{Value: e.attr("value"), Label: label})
		}
		return opts, len(opts) > 0
	}
	return nil, false
}

// maxLookupRows bounds a lookup table offered as a choice.
const maxLookupRows = 500

// lookupOptions reads a lookup table's rows as the server's own account: the
// row key is the value, its label (else its value) the label.
func (c *Client) lookupOptions(ctx context.Context, oid string) ([]FormOption, error) {
	if oid == "" {
		return nil, fmt.Errorf("no lookup table OID")
	}
	body, err := c.do(ctx, http.MethodGet, "/lookupTables/"+url.PathEscape(oid), url.Values{"include": {"row"}}, nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		LookupTable struct {
			Row flexSlice `json:"row"`
		} `json:"lookupTable"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decoding lookup table: %w", err)
	}
	if len(env.LookupTable.Row) > maxLookupRows {
		return nil, fmt.Errorf("more than %d rows", maxLookupRows)
	}
	var opts []FormOption
	for _, raw := range env.LookupTable.Row {
		var row struct {
			Key   string     `json:"key"`
			Value string     `json:"value"`
			Label polyString `json:"label"`
		}
		if json.Unmarshal(raw, &row) != nil || row.Key == "" {
			continue
		}
		opts = append(opts, FormOption{Value: row.Key, Label: cmp.Or(row.Label.Orig, row.Value)})
	}
	if len(opts) == 0 {
		return nil, fmt.Errorf("no rows")
	}
	return opts, nil
}

func fieldError(name, reason string) error {
	return &CodedError{Code: CodeInvalidField, Field: name, Err: fmt.Errorf("invalid request field %s: %s", name, reason)}
}

// ValidateRequestFields returns the nonempty, typed values and their qualified
// extension keys. No field outside midPoint's assignment schema is accepted. A
// field of several values takes a list (a single value counts as a list of
// one); a required field needs a value, as midPoint's schema says (midPoint
// itself doesn't check this on the server). Who must fill what beyond that is
// midPoint's to decide (D42).
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
		var given []any
		switch v := fields[i.Name].(type) {
		case nil:
		case []any:
			if !i.Multiple {
				return nil, nil, fieldError(i.Name, "takes one value")
			}
			given = v
		default:
			given = []any{v}
		}
		var values []any
		for _, v := range given {
			if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
				continue
			}
			if v == nil {
				continue
			}
			if !i.accepts(v) {
				return nil, nil, fieldError(i.Name, "expected "+i.expected())
			}
			values = append(values, v)
		}
		if len(values) == 0 {
			if i.Required {
				return nil, nil, fieldError(i.Name, "required")
			}
			continue
		}
		var value any = values[0]
		if i.Multiple {
			value = values
		}
		clean[i.Name] = value
		// Fully qualified property names keep fields from different extension
		// namespaces distinct (Prism's namespace#local JSON spelling).
		q, _ := parseQName(i.QName)
		ext[q.Namespace+"#"+q.Local] = value
	}
	return clean, ext, nil
}

// expected names the field's type in a refusal.
func (i FormItem) expected() string {
	if i.Type == "choice" {
		return "one of the offered values"
	}
	return i.Type
}

// maxExactWhole is the largest whole number a JSON number holds exactly.
const maxExactWhole = 1<<53 - 1

// accepts reports whether v is a value of the field's type.
func (i FormItem) accepts(v any) bool {
	s, isString := v.(string)
	switch i.Type {
	case "string":
		return isString
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "int", "long":
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return false
		}
		lo, hi := float64(math.MinInt32), float64(math.MaxInt32)
		switch i.xsdType {
		case "short":
			lo, hi = math.MinInt16, math.MaxInt16
		case "byte":
			lo, hi = math.MinInt8, math.MaxInt8
		case "long", "integer":
			lo, hi = -maxExactWhole, maxExactWhole
		case "nonNegativeInteger":
			lo, hi = 0, maxExactWhole
		case "positiveInteger":
			lo, hi = 1, maxExactWhole
		}
		return n >= lo && n <= hi
	case "decimal":
		n, ok := v.(float64)
		return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
	case "date":
		_, err := time.Parse("2006-01-02", s)
		return isString && err == nil
	case "dateTime":
		_, err := time.Parse(time.RFC3339, s)
		return isString && err == nil
	case "choice":
		if !isString {
			return false
		}
		for _, o := range i.Options {
			if o.Value == s {
				return true
			}
		}
	}
	return false
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

// PlanRequestRoleWithValues validates and adds the relation, activation and
// extension values to the same single assignment-add PATCH used by
// PlanRequestRole. The relation is a local name; "" or "default" is member.
func (c *Client) PlanRequestRoleWithValues(user, role, relation, from, to string, fields map[string]any) (Plan, map[string]any, error) {
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
	ref := map[string]any{"oid": role, "type": "RoleType"}
	if rel := localName(relation); rel != "" && rel != "default" {
		ref["relation"] = "org:" + rel
	}
	value := map[string]any{"targetRef": ref}
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
