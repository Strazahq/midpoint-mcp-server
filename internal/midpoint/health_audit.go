package midpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// auditErrors reads the audit records of failed executions in the window,
// newest first, over the audit script route of SearchAudit. midPoint has no
// REST audit endpoint, so the route needs script access: when midPoint refuses
// the script (most people, and every caller under OIDC impersonation), the
// section is skipped and says why, and the other sections still answer.
func (c *Client) auditErrors(ctx context.Context, since time.Time, limit int) AuditErrorsSection {
	sec := AuditErrorsSection{Items: []AuditRecord{}}
	out, err := c.ExecuteScript(ctx, auditScriptBody(auditErrorsGroovy(since, limit+1)))
	if err != nil {
		sec.SectionHead = sectionError(err, "")
		if sec.Code == CodeNotAuthorized {
			sec.Status = SectionSkipped
			sec.Reason = "needs script access: midPoint offers its audit trail over REST only through a script, " +
				"and refused that script for you (" + err.Error() + ")"
		} else {
			sec.Code = CodeAuditUnavailable
		}
		return sec
	}
	if out.Status == "fatal_error" {
		sec.SectionHead = SectionHead{Status: SectionFailed, Code: CodeAuditUnavailable,
			Reason: "the audit script ended in fatal_error"}
		return sec
	}
	sec.Status = SectionOK
	recs := parseAuditErrorItems(out.Items)
	if len(recs) > limit {
		sec.More, recs = true, recs[:limit]
	}
	for i := range recs {
		recs[i].Message = cut(recs[i].Message, messageLimit)
	}
	sec.Items = append(sec.Items, recs...)
	sec.Count = len(sec.Items)
	return sec
}

// auditErrorsGroovy renders the script: the audit records of the execution
// stage whose outcome is a fatal or partial error, after since, newest first.
// It is the filter of midPoint's own "Error audit records in 24h" dashboard
// collection, with partial errors added and the window as given. The script
// reaches the audit service the way SearchAudit's does, and returns each record
// as one tab-delimited string (tabs and line breaks in values become spaces).
func auditErrorsGroovy(since time.Time, maxSize int) string {
	return fmt.Sprintf(`import com.evolveum.midpoint.xml.ns._public.common.audit_3.AuditEventRecordType
import com.evolveum.midpoint.xml.ns._public.common.audit_3.AuditEventStageType
import com.evolveum.midpoint.xml.ns._public.common.common_3.OperationResultStatusType
import javax.xml.datatype.DatatypeFactory
def getField
getField = { obj, name ->
    def k = obj.getClass()
    while (k != null) {
        try { def f = k.getDeclaredField(name); f.setAccessible(true); return f.get(obj) } catch (Throwable t) {}
        k = k.superclass
    }
    return null
}
def auditService = getField(getField(midpoint, 'modelInteractionService'), 'modelAuditService')
def since = DatatypeFactory.newInstance().newXMLGregorianCalendar('%s')
def query = prismContext.queryFor(AuditEventRecordType.class)
    .item(AuditEventRecordType.F_TIMESTAMP).gt(since)
    .and().item(AuditEventRecordType.F_EVENT_STAGE).eq(AuditEventStageType.EXECUTION)
    .and().block()
        .item(AuditEventRecordType.F_OUTCOME).eq(OperationResultStatusType.FATAL_ERROR)
        .or().item(AuditEventRecordType.F_OUTCOME).eq(OperationResultStatusType.PARTIAL_ERROR)
    .endBlock()
    .desc(AuditEventRecordType.F_TIMESTAMP).maxSize(%d).build()
def text = { v -> v == null ? '' : (v instanceof Enum && v.metaClass.respondsTo(v, 'value') ? v.value() : v.toString()) }
def records = auditService.searchObjects(query, null, midpoint.getCurrentTask(), midpoint.getCurrentResult())
return records.collect { r ->
    [r.timestamp, r.eventType, r.eventStage, r.outcome, r.channel,
     (r.initiatorRef ? (r.initiatorRef.targetName ?: r.initiatorRef.oid) : ''),
     (r.targetRef ? (r.targetRef.targetName ?: r.targetRef.oid) : ''),
     (r.message ?: '')].collect { text(it).replace('\t', ' ').replace('\n', ' ').replace('\r', ' ') }.join('\t')
}
`, since.UTC().Format(time.RFC3339), maxSize)
}

// parseAuditErrorItems decodes the records auditErrorsGroovy returns. Each item
// is an xsd:string, which midPoint writes as {"@type":"xsd:string","@value":…}.
func parseAuditErrorItems(items []json.RawMessage) []AuditRecord {
	recs := []AuditRecord{}
	for _, raw := range items {
		var v struct {
			Value string `json:"@value"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Value == "" {
			continue
		}
		f := strings.Split(v.Value, "\t")
		at := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		recs = append(recs, AuditRecord{
			Timestamp: at(0), EventType: at(1), EventStage: at(2), Outcome: at(3),
			Channel: at(4), Initiator: at(5), Target: at(6), Message: at(7),
		})
	}
	return recs
}
