import { CAT, text as s } from './strings.mjs';
import { fixture, derive, personName, roleName } from './derive.mjs';
import { VIEW_URL, defaults, diagnostics, inLiveRegion, sleep, norm } from '../harness.mjs';
import { hostZone, endOfDayIn } from '../derive.mjs';

export const checks = [], runWide = [];
const check=(id,criterion,title,run)=>checks.push({id,criterion,title,run});
const across=(id,criterion,title,run)=>runWide.push({id,criterion,title,run});
const tools={list_my_team:['limit'],get_user_assignments:['oid'],unassign_role:['userOid','roleOid'],whoami:[]};
defaults.allowedTools=tools;
const fx=fixture('person'), sc=fx.result.structuredContent, user=personName(sc.user), role=roleName(sc.assignments[0]);
const userOid=sc.user.oid, roleOid=sc.assignments[0].targetOid;
const secondUser=personName(fixture('second-person').result.structuredContent.user), actingName=personName(sc.acting);
const included=sc.effectiveMembership.find(m=>!m.direct), sourceName=included.via.displayName||included.via.name;
const resourceName=roleName(sc.assignments.find(a=>a.targetType==='Resource')), orgName=fixture('none-visible').result.structuredContent.orgs[0].name;
const remove=s('review.action.revokeLabel',{role,user});
const preview=s('review.action.previewLabel',{role,user});
const noTools={openLinks:{},message:{},updateModelContext:{}};
const answers=name=>[{result:fixture(name).result}];
defaults.tools={whoami:answers('whoami'),list_my_team:answers('team'),get_user_assignments:[
 {when:{oid:userOid},result:fx.result},
 {when:{oid:fixture('second-person').call.arguments.oid},result:fixture('second-person').result},
]};
async function person(t,entry='person',opts={}) {
 const source=typeof entry==='string'?fixture(entry):entry;
 const server=source.result.structuredContent?.server,acting=source.result.structuredContent?.acting;
 const team=server?.writesEnabled===false?'team-preview':acting?.mode==='personal'?'team-personal':'team';
 const v=await t.open({entry:source,...opts,tools:{list_my_team:answers(team),...(opts.tools||{})}});
 t.ok(await v.waitFor(v.frame.getByRole('heading',{name:s('app.title.accessReview'),exact:true})),'missing view title');
 await sleep(100); return v;
}
const section=(v,key)=>v.frame.getByRole('region',{name:s(key),exact:true});
const roleRow=v=>section(v,'review.section.roles').getByRole('listitem').filter({hasText:role});
async function confirm(t,v,label=remove){
 const b=v.button(label); if(!t.ok(await v.waitFor(b),`missing ${label}`)) return null;
 await b.click(); const d=v.dialog(); t.ok(await v.waitFor(d),'missing dialog'); return d;
}
async function submit(t,v,label=remove){const d=await confirm(t,v,label); if(d) await d.getByRole('button',{name:s(label===preview?'dryrun.submit':'confirm.revoke.submit'),exact:true}).click();}

check('ac01.team-entry','7.4 AC1','team entry selects first report and reads access once',async t=>{
 const v=await person(t,'team',{echo:true});
 t.ok(await v.waitFor(v.button(remove)),'first report did not load');
 t.ok(await v.frame.getByRole('radio',{name:user,exact:true}).isChecked(),'first report not selected');
 const calls=await v.calls('get_user_assignments');t.ok(calls.length===1&&calls[0].args.oid===userOid,'not exactly one first-person read');
 t.ok((await v.calls('list_my_team')).length===0,'team entry repeated');
});
for(const [name,key] of [['no-orgs','review.team.noOrgs'],['none-visible','review.team.noneVisible']]) {
 check(`ac01.${name}`,'7.4 AC1','empty team explains why',async t=>{
  const v=await person(t,name);t.ok(await v.hasText(s(key,{orgs:orgName})),'missing empty-team reason');
  t.ok((await v.calls('get_user_assignments')).length===0,'empty team read a person');
 });
}
check('ac01.empty-personal','7.4 AC1, 6.2','personal empty team names configured identity',async t=>{
 const v=await person(t,'empty-personal');t.ok(await v.hasText(s('review.team.personalNote',{name:actingName})),'personal note missing');
});
check('ac02.switch','7.4 AC2','one assignment read per person switch and no repeated team query',async t=>{
 const v=await person(t); const b=v.frame.getByRole('radio',{name:secondUser,exact:true});
 t.ok(await v.waitFor(b),'no team picker');await b.check();
 t.ok(await v.waitFor(v.frame.getByRole('heading',{name:secondUser,exact:true})),'second person not shown');
 t.ok(await v.hasText(s('status.personDisabled')),'disabled person missing note');
 t.ok((await v.calls('get_user_assignments')).length===1,'switch did not read once');
 await v.frame.getByRole('radio',{name:user,exact:true}).check();await v.waitFor(v.button(remove));
 t.ok((await v.calls('get_user_assignments')).length===2,'switch back did not read once');
 t.ok((await v.calls('list_my_team')).length===1,'team list read more than once');
});
for(const relation of ['self','other']) {
 check(`ac02.${relation}`,'7.4 AC2, AC4','current person outside team is first, labelled, without removal or hand-off',async t=>{
  const entry=derive('person','relation',relation), name=personName(entry.result.structuredContent.user), v=await person(t,entry);
  const label=s(relation==='self'?'review.person.selfOption':'review.person.otherOption',{name});
  t.ok(await v.waitFor(v.frame.getByRole('radio',{name:label,exact:true})),'missing labelled option');
  t.ok(await v.frame.getByRole('radio').first().isChecked(),'current extra option not first and selected');
  t.ok(await v.hasText(s(`review.relation.${relation}`)),'relation note missing');
  t.ok(await v.frame.getByRole('button',{name:/^(Remove|Preview removal|Request access for) /}).count()===0,'unexpected write or handoff');
 });
}
check('ac03.person','7.4 AC3, D16','names are display names, normal person has no status note or email',async t=>{
 const v=await person(t);const txt=await v.surfaceStrings();
 for(const bad of ['bstone','jdoe','mkovac','@example.com',userOid,'build-runner'])t.ok(!txt.includes(bad),`surface leaked ${bad}`);
 t.ok(!await v.hasText(s('status.personDisabled')),'enabled person shows disabled');
 t.ok(await v.hasText(sourceName),'display name missing');
});
check('ac03.handoff','7.4 AC3, 6.14','handoff sends name and OID as context before the name-only chat message',async t=>{
 const v=await person(t);await v.button(s('review.action.requestFor',{name:user})).click();await sleep(150);
 const context=await v.sent('ui/update-model-context'), messages=await v.sent('ui/message');
 t.ok(context.length===1&&JSON.stringify(context[0].params).includes(userOid),'identifier not in context');
 t.ok(messages.length===1&&messages[0].params.content[0].text===s('review.handoff.requestFor',{name:user}),'wrong visible message');
 t.ok(!JSON.stringify(messages).includes(userOid),'OID leaked in message');
 const log=await v.log();t.ok(log.find(e=>e.msg.method==='ui/update-model-context').seq<log.find(e=>e.msg.method==='ui/message').seq,'context sent after message');
});
check('ac03.handoff-caps','7.4 AC3, 6.14','message capability controls handoff; no context capability still permits name-only handoff',async t=>{
 const v=await person(t,'person',{caps:{serverTools:{}}});t.ok(await v.button(s('review.action.requestFor',{name:user})).count()===0,'handoff without capability');
 const w=await person(t,'person',{caps:{serverTools:{},message:{}}});await w.button(s('review.action.requestFor',{name:user})).click();await sleep(80);
 t.ok((await w.sent('ui/update-model-context')).length===0,'context without capability');t.ok((await w.sent('ui/message')).length===1,'missing handoff');
});
check('ac04.scope','7.4 AC4','only direct role assignments have Remove; other sections have no actions',async t=>{
 const v=await person(t);const count=sc.assignments.filter(a=>a.targetType==='Role').length;
 t.ok(await v.frame.getByRole('button',{name:/^Remove .* from /}).count()===count,'wrong removal count');
 for(const key of ['review.section.inherited','review.section.orgs','review.section.other'])t.ok(await section(v,key).getByRole('button').count()===0,`${key} actionable`);
});
check('ac05.dialog','7.4 AC5, 6.5','removal dialog has summary, no comment, initial Cancel focus and one call',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:answers('pending')},echo:true});const d=await confirm(t,v);if(!d)return;
 t.ok(await d.getByRole('textbox').count()===0,'comment field present');
 for(const key of ['confirm.revoke.body','confirm.revoke.policy'])t.ok(await v.hasText(s(key,{user,role}),{within:d}),`missing ${key}`);
 t.ok(await v.isFocused(d.getByRole('button',{name:s('common.cancel'),exact:true})),'Cancel not initially focused');
 t.ok(await v.hasText(s('confirm.row.role'),{within:d})&&await v.hasText(s('confirm.row.from'),{within:d}),'missing summary rows');
 await d.getByRole('button',{name:s('confirm.revoke.submit'),exact:true}).click();
 t.ok(await v.waitFor(s('review.outcome.pending',{user,role})),'pending outcome missing');
 await sleep(150);const calls=await v.calls('unassign_role');
 t.ok(calls.length===1&&JSON.stringify(calls[0].args)===JSON.stringify({userOid,roleOid}),'wrong removal call');
 t.ok(await v.dialog().count()===0,'dialog stayed open');
});
check('ac05.keyboard','6.5, 6.12','modal traps Tab, Esc and Cancel restore opener focus',async t=>{
 const v=await person(t);let d=await confirm(t,v);if(!d)return;
 const cancel=d.getByRole('button',{name:s('common.cancel'),exact:true});
 await cancel.press('Shift+Tab');t.ok(await v.isFocused(d.getByRole('button',{name:s('confirm.revoke.submit'),exact:true})),'reverse Tab left dialog');
 await d.getByRole('button',{name:s('confirm.revoke.submit'),exact:true}).press('Tab');t.ok(await v.isFocused(cancel),'Tab left dialog');
 await cancel.press('Escape');t.ok(await v.isFocused(v.button(remove)),'Esc did not restore focus');
 d=await confirm(t,v);await d.getByRole('button',{name:s('common.cancel'),exact:true}).click();t.ok(await v.isFocused(v.button(remove)),'Cancel did not restore focus');
 t.ok((await v.calls('unassign_role')).length===0,'cancel wrote');
});
check('ac06.role-words','7.4 AC6','role descriptions, plain governance relations and disabled/archived wording',async t=>{
 const v=await person(t,derive('person','relations'));
 for(const key of ['review.link.approver','review.link.owner','status.disabled','status.archived'])t.ok(await v.hasText(s(key)),`missing ${key}`);
 t.ok(await v.hasText(sc.assignments[0].target.description),'description missing');
 const txt=await v.text();for(const raw of ['org:','deputy','RoleType','ResourceType','Archetype','employee'])t.ok(!txt.includes(raw),`raw vocabulary ${raw}`);
});
check('ac06.description','7.4 AC6, 6.12','description clamps to two lines with one ellipsis and expands without a read',async t=>{
 const entry=derive('person','longDescription'),v=await person(t,entry,{width:320});const row=roleRow(v);
 t.ok(await v.waitFor(row.getByRole('button',{name:s('common.showMore'),exact:true})),'no description disclosure');
 const before=await v.calls();const txt=await row.innerText();t.ok(txt.includes('…')&&!txt.includes('….'),'bad ellipsis');
 await row.getByRole('button',{name:s('common.showMore'),exact:true}).click();
 t.ok(await v.hasText(entry.result.structuredContent.assignments[0].target.description,{within:row}),'full text not shown');
 await row.getByRole('button',{name:s('common.showLess'),exact:true}).click();t.ok((await v.calls()).length===before.length,'clamp called tool');
});
for(const kind of ['until','fromUntil','from','unlimited','past-start','invalid']){
 check(`ac06.validity.${kind}`,'7.4 AC6, 4.5, 6.11','existing access dates use host calendar dates and no unlimited note',async t=>{
  const zone=hostZone(),today=endOfDayIn(0,zone),tomorrow=endOfDayIn(1,zone),past=endOfDayIn(-5,zone);
  const from={fromUntil:tomorrow,from:tomorrow,'past-start':past,invalid:'invalid date'}[kind]||'',to=['until','fromUntil','past-start'].includes(kind)?(kind==='fromUntil'?endOfDayIn(2,zone):today):'';
  const v=await person(t,derive('person','dates',from,to),{zone});const txt=await roleRow(v).innerText();
  if(kind==='until'||kind==='past-start')t.ok(txt.includes(s('validity.until',{date:s('time.today')})),'end date not today');
  if(kind==='from'||kind==='fromUntil')t.ok(txt.includes(s('validity.from',{date:s('time.tomorrow')})),'start date not tomorrow');
  if(kind==='invalid')t.ok(txt.includes('invalid date'),'unparseable date lost');
  if(kind==='unlimited')t.ok(!/Until|From|No end date/.test(txt),'unlimited access has a validity label');
 });
}
check('ac07.included-account','7.4 AC7','included access names its source and account names its resource',async t=>{
 const v=await person(t);t.ok(await section(v,'review.section.inherited').getByText(s('common.inheritedVia',{source:sourceName}),{exact:true}).count()===1,'source missing');
 t.ok(await v.hasText(s('review.other.account',{name:resourceName})),'account wording missing');
 const w=await person(t,derive('person','noVia'));t.ok(await w.hasText(s('common.inherited')),'missing unknown-source fallback');
});
for(const [name,key] of [['removed','review.outcome.removed'],['pending','review.outcome.pending'],['still','review.outcome.stillAssigned']]){
 check(`ac08.${name}`,'7.4 AC8','applied outcome persists above roles through one access re-read',async t=>{
  const v=await person(t,'person',{echo:true,tools:{unassign_role:answers(name),get_user_assignments:answers(name==='removed'?'after':'person')}});
  await submit(t,v);const expected=s(key,{role,user});t.ok(await v.waitFor(expected),'outcome not shown');await sleep(150);
  t.ok(await v.hasText(expected),'outcome lost after read');t.ok((await v.calls('get_user_assignments')).length===1,'wrong read-back count');
  t.ok((await v.sent('ui/update-model-context')).length===1,'write context missing or repeated');
  t.ok(await inLiveRegion(v.frame,expected,'polite'),'outcome not announced');
  const body=await v.text();t.ok(body.indexOf(expected)<body.indexOf('\nRoles'),'outcome below roles');
  if(name==='removed')t.ok(await v.button(remove).count()===0,'removed row retained');
  else t.ok(await v.button(remove).count()===1,'still assigned row disappeared');
 });
}
check('ac08.preview','7.4 AC8, 6.7','preview has same confirmation, summary and hidden technical request; no read-back',async t=>{
 const v=await person(t,'person-preview',{tools:{unassign_role:answers('preview')}});
 t.ok(await v.hasText(s('dryrun.banner.title')),'preview banner missing');await submit(t,v,preview);
 t.ok(await v.waitFor(s('dryrun.result.title')),'preview missing');
 t.ok((await v.calls('get_user_assignments')).length===0,'preview re-read');t.ok((await v.sent('ui/update-model-context')).length===0,'preview sent context');
 t.ok(!(await v.text()).includes(userOid),'preview exposed OID');await v.button(s('common.showDetails')).click();
 t.ok(await v.hasText('PATCH',{loose:true})&&await v.hasText(userOid,{loose:true}),'technical request missing');
});
check('ac08.entry-outcome','7.4 outcome mode','agent removal renders outcome and one read with no confirmation or write',async t=>{
 const v=await person(t,'removed',{tools:{get_user_assignments:answers('after')},echo:true});
 t.ok(await v.waitFor(s('review.outcome.removed',{role,user})),'entry outcome missing');
 t.ok((await v.calls('get_user_assignments')).length===1,'agent outcome did not read once');
 t.ok((await v.calls('unassign_role')).length===0&&await v.dialog().count()===0,'agent outcome initiated a write');
 t.ok((await v.sent('ui/update-model-context')).length===0,'agent outcome re-announced context');
});
check('ac09.not-assigned','7.4 AC9','not-assigned error disables just that role and offers Refresh',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:answers('not-assigned')}});await submit(t,v);
 t.ok(await v.waitFor(s('error.notAssigned')),'not-assigned sentence missing');t.ok(await v.button(remove).isDisabled(),'row remains actionable');
 t.ok(await v.frame.getByRole('button',{name:s('common.refresh'),exact:true}).count()===2,'Refresh not suggested by error');
 t.ok(await inLiveRegion(v.frame,s('error.notAssigned'),'assertive'),'write error not announced');
});
check('ac09.refused','7.4 AC9, owner rule','midPoint permission refusal shown; Remove remains available',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:answers('not-authorized')}});await submit(t,v);t.ok(await v.waitFor(s('error.notAuthorized')),'refusal missing');
 t.ok(await v.button(remove).isEnabled(),'refusal hid or disabled button');t.ok((await v.calls('get_user_assignments')).length===0,'failed write re-read');
});

for(const decision of ['allowed','held','denied']){
 check(`shared.slot-entry.${decision}`,'7.4 AC10, 5.5','entry slot follows intermediary semantics',async t=>{
  const entry=derive('person','slot',decision,{source:'Access policy',reason:'Needs review',approver:{name:'Access team'}}),v=await person(t,entry);
  if(decision==='allowed'){t.ok(await v.button(remove).count()===1,'allowed lost content');t.ok(!await v.hasText('Access policy'),'allowed rendered strip');}
  else {t.ok(await v.hasText(s(decision==='held'?'strip.held.title':'strip.denied.title')),'slot title missing');t.ok(await v.hasText(s('strip.source',{source:'Access policy'})),'source missing');t.ok(await v.button(remove).count()===0,'blocked entry shows access');t.ok((await v.calls('whoami')).length===1,'no header fallback');}
 });
 if(decision==='allowed')continue;
 check(`shared.slot-write.${decision}`,'7.4 AC8, 5.5','held or denied write keeps row and sends no read-back or model context',async t=>{
  const res=derive('removed','slot',decision,{reason:'Wait for review.'}).result;
  const v=await person(t,'person',{tools:{unassign_role:[{result:res}]}});await submit(t,v);
  t.ok(await v.waitFor(s(decision==='held'?'strip.held.title':'strip.denied.title')),'slot missing');t.ok(await v.button(remove).isEnabled(),'row not actionable');
  t.ok((await v.calls('get_user_assignments')).length===0,'blocked write re-read');t.ok((await v.sent('ui/update-model-context')).length===0,'blocked write sent context');
 });
}
check('shared.slot-validation','5.3','invalid slots ignored and optional slot data sanitized',async t=>{
 const v=await person(t,derive('person','invalidSlot',{v:2,decision:'held',audited:true}));t.ok(await v.button(remove).count()===1,'unknown slot version blocked view');
 const w=await person(t,derive('person','slot','held',{source:22,approver:{name:4},reason:'<b>Review</b>\u0000'+'x'.repeat(600)}));
 t.ok(await w.hasText(s('strip.sourceUnnamed')),'bad source not dropped');t.ok(await w.hasText(s('strip.held.bodyNoApprover')),'bad approver not dropped');
 t.ok((await w.text()).includes('<b>Review</b>'),'slot treated as HTML');t.ok(!(await w.text()).includes('\u0000'),'slot controls retained');
});
check('shared.read-only','6.1, 6.6','read-only host hides tool controls, wins over preview banner, keeps handoff',async t=>{
 const v=await person(t,'person-preview',{caps:noTools});
 t.ok(await v.hasText(s('common.readOnlyHost'))&&await v.hasText(s('common.askAssistantToRefresh')),'read-only copy missing');
 for(const label of [s('common.refresh'),remove,preview])t.ok(await v.button(label).count()===0,`visible ${label}`);
 t.ok(await v.frame.getByRole('radio').count()===0,'picker not hidden');t.ok(!await v.hasText(s('dryrun.banner.title')),'both informational banners');
 t.ok(await v.button(s('review.action.requestFor',{name:user})).count()===1,'handoff hidden despite own capability');t.ok((await v.calls()).length===0,'read-only host called tools');
});
check('shared.personal-header','6.2','personal header explains account; resource-server header omits identity',async t=>{
 const v=await person(t,'person-personal');t.ok(await v.hasText(s('header.mode.personal',{name:actingName})),'personal header missing');
 await v.button(s('common.whatsThis')).click();t.ok(await v.hasText(s('header.mode.personal.help')),'personal help missing');
 const w=await person(t);t.ok(!await w.hasText(s('header.mode.personal',{name:actingName})),'resource-server identity shown');
});
check('shared.shared-credential','6.2, 6.8','shared credential refusal uses code and whoami warning',async t=>{
 const v=await person(t,'shared',{tools:{whoami:answers('whoami-shared')}});t.ok(await v.waitFor(s('error.sharedCredential')),'shared refusal missing');
 t.ok(await v.hasText(s('header.sharedCredential',{name:actingName})),'shared header warning missing');t.ok((await v.calls('whoami')).length===1,'fallback not once');
});
check('shared.refresh-person','6.6','Refresh reuses current person, dims snapshot until answer, clears outcome',async t=>{
 const v=await person(t,'person',{tools:{get_user_assignments:[{result:fx.result,hold:true}]}});await v.button(s('common.refresh')).click();
 t.ok(await v.button(s('common.refreshing')).isDisabled(),'Refresh not disabled');t.ok(await v.hasText(user),'old snapshot hidden');
 t.ok(await v.frame.getByRole('main').getAttribute('aria-busy')==='true','not aria-busy');
 const calls=await v.calls('get_user_assignments');t.ok(calls.length===1&&calls[0].args.oid===userOid,'wrong refresh args');await v.release();
 t.ok(await v.waitFor(v.button(s('common.refresh'))),'refresh did not complete');
});
check('shared.refresh-team','6.6, 7.4 Tools','team-entry Refresh reads team and current person once',async t=>{
 const v=await person(t,'team');await v.waitFor(v.button(remove));await v.button(s('common.refresh')).click();await sleep(180);
 t.ok((await v.calls('list_my_team')).length===1,'team refresh wrong count');t.ok((await v.calls('get_user_assignments')).length===2,'current access not refreshed once');
 t.ok(JSON.stringify((await v.calls('list_my_team'))[0].args)==='{"limit":100}','team limit not 100');
});
check('shared.refresh-failure','6.8','failed Refresh keeps previous snapshot and shows error',async t=>{
 const v=await person(t,'person',{tools:{get_user_assignments:answers('unavailable')}});await v.button(s('common.refresh')).click();
 t.ok(await v.waitFor(s('error.midpointUnavailable')),'refresh error missing');t.ok(await v.button(remove).count()===1,'snapshot lost');
});
check('shared.refresh-slot','5.5','held refresh replaces content with slot',async t=>{
 const v=await person(t,'person',{tools:{get_user_assignments:[{result:derive('person','slot','held').result}]}});await v.button(s('common.refresh')).click();
 t.ok(await v.waitFor(s('strip.held.title')),'refresh slot missing');t.ok(await v.button(remove).count()===0,'old content retained under blocked refresh');
});
check('shared.switch-working','7.4 switching state','person switch dims prior snapshot and picker until reply',async t=>{
 const v=await person(t,'person',{tools:{get_user_assignments:[{result:fixture('second-person').result,hold:true}]}});
 await v.frame.getByRole('radio',{name:secondUser,exact:true}).check();
 t.ok(await v.frame.getByRole('main').getAttribute('aria-busy')==='true','switch lacks busy state');t.ok(await v.button(remove).isDisabled(),'stale person writable during switch');
 await v.release();t.ok(await v.waitFor(s('status.personDisabled')),'switch did not finish');
});
check('shared.slow-read','6.5, 6.7','slow read waits past eight seconds without retry or timeout',async t=>{
 const v=await person(t,'person',{tools:{get_user_assignments:[{result:fx.result,hold:true}]}});await v.button(s('common.refresh')).click();
 await sleep(8350);t.ok(await v.waitFor(s('state.slow')),'slow read notice missing');t.ok((await v.calls('get_user_assignments')).length===1,'slow read retried');
 t.ok(!await v.hasText(s('error.midpointUnavailable')),'slow became error');await v.release();t.ok(await v.waitGone(v.frame.getByText(s('state.slow'),{exact:true})),'slow notice retained');
});
check('shared.slow-write','6.5, D27','working dialog cannot dismiss or repeat write and has slow notice',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:[{result:fixture('removed').result,hold:true}]}});await submit(t,v);
 const d=v.dialog();t.ok(await d.getByRole('button',{name:s('confirm.working'),exact:true}).isDisabled(),'submit not disabled');
 t.ok(await d.getByRole('button',{name:s('common.cancel'),exact:true}).isDisabled(),'Cancel not disabled');
 await d.press('Escape');t.ok(await v.visible(d),'Esc dismissed pending write');await sleep(8350);
 t.ok(await v.hasText(s('state.slow'),{within:d}),'slow write notice missing');t.ok((await v.calls('unassign_role')).length===1,'write repeated');await v.release();
 t.ok(await v.waitFor(s('review.outcome.removed',{role,user})),'write never completed');
});
check('shared.loading-cancel','6.7','tool-input loading and cancellation do not trigger a write',async t=>{
 const v=await t.open({entry:fixture('person'),deliver:'input-only'});t.ok(await v.waitFor(s('review.loading')),'loading text missing');
 await v.notify('ui/notifications/tool-input-partial',{arguments:{userOid,roleOid}});t.ok((await v.calls()).length===0,'partial input called tool');
 await v.notify('ui/notifications/tool-cancelled',{reason:'Cancelled by the host'});t.ok(await v.waitFor(s('state.cancelledReason',{reason:'Cancelled by the host'})),'cancelled state missing');
});
check('shared.loading-slow','6.7','initial tool input gets one slow notice',async t=>{
 const v=await t.open({entry:fixture('person'),deliver:'input-only'});await v.waitFor(s('review.loading'));await sleep(8350);
 t.ok(await v.waitFor(s('state.slow')),'initial slow notice missing');t.ok((await v.calls()).length===0,'loading triggered calls');
});
for(const mutation of ['textOnly','malformed','mismatch']){
 check(`shared.${mutation}`,'6.7','unknown shape or major mismatch renders server text',async t=>{
  const v=await t.open({entry:derive('person',mutation)});t.ok(await v.waitFor(s(mutation==='mismatch'?'state.versionMismatch':'state.textOnly')),'fallback missing');
  t.ok(await v.button(remove).count()===0,'fallback offered write');
 });
}
check('shared.error-details','6.8','stable code wins over text and details redact every address',async t=>{
 const v=await person(t,derive('person','errorDetails','not-authorized'));t.ok(await v.waitFor(s('error.notAuthorized')),'code not classified');
 t.ok(!await v.hasText('Failure'),'raw error on surface');await v.button(s('common.showDetails')).click();
 t.ok(await v.hasText(s('common.detailsTool',{tool:'get_user_assignments'})),'tool name missing');
 const txt=await v.text();t.ok(!txt.includes('https://')&&!txt.includes('http://'),'URL leaked');t.ok(txt.includes(s('common.redacted')),'no redaction marker');
});
check('shared.host-error','6.8','JSON-RPC refusal is shown without hiding row',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:[{rpcError:{message:'Refused by host'}}]}});await submit(t,v);
 t.ok(await v.waitFor(s('error.hostRefused')),'host refusal not classified');t.ok(await v.button(remove).isEnabled(),'row disabled');
});
check('shared.empty','7.4 states','no direct assignments gives named empty state and included access stays',async t=>{
 const v=await person(t,derive('person','includedOnly'));t.ok(await v.hasText(s('review.empty',{user})),'empty message missing');
 t.ok(await v.hasText(s('review.section.inherited')),'included access hidden by empty direct list');
});
check('shared.names-fallback','4.5, D16','missing names use catalog fallback, never OIDs',async t=>{
 const v=await person(t,derive('person','missingNames'));const txt=await v.text();t.ok(txt.includes(s('common.personHidden')),'hidden person not named');
 t.ok(txt.includes(s('common.itemHidden')),'hidden role not named');t.ok(!txt.includes(userOid)&&!txt.includes(roleOid),'OID used as name');
});
check('shared.injection','9','role descriptions render literally and never reach model context',async t=>{
 const entry=derive('person','injection'),v=await person(t,entry);t.ok(await v.hasText(entry.result.structuredContent.assignments[0].target.description),'markup not literal');
 t.ok((await v.sent('ui/update-model-context')).length===0&&(await v.sent('ui/message')).length===0,'description sent to model');
});
check('shared.inline-pagination','6.13','inline rows show ten then ten more without another tool call',async t=>{
 const v=await person(t,derive('person','many'));const reg=section(v,'review.section.roles');t.ok(await reg.getByRole('listitem').count()===10,'initial rows not ten');
 const before=(await v.calls()).length;await reg.getByRole('button',{name:s('common.showMore'),exact:true}).click();t.ok(await reg.getByRole('listitem').count()===20,'not twenty rows');
 await reg.getByRole('button',{name:s('common.showMore'),exact:true}).click();t.ok(await reg.getByRole('listitem').count()===25,'not all rows');t.ok((await v.calls()).length===before,'pagination called tool');
});
check('shared.expand-size','6.13','Expand follows returned mode and reports changed size; fixed height scrolls',async t=>{
 const v=await person(t,derive('person','many'),{context:{containerDimensions:{height:480,width:720}}});
 const reg=section(v,'review.section.roles');await v.button(s('common.expand')).click();
 t.ok(await v.waitFor(v.button(s('common.collapse'))),'fullscreen label missing');t.ok(await reg.getByRole('listitem').count()===25,'fullscreen limited rows');
 t.ok((await v.sent('ui/request-display-mode')).length===1,'mode not requested');t.ok((await v.sent('ui/notifications/size-changed')).length>0,'size never reported');
 const m=await v.frame.getByRole('main').evaluate(el=>({client:el.clientHeight,scroll:el.scrollHeight}));t.ok(m.scroll>m.client,'fixed-height content not scrollable');
 await v.button(s('common.collapse')).click();t.ok(await v.waitFor(v.button(s('common.expand'))),'inline not restored');
});
check('shared.no-expand','6.13','no Expand without host mode',async t=>{
 const v=await person(t,'person',{context:{availableDisplayModes:['inline']}});t.ok(await v.button(s('common.expand')).count()===0,'Expand without mode');
});
check('shared.themes-fonts','6.9','host theme follows changes; only font variables adopted',async t=>{
 const v=await person(t,'person',{context:{styles:{variables:{'--font-sans':'Arial','--mp-canvas':'red'}}}});
 const before=await v.frame.evaluate(()=>({theme:document.documentElement.dataset.theme,bg:getComputedStyle(document.body).backgroundColor,font:getComputedStyle(document.documentElement).getPropertyValue('--font-sans')}));
 t.ok(before.theme==='light'&&before.font==='Arial'&&before.bg!=='rgb(255, 0, 0)','host styles violated palette or font');await v.setTheme('dark');await sleep(80);
 const after=await v.frame.evaluate(()=>({theme:document.documentElement.dataset.theme,bg:getComputedStyle(document.body).backgroundColor}));t.ok(after.theme==='dark'&&before.bg!==after.bg,'dark did not apply');
});
for(const width of [320,720]){
 check(`shared.layout.${width}`,'6.1, 6.12','narrow/enlarged view and dialog have no horizontal overflow',async t=>{
  const v=await person(t,derive('person','longDescription'),{width,context:{containerDimensions:{maxHeight:1600,width}}});
  if(width===720)await v.frame.evaluate(()=>{document.body.style.zoom='2';});
  await confirm(t,v);const m=await v.frame.evaluate(()=>({sw:document.documentElement.scrollWidth,cw:document.documentElement.clientWidth}));t.ok(m.sw<=m.cw+1,`overflow: ${JSON.stringify(m)}`);await t.shot(v,`width-${width}`);
 });
}
check('bridge.idempotence','3.5','duplicate entry and tool echoes do not repeat reads or writes',async t=>{
 const v=await person(t,'team',{echo:true});await v.waitFor(v.button(remove));
 await v.notify('ui/notifications/tool-result',fixture('team').result);await v.notify('ui/notifications/tool-result',fx.result);
 await v.notify('ui/notifications/tool-result',derive('person','unknownTool').result);await sleep(100);
 t.ok((await v.calls('get_user_assignments')).length===1,'echo repeated access read');t.ok((await v.calls('unassign_role')).length===0,'notification wrote');
 const torn=await v.request('ui/resource-teardown',{});t.ok(torn.result&&Object.keys(torn.result).length===0,'teardown not answered');
});
check('shared.no-gui','S15 deferred','without server.gui no link or explanatory link note',async t=>{
 const v=await person(t);t.ok(!await v.hasText(s('common.openInMidpoint'))&&!await v.hasText(s('common.openInMidpointNote')),'absent links rendered');t.ok((await v.sent('ui/open-link')).length===0,'link opened');
});
check('shared.list-text','4.8','real list fixtures name every item and keep OIDs for agent calls',async t=>{
 const txt=fx.result.content[0].text;t.ok(txt.startsWith(`${sc.user.name} has ${sc.assignments.length} direct assignment(s), ${sc.effectiveMembership.length} effective membership(s).\n`),'first summary changed');
 t.ok(txt.includes('Direct assignments:')&&txt.includes('Effective membership:'),'group lines missing');
 for(const a of sc.assignments)t.ok(txt.includes(a.targetOid),`assignment ${a.targetOid} omitted`);
 for(const u of fixture('team').result.structuredContent.users)t.ok(fixture('team').result.content[0].text.includes(u.oid),'team item omitted');
});
const viewConsole = (d) => d.console.filter((m) => m.type === 'error' && (m.url === '' || m.url.startsWith(VIEW_URL)));

across('shared.no-network', '7.4 AC10, 3.2, 3.6', 'the view made no network request (routes, CSP, network APIs)', (t) => {
  for (const d of diagnostics) {
    const reqs = d.routed.filter((r) => r.frameUrl.startsWith(VIEW_URL));
    if (reqs.length) t.fail(`${d.check}: requests ${JSON.stringify(reqs.slice(0, 3))}`);
    if (d.inst?.network?.length) t.fail(`${d.check}: ${JSON.stringify(d.inst.network.slice(0, 3).map((n) => `${n.api} ${n.target}`))}`);
    if (d.inst?.csp?.length) t.fail(`${d.check}: CSP violations ${JSON.stringify(d.inst.csp.slice(0, 3))}`);
    if (d.inst?.popups?.length) t.fail(`${d.check}: window.open ${JSON.stringify(d.inst.popups)}`);
  }
});

across('shared.no-console-errors', '7.4 AC10', 'no console errors or uncaught exceptions in the view', (t) => {
  for (const d of diagnostics) {
    const errs = [...viewConsole(d).map((m) => m.text), ...(d.inst?.errors ?? [])];
    if (errs.length) t.fail(`${d.check}: ${JSON.stringify([...new Set(errs)].slice(0, 3))}`);
  }
});

across('shared.no-storage', '3.6', 'no browser storage or cookies', (t) => {
  for (const d of diagnostics) if (d.inst?.storage?.length) t.fail(`${d.check}: ${[...new Set(d.inst.storage.map((s) => s.api))].join(', ')}`);
});

// The slow notice may wake a little after the oldest wait reaches 8 s, or sooner when a wait
// began earlier, so any timeout up to SLOW_MAX_MS counts as that notice; nothing may run longer.
const SLOW_MAX_MS = 8100;
across('shared.no-timers', '7.4 AC10, 6.6, 6.5 (D27)', 'no setInterval; no timer longer than the 8 s slow notice', (t) => {
  for (const d of diagnostics) {
    if (!d.inst) continue;
    if (d.inst.intervals.length) t.fail(`${d.check}: setInterval(${d.inst.intervals[0].ms}) at ${d.inst.intervals[0].stack}`);
    const long = d.inst.timeouts.filter((x) => x.ms > SLOW_MAX_MS);
    if (long.length) t.fail(`${d.check}: setTimeout(${long[0].ms}) at ${long[0].stack}`);
  }
  if (!diagnostics.some((d) => d.inst)) t.fail('the instrumentation never loaded in a view frame');
});

across('bridge.protocol', '3.3, 3.4, 7.4 Tools', 'only allowed tools and arguments, capabilities respected, every call anticipated', (t) => {
  for (const d of diagnostics) {
    for (const x of d.state?.violations ?? []) t.fail(`${d.check}: ${x}`);
    for (const u of d.state?.unanswered ?? []) t.fail(`${d.check}: unexpected call ${u.name} ${JSON.stringify(u.args)}`);
  }
});

async function contrastIssues(v) {
  return v.frame.evaluate(() => {
    const parse = (c) => {
      const m = /rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/.exec(c);
      return m ? { r: +m[1], g: +m[2], b: +m[3], a: m[4] === undefined ? 1 : +m[4] } : null;
    };
    const lum = ({ r, g, b }) => {
      const f = (x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const bgOf = (el) => {
      for (let n = el; n; n = n.parentElement) {
        const c = parse(getComputedStyle(n).backgroundColor);
        if (c && c.a > 0.5) return c;
      }
      return null;
    };
    const out = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const seen = new Set();
    for (let tn = walker.nextNode(); tn; tn = walker.nextNode()) {
      const el = tn.parentElement;
      if (!el || seen.has(el) || !tn.textContent.trim()) continue;
      seen.add(el);
      const cs = getComputedStyle(el);
      if (cs.visibility === 'hidden' || el.getClientRects().length === 0 || el.closest('[disabled],[aria-disabled="true"],[aria-hidden="true"]')) continue;
      let opacity = 1;
      for (let n = el; n; n = n.parentElement) opacity *= +getComputedStyle(n).opacity;
      if (opacity < 0.99) continue; // dimmed while refreshing; judged when idle
      const fg = parse(cs.color);
      const bg = bgOf(el);
      if (!fg || !bg) {
        if (!bg) out.push({ text: tn.textContent.trim().slice(0, 40), why: 'no painted background' });
        continue;
      }
      const l1 = lum(fg); const l2 = lum(bg);
      const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
      const size = parseFloat(cs.fontSize);
      const large = size >= 24 || (size >= 18.66 && +cs.fontWeight >= 700);
      if (ratio < (large ? 3 : 4.5)) out.push({ text: tn.textContent.trim().slice(0, 40), ratio: Math.round(ratio * 100) / 100, fg: cs.color, bg: `rgb(${bg.r}, ${bg.g}, ${bg.b})` });
    }
    return out;
  });
}


for(const theme of ['light','dark']) {
 check(`shared.contrast.${theme}`,'6.9, 6.12','text contrast meets AA on rows, descriptions and confirmation',async t=>{
  const v=await person(t,'person',{context:{theme}});let issues=await contrastIssues(v);t.ok(!issues.length,JSON.stringify(issues.slice(0,4)));
  await confirm(t,v);issues=await contrastIssues(v);t.ok(!issues.length,JSON.stringify(issues.slice(0,4)));await t.shot(v,theme);
 });
}
check('shared.read-only-team','6.6','read-only team entry shows named reports with no picker or tool call',async t=>{
 const v=await person(t,'team',{caps:noTools});t.ok(await v.hasText(user)&&await v.hasText(secondUser),'read-only team snapshot is blank');
 t.ok(await v.frame.getByRole('radio').count()===0&&(await v.calls()).length===0,'read-only entry has tool controls');
});
check('shared.unmanaged','7.4 picker','no manager link means no team query or picker',async t=>{
 const v=await person(t,derive('person','unmanaged'));t.ok((await v.calls('list_my_team')).length===0,'unmanaged called team tool');t.ok(await v.frame.getByRole('radio').count()===0,'unmanaged shows picker');
});
check('shared.outcome-lifetime','7.4 AC8','next action, person switch and Refresh clear the prior removal notice',async t=>{
 const v=await person(t,'person',{tools:{unassign_role:answers('pending')}});await submit(t,v);const notice=s('review.outcome.pending',{user,role});await v.waitFor(notice);await sleep(120);
 await confirm(t,v);t.ok(!await v.hasText(notice),'next confirmation retained notice');await v.dialog().getByRole('button',{name:s('common.cancel'),exact:true}).click();
 await submit(t,v);await v.waitFor(notice);await sleep(120);await v.frame.getByRole('radio',{name:secondUser,exact:true}).check();t.ok(!await v.hasText(notice),'person switch retained notice');
 await v.waitFor(s('status.personDisabled'));await v.frame.getByRole('radio',{name:user,exact:true}).check();await v.waitFor(v.button(remove));
 await submit(t,v);await v.waitFor(notice);await sleep(120);await v.button(s('common.refresh')).click();t.ok(!await v.hasText(notice),'Refresh retained notice');
});
