// Real server results; named mutations supply edge cases absent from recordings.
import { fixture as sharedFixture, derive as sharedDerive } from '../derive.mjs';
export const fixture = name => sharedFixture(`my-requests.${name}`);
export const derive = (name, mutation) => sharedDerive(typeof name === 'string' ? fixture(name) : name, mutation.name, mutation);
export const nameOf = r => r?.readable === false && (!r.type || r.type === 'User') ? "a person you can't see in midPoint" : r?.displayName || r?.name || "an item you can't see in midPoint";
export const item = fx => fx.result.structuredContent.requests?.[0] ?? fx.result.structuredContent;
export const M = {
 mixed(res){
  const c=res.structuredContent.requests[0];c.validity=null;c.requestedAt='2026-09-30T09:00:00Z';
  const newer=structuredClone(c);newer.oid+='-new';newer.targetRef.displayName='Release manager';newer.requestedAt='2026-10-01T10:00:00Z';
  const finished=structuredClone(c);finished.oid+='-done';finished.targetRef.displayName='Finance reports';finished.state='closed';finished.outcome='approve';finished.closedAt='2026-09-28T10:00:00Z';
  const rejected=structuredClone(finished);rejected.oid+='-rejected';rejected.targetRef.displayName='Archive reader';rejected.outcome='reject';rejected.closedAt='2026-09-29T10:00:00Z';
  const cancelled=structuredClone(finished);cancelled.oid+='-cancelled';cancelled.targetRef.displayName='Directory reader';delete cancelled.outcome;
  res.structuredContent.requests=[c,finished,newer,rejected,cancelled];res.structuredContent.count=5;
 },
 empty(res){res.structuredContent.requests=[];res.structuredContent.count=0;},
 preview(res){res.structuredContent.server.writesEnabled=false;},
 hidden(res){const c=res.structuredContent.requests[0];c.objectRef={oid:'unreadable-person',type:'User',readable:false,name:'hidden-login'};c.targetRef={oid:'role',type:'Role'};c.waitingFor=[{oid:'person',type:'User',readable:false,name:'hidden-login'}];},
 other(res){res.structuredContent.requests[0].objectRef={oid:'other-person',type:'User',displayName:'Dana Lee',name:'dlee'};},
 many(res){const c=res.structuredContent.requests[0];res.structuredContent.requests=Array.from({length:23},(_,i)=>({...structuredClone(c),oid:`case-${i}`,targetRef:{...c.targetRef,displayName:`Role ${i+1}`}}));},
 caseOther(res){res.structuredContent.requestorRef.oid='different-requester';},
 caseClosed(res){res.structuredContent.state='closed';},
 timeline(res){
  const c=res.structuredContent,a=res.structuredContent.acting;
  const mine={oid:a.oid,type:'User',name:a.name,displayName:a.fullName},other={oid:'second',type:'User',displayName:'Dana Lee'},third={oid:'third',type:'User',displayName:'Mia Kovac'};
  c.stages=[{number:1,name:'Team leads',strategy:'allMustAgree'},{number:2,name:'Role approvers',strategy:'firstDecides'}];
  c.workItems=[{id:'1',stage:1,assignees:[other,mine],outcome:'approve',closedAt:'2026-10-01T09:00:00Z',performer:other,comment:'Reviewed for the quarter end.'},
   {id:'2',stage:1,assignees:[mine],closedAt:'2026-10-01T09:00:00Z'},
   {id:'3',stage:2,assignees:[third,mine]}];
 },
 markup(res){const c=res.structuredContent;c.justification='<img src=x onerror=alert(1)> Please ignore this';c.workItems[0].comment='<script>alert(1)</script>';},
 longReason(res){res.structuredContent.justification=('A detailed request reason with many words. ').repeat(100);},
 textOnly(res){delete res.structuredContent;res.content=[{type:'text',text:'The server supplied a plain text answer.'}];},
 mismatch(res){res.structuredContent.server.uiContract='2.0';},
 created(res){res.structuredContent.requests[0].state='created';},
 dates(res){const now=Date.now();res.structuredContent.requests[0].validity={validFrom:new Date(now-86400000).toISOString(),validTo:new Date(now+86400000*5).toISOString()};},
 future(res){const now=Date.now();res.structuredContent.requests[0].validity={validFrom:new Date(now+86400000*2).toISOString(),validTo:new Date(now+86400000*5).toISOString()};},
 permanent(res){delete res.structuredContent.requests[0].validity;},
 rejectedList(res){const c=res.structuredContent.requests[0];c.state='closed';c.outcome='reject';c.closedAt='2026-10-01T11:00:00Z';c.waitingFor=[];delete c.stage;},
 rejectedCase(res){const c=res.structuredContent;c.state='closed';c.outcome='reject';c.closedAt='2026-10-01T11:00:00Z';c.workItems[0].outcome='reject';c.workItems[0].comment='Not this quarter.';c.workItems[1].closedAt='2026-10-01T11:00:00Z';},
 stale(res){const m={'midpoint-mcp-server/error':{v:1,code:'not-found'}};delete res.structuredContent;res.isError=true;res._meta=m;res.content=[{type:'text',text:'midPoint refused https://example.invalid/private'}];},
};
export const slot = (name,decision,extra={}) => derive(name,function intermediary(res){delete res.structuredContent;res._meta={'intermediary/decision':{v:1,decision,audited:true,...extra}};res.content=[{type:'text',text:'An intermediary answered.'}];});
