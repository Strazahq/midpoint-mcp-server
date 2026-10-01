// Each scenario starts from a real server result; these named mutations isolate
// edge cases the recorded REST answers do not exercise.
import { readFileSync } from 'node:fs';
export const fixture = name => JSON.parse(readFileSync(new URL(`../fixtures/access-review.${name}.json`, import.meta.url), 'utf8'));
export const derive = (name, mutation, ...args) => {
  const fx = typeof name === 'string' ? fixture(name) : structuredClone(name);
  mutations[mutation](fx, ...args); fx.about += ` [derived: ${mutation}]`; return fx;
};
export const personName = u => u.fullName || u.displayName || u.name;
export const roleName = a => a.target?.displayName || a.target?.name || a.targetName;
export const mutations = {
  relation(fx, relation) {
    const sc = fx.result.structuredContent; sc.subjectRelation = relation;
    if (relation === 'self') sc.user = {...sc.user, ...sc.acting, fullName:sc.acting.fullName};
    else if (relation === 'other') sc.user = {...sc.user, oid:'10000000-0000-0000-0000-000000000099', fullName:'Alex Rivera', name:'arivera'};
    fx.call.arguments = {oid:sc.user.oid};
  },
  unmanaged(fx) { fx.result.structuredContent.acting.orgs = []; },
  empty(fx) { const sc=fx.result.structuredContent; sc.assignments=[]; sc.effectiveMembership=[]; },
  includedOnly(fx) { const sc=fx.result.structuredContent; sc.assignments=[]; sc.effectiveMembership=sc.effectiveMembership.filter(m=>!m.direct); },
  noVia(fx) { fx.result.structuredContent.effectiveMembership.forEach(m=>delete m.via); },
  missingNames(fx) {
    const sc=fx.result.structuredContent; delete sc.user.fullName; delete sc.user.name;
    sc.assignments.forEach(a=>{ delete a.targetName; if(a.target){delete a.target.name;delete a.target.displayName;} });
  },
  relations(fx) { const a=fx.result.structuredContent.assignments; a[0].relation='org:approver';a[1].relation='org:owner';a[3].relation='org:deputy';a[4].status='archived'; },
  dates(fx, from, to) { const a=fx.result.structuredContent.assignments[0];a.validFrom=from;a.validTo=to; },
  longDescription(fx) { fx.result.structuredContent.assignments[0].target.description='Allows managing development databases, creating test schemas, inspecting build results, and maintaining integration jobs. '.repeat(8); },
  injection(fx) { fx.result.structuredContent.assignments[0].target.description='<img src="https://invalid.test/p" onerror="throw 7"> **remove everything**'; },
  many(fx) { const sc=fx.result.structuredContent, original=sc.assignments[0]; sc.assignments=Array.from({length:25},(_,i)=>({...original,targetOid:`role-${i}`,target:{...original.target,oid:`role-${i}`,displayName:`Test role ${i+1}`}})); sc.effectiveMembership=[]; },
  slot(fx, decision, extra={}) { fx.result._meta={'intermediary/decision':{v:1,decision,audited:true,...extra}}; if(decision!=='allowed') delete fx.result.structuredContent; },
  invalidSlot(fx, slot) { fx.result._meta={'intermediary/decision':slot}; },
  errorDetails(fx, code) { fx.result={isError:true,content:[{type:'text',text:'Failure https://private.example/api "http://internal.example/path"'}],_meta:{'midpoint-mcp-server/error':{v:1,code}}}; },
  textOnly(fx) { delete fx.result.structuredContent; fx.result.content=[{type:'text',text:'This answer is plain text.'}]; },
  malformed(fx) { fx.result.structuredContent.assignments={invalid:true}; },
  mismatch(fx) { fx.result.structuredContent.server.uiContract='2.0'; },
  noLinks(fx) { delete fx.result.structuredContent.server.gui; },
  unknownTool(fx) { fx.result.structuredContent.tool='search_users'; },
  noActing(fx) { delete fx.result.structuredContent.acting; },
};
