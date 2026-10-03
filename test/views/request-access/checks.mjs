import {S} from './strings.mjs';
import {fixture,derive,mutations as M,role,roleName,person,dateIn,endOfDayIn} from './derive.mjs';
import {defaults,diagnostics,VIEW_URL,inLiveRegion,sleep} from '../harness.mjs';
export const checks=[],runWide=[];
const check=(id,criterion,title,run)=>checks.push({id,criterion,title,run});
const across=(id,criterion,title,run)=>runWide.push({id,criterion,title,run});
defaults.allowedTools={list_requestable_roles:['limit','forUser','query'],request_role:['roleOid','roleName','userOid','userName','validFrom','validTo','fields'],list_my_team:['limit'],list_my_managers:['limit'],get_case:['oid'],whoami:[]};
defaults.tools={whoami:[{result:fixture('identity').result}],list_my_managers:[{result:fixture('managers').result}],list_my_team:[{result:fixture('team').result}],list_requestable_roles:[{result:fixture('catalog').result}],get_case:[{result:fixture('case').result}],request_role:[{result:fixture('pending').result}]};
const rn=roleName(role());
async function catalog(t,name='catalog',opts={}){const v=await t.open({entry:typeof name==='string'?fixture(name):name,...opts});t.ok(await v.waitFor(v.frame.getByRole('heading',{name:S.title,exact:true})),'Get access did not load');return v;}
async function open(t,v,label=S.requestLabel(rn)){await v.button(label).click();t.ok(await v.waitFor(v.dialog()),'no confirm dialog');return v.dialog();}
async function submit(v,label=S.submit){await v.dialog().getByRole('button',{name:label,exact:true}).click();await v.waitGone(v.dialog());}
async function form(v){await v.dialog().getByLabel(S.project,{exact:true}).fill('OPS-7');}
const listTools=fx=>({list_requestable_roles:[{result:fx.result}]});
const writeTools=(name,extra={})=>({request_role:[{result:(typeof name==='string'?fixture(name):name).result,...extra}]});
const noTools={message:{},updateModelContext:{},openLinks:{}};

check('ac01.self','7.2 AC1','non-managers see only self; manager hint fetched once',async t=>{
 const v=await catalog(t);t.ok(await v.hasText(S.selfOnly),'self line absent');t.ok(await v.frame.getByRole('group',{name:S.target,exact:true}).count()===0,'non-manager picker');t.ok(await v.waitFor(S.managerHint('Jane Doe')),'manager hint absent');
 t.ok((await v.calls('list_my_team')).length===0,'team called');t.ok((await v.calls('list_my_managers')).length===1,'managers not once');
 await v.button(S.refresh).click();await sleep(200);t.ok((await v.calls('list_my_managers')).length===1,'managers fetched again');
});
check('ac01.manager','7.2 AC1','selected managers get direct reports once',async t=>{
 const v=await catalog(t,'manager');t.ok(await v.waitFor(v.frame.getByRole('group',{name:S.target,exact:true})),'no target picker');
 const s=v.frame.getByRole('group',{name:S.target,exact:true});await sleep(150);t.ok(await s.getByRole('radio',{name:person(),exact:true}).count()===1,'report not offered');
 t.ok((await v.calls('list_my_team')).length===1,'team not fetched once');t.ok((await v.calls('list_my_team'))[0].args.limit===100,'team limit');
});
check('ac01.unselected','7.2 AC1','unselected manager links do not enable a picker',async t=>{
 const v=await catalog(t,derive('manager','unselected',M.unselected));t.ok(await v.frame.getByRole('group',{name:S.target,exact:true}).count()===0,'unselected manager picker');t.ok((await v.calls('list_my_team')).length===0,'unselected team call');
});
check('ac02.switch','7.2 AC2','target switches read only, retaining new selection while pending',async t=>{
 const report=fixture('report'),v=await catalog(t,'manager',{tools:{list_requestable_roles:[{result:report.result,hold:true}]}});
 const picker=v.frame.getByRole('group',{name:S.target,exact:true});await sleep(150);await picker.getByRole('radio',{name:person(),exact:true}).check();
 t.ok((await v.calls('request_role')).length===0,'switch sent a write');const calls=await v.calls('list_requestable_roles');t.ok(calls.length===1&&calls[0].args.forUser===report.call.arguments.forUser&&calls[0].args.limit===100,'wrong target call');
 t.ok(await picker.getByRole('radio',{name:person(),exact:true}).isChecked(),'new choice not kept');await v.release();t.ok(await v.waitFor(S.reportHint(person())),'report hint');
});
check('ac03.local','7.2 AC3','one search box, no search button; local name and description filtering',async t=>{
 const v=await catalog(t,'limited');const search=v.frame.getByRole('searchbox',{name:S.search});t.ok(await search.count()===1,'search boxes');t.ok(await v.button(S.search).count()===0,'search button exists');
 await search.fill('BACKUPS');await sleep(750);t.ok(await v.visible(v.button(S.requestLabel(rn))),'description not matched');t.ok(!(await v.visible(v.button(S.requestLabel('Finance reports')))),'unmatched role remains');t.ok((await v.calls('list_requestable_roles')).length===0,'local filtering called server');
});
check('ac03.complete','7.2 AC3','complete catalog never searches midPoint',async t=>{
 const v=await catalog(t);await v.frame.getByRole('searchbox',{name:S.search}).fill('missing');await sleep(750);t.ok(await v.hasText(S.noMatch('missing')),'no-match text');t.ok((await v.calls('list_requestable_roles')).length===0,'complete list queried');
});
check('ac03.debounce','7.2 AC3','600 ms and two characters, one query per pause; Enter does not repeat',async t=>{
 const found=derive('catalog','search-zebra',M.search('zebra')),v=await catalog(t,'limited',{tools:listTools(found)}),s=v.frame.getByRole('searchbox',{name:S.search});
 await s.fill('z');await sleep(720);t.ok((await v.calls('list_requestable_roles')).length===0,'one character queried');
 await s.fill('ze');await sleep(200);await s.fill('zebra');await sleep(200);t.ok((await v.calls('list_requestable_roles')).length===0,'query before pause');
 t.ok(await v.waitFor(S.results('zebra')),'search did not return');await s.press('Enter');await sleep(700);const calls=await v.calls('list_requestable_roles');t.ok(calls.length===1&&calls[0].args.query==='zebra'&&calls[0].args.limit===100,'query not exactly once');
});
check('ac03.clear','7.2 AC3','clearing a replaced search makes exactly one unqueried read',async t=>{
 const found=derive('catalog','search-zebra',M.search('zebra')),v=await catalog(t,'limited',{tools:{list_requestable_roles:[{when:{query:'zebra'},result:found.result},{result:fixture('catalog').result}]}}),s=v.frame.getByRole('searchbox',{name:S.search});
 await s.fill('zebra');await v.waitFor(S.results('zebra'));await s.fill('');await v.waitFor(v.button(S.requestLabel(rn)));const calls=await v.calls('list_requestable_roles');t.ok(calls.length===2&&!('query' in calls[1].args),'clear query calls');
});
check('ac03.stale','7.2 AC3, 3.5','stale search responses and echoes cannot replace newer input',async t=>{
 const found=derive('catalog','search-zebra',M.search('zebra')),v=await catalog(t,'limited',{echo:true,tools:{list_requestable_roles:[{result:found.result,hold:true}]}}),s=v.frame.getByRole('searchbox',{name:S.search});
 await s.fill('zebra');t.ok(await v.waitFor(S.searching('zebra')),'search not announced');await s.fill('finance');await v.release();await sleep(250);
 t.ok(!(await v.hasText(S.results('zebra'))),'stale response used');t.ok(await v.visible(v.button(S.requestLabel('Finance reports'))),'local list lost');t.ok(await s.inputValue()==='finance','input changed');
});
check('ac04.confirm','7.2 AC4, 6.5','confirm names role and person, policy hint, summary rows, no comment',async t=>{
 const v=await catalog(t);const d=await open(t,v);t.ok(await v.hasText(S.titleRequest(rn),{within:d}),'role title');t.ok(await v.hasText(S.body(rn),{within:d}),'requestee body');t.ok(await v.hasText(S.policy,{within:d}),'policy');t.ok(await v.hasText(person(),{within:d}),'summary person');t.ok(await d.getByRole('textbox').count()===0,'comment field without form');t.ok((await v.calls('request_role')).length===0,'write before confirm');
});
check('ac05.permanent','7.2 AC5','no end date is default and sends no dates or self OID',async t=>{
 const v=await catalog(t);const d=await open(t,v);t.ok(await d.getByRole('radio',{name:S.permanent}).isChecked(),'permanent not default');await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a&&Object.keys(a).join(',')==='roleOid,roleName'&&a.roleName===role().name,'unexpected permanent args');
});
for(const n of [7,30,90,17])check(`ac05.days-${n}`,'7.2 AC5','day validity sends host-zone end of day only',async t=>{
 const v=await catalog(t);const d=await open(t,v);await d.getByRole('radio',{name:S.days,exact:true}).check();
 if(n===17){await d.getByRole('radio',{name:S.other,exact:true}).check();await d.getByLabel(S.daysLabel,{exact:true}).fill('17')}else await d.getByRole('radio',{name:`${n} days`,exact:true}).check();
 t.ok(await v.hasText(`Access for ${n} days`,{within:d,loose:true}),'requested duration not summarized');await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a?.validTo===endOfDayIn(n,v.config.zone)&&!a.validFrom,'wrong host-zone validity');
});
check('ac05.custom','7.2 AC5','future custom dates send start/end in host zone',async t=>{
 const v=await catalog(t);const d=await open(t,v);await d.getByRole('radio',{name:S.custom}).check();await d.getByLabel(S.from,{exact:true}).fill(dateIn(v.config.zone,3));await d.getByLabel(S.to,{exact:true}).fill(dateIn(v.config.zone,5));await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a?.validFrom?.includes('T00:00:00')&&a?.validTo?.includes('T23:59:59'),'custom clock bounds');
});
for(const raw of ['0','3651','1.5'])check(`ac05.invalid-days-${raw}`,'7.2 AC5','invalid days refused locally',async t=>{
 const v=await catalog(t),d=await open(t,v);await d.getByRole('radio',{name:S.days,exact:true}).check();await d.getByRole('radio',{name:S.other,exact:true}).check();await d.getByLabel(S.daysLabel,{exact:true}).fill(raw);await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(S.errorDays,{within:d}),'days error absent');t.ok((await v.calls('request_role')).length===0,'invalid days sent');t.ok(await v.isFocused(d.getByLabel(S.daysLabel,{exact:true})),'invalid days not focused');
});
for(const [id,from,to,error] of [['past-from',-1,2,S.errorFrom],['past-end',0,-1,S.errorPast],['order',4,2,S.errorOrder],['empty-end',0,null,S.errorTo]])check(`ac05.${id}`,'7.2 AC5, AC8','custom validity has its own error and sends nothing',async t=>{
 const v=await catalog(t),d=await open(t,v);await d.getByRole('radio',{name:S.custom}).check();await d.getByLabel(S.from,{exact:true}).fill(dateIn(v.config.zone,from));await d.getByLabel(S.to,{exact:true}).fill(to===null?'':dateIn(v.config.zone,to));
 t.ok(await v.hasText('–',{within:d}),'invalid summary is not a dash');await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(error,{within:d}),'specific error absent');t.ok((await v.calls('request_role')).length===0,'invalid dates sent');
});
check('ac08.empty-start','7.2 AC8','empty start means today and omits validFrom',async t=>{
 const v=await catalog(t),d=await open(t,v);await d.getByRole('radio',{name:S.custom}).check();await d.getByLabel(S.from,{exact:true}).fill('');await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a?.validTo&&!a.validFrom,'empty start not today');
});
check('ac06.form','7.2 AC6','all five field types render and only configured, nonempty values are sent',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);
 t.ok(await d.getByLabel(S.justification,{exact:true}).evaluate(el=>el.tagName==='TEXTAREA'),'justification not multiline');
 t.ok(await d.getByLabel(S.needed,{exact:true}).getAttribute('type')==='date','date type');t.ok(await d.getByLabel(S.handover,{exact:true}).getAttribute('type')==='datetime-local','datetime type');
 await form(v);await d.getByLabel(S.ticket,{exact:true}).fill('42');await d.getByLabel(S.needed,{exact:true}).fill('2099-10-02');await d.getByLabel(S.handover,{exact:true}).fill('2099-10-02T14:30');
 await submit(v);const f=(await v.calls('request_role'))[0]?.args.fields;t.ok(f?.projectCode==='OPS-7'&&f.ticket===42&&f.acknowledged===false&&f.neededOn==='2099-10-02'&&/T14:30:00[+-]/.test(f.handover),'typed form fields');t.ok(!('justification'in f)&&Object.keys(f).length===5,'empty/unlisted form item sent');
});
check('ac06.required','7.2 AC6','required field blocks submission and receives focus',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(S.required,{within:d}),'required error');t.ok(await v.isFocused(d.getByLabel(S.project,{exact:true})),'required focus');t.ok((await v.calls('request_role')).length===0,'required bypassed');
});
check('ac06.integer','7.2 AC6','integer field rejects fraction without a call',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);await form(v);await d.getByLabel(S.ticket,{exact:true}).fill('7.5');await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(S.whole,{within:d}),'integer error');t.ok((await v.calls('request_role')).length===0,'fraction sent');
});
for(const error of ['invalid-field','invalid-validity'])check(`ac07.${error}`,'7.2 AC7','server refusal restores draft and focuses the refused input',async t=>{
 const v=await catalog(t,'form',{tools:writeTools(error)}),d=await open(t,v);await form(v);await d.getByLabel(S.justification,{exact:true}).fill('Needed for reporting');await d.getByRole('radio',{name:S.days,exact:true}).check();await d.getByRole('radio',{name:'7 days',exact:true}).check();await submit(v);
 t.ok(await v.waitFor(error==='invalid-field'?S.invalidField:S.invalidValidity),'refusal not shown');const again=await open(t,v);
 t.ok(await again.getByLabel(S.project,{exact:true}).inputValue()==='OPS-7','project lost');t.ok(await again.getByLabel(S.justification,{exact:true}).inputValue()==='Needed for reporting','justification lost');t.ok(await again.getByRole('radio',{name:'7 days',exact:true}).isChecked(),'validity lost');
 if(error==='invalid-field'){const input=again.getByLabel(S.project,{exact:true});t.ok(await v.isFocused(input),'named field not focused');t.ok(await input.getAttribute('aria-invalid')==='true','field not marked');await input.fill('OPS-8');t.ok(await input.getAttribute('aria-invalid')!=='true','field mark not cleared');}else t.ok(await v.isFocused(again.getByRole('radio',{name:S.days,exact:true})),'validity not focused');
});
check('ac07.not-requestable','7.2 AC7','not-requestable shown with actions available',async t=>{
 const error=derive('invalid-field','not-requestable',M.error('not-requestable')),v=await catalog(t,'catalog',{tools:writeTools(error)});await open(t,v);await submit(v);t.ok(await v.waitFor(S.notRequestable),'guardrail missing');t.ok(await v.visible(v.button(S.requestLabel(rn))),'refused row lost action');
});
for(const outcome of ['pending','granted','preview'])check(`ac09.${outcome}`,'7.2 AC9','request outcome and requested badge follow the authoritative result',async t=>{
 const v=await catalog(t,outcome==='preview'?'dry-catalog':'catalog',{tools:writeTools(outcome)});await open(t,v,outcome==='preview'?S.previewLabel(rn):S.requestLabel(rn));await submit(v,outcome==='preview'?S.previewSubmit:S.submit);
 if(outcome==='pending'){t.ok(await v.waitFor(v.button(S.track)),'track missing');t.ok(await v.hasText(S.requested),'requested badge missing');}
 if(outcome==='preview'){t.ok(await v.waitFor(S.preview),'preview missing');t.ok(!(await v.hasText(S.requested)),'preview claimed requested');t.ok(await v.hasText(S.permanent),'preview summary missing');}
 if(outcome==='granted'){const r=fixture('granted').result.structuredContent.request;t.ok(await v.waitFor(S.granted(roleName(r.role),r.user.displayName)),'grant outcome');const warning=v.frame.getByRole('status').filter({hasText:S.granted(roleName(r.role),r.user.displayName)});t.ok(await warning.evaluate(el=>getComputedStyle(el).backgroundColor)==='rgb(255, 243, 205)','grant not warning');}
 t.ok((await v.calls('list_requestable_roles')).length===0,'write reread catalog');
});
check('ac09.track-context','7.2 AC9, 6.14','successful write sends tool text as model context; track is a name-only message',async t=>{
 const v=await catalog(t);await open(t,v);await submit(v);await v.button(S.track).click();const context=await v.sent('ui/update-model-context'),messages=await v.sent('ui/message');t.ok(context.length===1&&JSON.stringify(context[0].params.content)===JSON.stringify(fixture('pending').result.content),'wrong model context');t.ok(messages.length===1&&messages[0].params.content[0].text===S.handoff&&messages[0].params.role==='user','wrong handoff');
});
check('ac09.no-message','7.2 AC9','track absent without message capability',async t=>{
 const v=await catalog(t,'catalog',{caps:{serverTools:{}}});await open(t,v);await submit(v);t.ok(!(await v.visible(v.button(S.track))),'track visible without capability');t.ok((await v.sent('ui/update-model-context')).length===0,'context without capability');
});
check('ac09.case-fallback','7.2 Tools','one case read when a pending result has no approvers',async t=>{
 const v=await catalog(t,'catalog',{tools:writeTools(derive('pending','no-approvers',M.noApprovers))});await open(t,v);await submit(v);await sleep(250);t.ok((await v.calls('get_case')).length===1,'case fallback not once');
});
check('ac09.entry-outcome','7.2 outcome mode, 3.3','agent request entry never writes and reads catalog once',async t=>{
 const v=await catalog(t,'pending');t.ok(await v.waitFor(v.button(S.track)),'entry outcome absent');await sleep(200);t.ok((await v.calls('request_role')).length===0,'entry wrote again');t.ok((await v.calls('list_requestable_roles')).length===1,'entry list not once');t.ok(await v.dialog().count()===0,'entry opened confirm');
});
check('ac10.clamp','7.2 AC10, 6.12','two-line description has Show more/less and no kind label',async t=>{
 const v=await catalog(t,derive('catalog','long-description',M.long));const list=v.frame.getByRole('listitem').filter({has:v.button(S.requestLabel(rn))});t.ok(await v.waitFor(list.getByRole('button',{name:S.more,exact:true})),'no description expander');t.ok((await v.text(list)).includes('…'),'no ellipsis');await list.getByRole('button',{name:S.more,exact:true}).click();t.ok(await v.visible(list.getByRole('button',{name:S.less,exact:true})),'no Show less');t.ok(!(await v.text(list)).split('\n').includes('Role'),'kind label shown');
});
check('ac11.limit','7.2 AC11','limitReached explains catalog cutoff',async t=>{const v=await catalog(t,'limited');t.ok(await v.hasText(S.limit(2)),'limit hint missing')});
check('shared.header','shared, 6.2','resource-server has no identity line; personal mode explains account',async t=>{
 const v=await catalog(t);t.ok(!(await v.hasText('bstone')),'login leaked');t.ok(!(await v.hasText(S.personal(person()))),'resource-server identity line');
 const p=await catalog(t,derive('catalog','personal',M.personal));t.ok(await p.hasText(S.personal(person())),'personal explanation absent');
});
check('shared.fallback','shared, 6.2','missing acting identity calls whoami exactly once',async t=>{
 const v=await catalog(t,derive('catalog','no-acting',M.noActing));await sleep(200);t.ok((await v.calls('whoami')).length===1,'whoami not once');
});
check('shared.read-only','shared, 6.6','read-only hides calling controls, keeps local filtering, one informational banner',async t=>{
 const v=await catalog(t,'manager',{caps:noTools});t.ok(await v.hasText(S.readOnly),'read-only banner missing');t.ok(await v.frame.getByRole('group',{name:S.target,exact:true}).count()===0,'picker exposed');t.ok(!(await v.visible(v.button(S.refresh))),'refresh exposed');t.ok((await v.calls()).length===0,'read-only made calls');
 await v.frame.getByRole('searchbox',{name:S.search}).fill('missing');await sleep(700);t.ok((await v.calls()).length===0,'read-only search called');t.ok(await v.hasText(S.noMatch('missing')),'local filter absent');
 const dry=await catalog(t,'dry-catalog',{caps:noTools});t.ok(!(await dry.hasText(S.previewBanner)),'duplicate preview banner');
});
for(const decision of ['allowed','held','denied'])check(`shared.slot-${decision}`,'shared, 5','intermediary slot respected before server success',async t=>{
 const fx=derive('pending',`slot-${decision}`,M.slot(decision)),v=await catalog(t,'catalog',{tools:writeTools(fx)});await open(t,v);await submit(v);
 if(decision==='allowed'){t.ok(await v.hasText(S.requested),'allowed lost success');t.ok(!(await v.hasText('Reported by Policy service')),'allowed strip shown');}
 else {t.ok(await v.waitFor(decision==='held'?S.held:S.blocked),'slot panel absent');t.ok(!(await v.hasText(S.requested)),'blocked claimed success');t.ok((await v.sent('ui/update-model-context')).length===0,'blocked changed model context');}
});
check('shared.slot-invalid','shared, 5','malformed slot is ignored',async t=>{
 const fx=derive('catalog','invalid-slot',M.invalidSlot),v=await catalog(t,fx);t.ok(await v.visible(v.button(S.requestLabel(rn))),'invalid slot hid roles');
});
check('shared.refresh','shared, 6.6','Refresh keeps content dimmed, disables button and reuses entry arguments',async t=>{
 const v=await catalog(t,'limited',{tools:{list_requestable_roles:[{result:fixture('limited').result,hold:true}]}});await v.button(S.refresh).click();
 t.ok(await v.button(S.refreshing).isDisabled(),'refresh not disabled');t.ok(await v.visible(v.button(S.requestLabel(rn))),'snapshot disappeared');const c=(await v.calls('list_requestable_roles'))[0];t.ok(c.args.limit===2,'entry args not reused');
 t.ok(await v.frame.getByRole('list').evaluate(el=>!!el.closest('[aria-busy="true"]')),'no aria-busy');await v.release();t.ok(await v.waitFor(v.button(S.refresh)),'refresh never finished');
});
check('shared.refresh-error','shared, 6.8','failed Refresh retains snapshot and shows stable error code',async t=>{
 const error=derive('invalid-field','unavailable',M.error('midpoint-unavailable')),v=await catalog(t,'catalog',{tools:listTools(error)});await v.button(S.refresh).click();t.ok(await v.waitFor(S.unavailable),'refresh error missing');t.ok(await v.visible(v.button(S.requestLabel(rn))),'snapshot lost on error');
});
check('shared.rpc-error','shared, 6.8','JSON-RPC refusal shown after confirm without retry',async t=>{
 const v=await catalog(t,'catalog',{tools:{request_role:[{rpcError:{code:-32000,message:'Host refused'}}]}});await open(t,v);await submit(v);t.ok(await v.waitFor(S.hostRefused),'host error missing');await sleep(200);t.ok((await v.calls('request_role')).length===1,'write retried');
});
check('shared.dialog-keyboard','shared, 6.5','focus starts on validity, traps Tab, Escape returns to opener',async t=>{
 const v=await catalog(t),d=await open(t,v);const first=d.getByRole('radio',{name:S.permanent});t.ok(await v.isFocused(first),'initial focus wrong');await first.press('Shift+Tab');t.ok(await v.isFocused(d.getByRole('button',{name:S.submit})),'reverse tab escaped');await d.getByRole('button',{name:S.submit}).press('Tab');t.ok(await v.isFocused(first),'tab escaped');await first.press('Escape');t.ok(await v.waitGone(d),'Escape not close');t.ok(await v.isFocused(v.button(S.requestLabel(rn))),'focus not returned');
});
check('shared.one-write','shared, 6.5','working dialog prevents double submissions and Escape; no timeout',async t=>{
 const v=await catalog(t,'catalog',{tools:writeTools('pending',{hold:true})}),d=await open(t,v);await d.getByRole('button',{name:S.submit}).click();t.ok(await d.getByRole('button',{name:S.working}).isDisabled(),'working enabled');t.ok(await d.getByRole('button',{name:S.cancel}).isDisabled(),'cancel enabled');await d.press('Escape');t.ok(await v.visible(d),'Escape closed working dialog');await sleep(8300);t.ok(await v.hasText(S.slow,{within:d}),'slow notice absent');t.ok(await v.visible(d),'view timed out');t.ok((await v.calls('request_role')).length===1,'multiple writes');await v.release();await v.waitGone(d);
});
check('shared.announcements','shared, 6.12','success is polite and write errors assertive',async t=>{
 const v=await catalog(t);await open(t,v);await submit(v);await sleep(150);t.ok(await inLiveRegion(v.frame,'Request sent.','polite'),'success not announced');
 const e=await catalog(t,'catalog',{tools:writeTools('invalid-validity')});await open(t,e);await submit(e);await sleep(150);t.ok(await inLiveRegion(e.frame,S.invalidValidity,'assertive'),'error not announced');
});
check('shared.loading-cancel','shared, 6.7','tool input loads skeleton, slow notice then cancellation',async t=>{
 const v=await t.open({entry:fixture('catalog'),deliver:'input-only'});t.ok(await v.waitFor(S.loading),'loading absent');await sleep(8300);t.ok(await v.hasText(S.slow),'slow loading absent');await v.notify('ui/notifications/tool-cancelled',{});t.ok(await v.waitFor(S.cancelled),'cancel absent');t.ok((await v.calls('request_role')).length===0,'cancelled wrote');
});
for(const [id,mutation,expected] of [['mismatch',M.unknownServer,S.mismatch],['text',M.textOnly,S.textOnly]])check(`shared.${id}`,'shared, 6.7','unrenderable result falls back to text',async t=>{
 const v=await t.open({entry:derive('catalog',id,mutation)});t.ok(await v.waitFor(expected),'fallback absent');t.ok((await v.calls('request_role')).length===0,'fallback writes');
});
check('shared.empty','shared, 7.2 states','empty catalog explains why',async t=>{const v=await catalog(t,derive('catalog','empty',M.empty));t.ok(await v.hasText(S.empty)&&await v.hasText(S.emptyWhy),'empty explanation missing')});
check('shared.safe-text','shared, 9','untrusted description renders as text without DOM injection',async t=>{
 const v=await catalog(t,derive('catalog','hostile',M.hostile));t.ok(await v.hasText('<img src=x onerror=alert(1)>',{loose:true}),'hostile text missing');t.ok(await v.frame.getByRole('img').count()===0,'markup created element');
});
check('shared.hidden-name','shared, 4.5','missing role name is human text, never OID',async t=>{
 const v=await catalog(t,derive('catalog','hidden',M.hidden));t.ok(await v.visible(v.button(S.requestLabel(S.hidden))),'hidden role label');t.ok(!/20000000-/.test(await v.surfaceStrings()),'OID on surface');
});
check('shared.preview-details','shared, 6.7','preview raw request stays behind technical details',async t=>{
 const v=await catalog(t,'dry-catalog',{tools:writeTools('preview')});await open(t,v,S.previewLabel(rn));await submit(v,S.previewSubmit);t.ok(!/20000000-|PATCH/.test(await v.surfaceStrings()),'preview exposes raw request');await v.button(S.technical).click();t.ok(await v.hasText('PATCH /ws/rest/users/',{loose:true}),'technical request absent');
});
check('shared.paging','shared, 6.13','ten rows then ten more without server call',async t=>{
 const v=await catalog(t,derive('catalog','many',M.many));t.ok(await v.frame.getByRole('listitem').count()===10,'initial page not 10');await v.button(S.more).click();t.ok(await v.frame.getByRole('listitem').count()===20,'second page not 20');t.ok((await v.calls('list_requestable_roles')).length===0,'paging called server');
});
check('shared.echo','shared, 3.5','duplicate result notifications are idempotent and foreign results ignored',async t=>{
 const v=await catalog(t,'catalog',{echo:true});await open(t,v);await submit(v);await v.notify('ui/notifications/tool-result',fixture('catalog').result);await v.notify('ui/notifications/tool-result',fixture('pending').result);await v.notify('ui/notifications/tool-result',fixture('team').result);await sleep(150);t.ok(await v.hasText(S.requested),'echo undid requested status');t.ok((await v.calls('request_role')).length===1,'echo wrote');
});
check('shared.bridge','shared, 3.4','handshake and teardown follow stable bridge protocol',async t=>{
 const v=await catalog(t);const init=(await v.sent('ui/initialize'))[0];t.ok(init.params.protocolVersion==='2026-01-26'&&init.params.appInfo.name==='midpoint-request-access','handshake mismatch');
 const result=await v.request('ui/resource-teardown',{});t.ok(result&&!result.error,'teardown failed');t.ok((await v.sent('ui/notifications/size-changed')).length>0,'no size reporting');
});
check('shared.theme','shared, 6.9','host theme and permitted font variables apply',async t=>{
 const v=await catalog(t,'catalog',{context:{styles:{variables:{'--font-sans':'Georgia','--mp-primary':'hotpink'}}}});await v.setTheme('dark');await sleep(100);const style=await v.frame.evaluate(()=>({theme:document.documentElement.dataset.theme,font:document.documentElement.style.getPropertyValue('--font-sans'),primary:document.documentElement.style.getPropertyValue('--mp-primary')}));t.ok(style.theme==='dark'&&style.font==='Georgia'&&!style.primary,'host style filtering');
});
for(const width of [320,720])check(`shared.width-${width}`,'shared, 6.1, 6.12','catalog and form stay within viewport at narrow widths',async t=>{
 const v=await catalog(t,'form',{width,context:{containerDimensions:{width,maxHeight:1600}}});await open(t,v);await sleep(100);const m=await v.frame.evaluate(()=>({sw:document.documentElement.scrollWidth,cw:document.documentElement.clientWidth}));t.ok(m.sw<=m.cw+1,'horizontal overflow');await t.shot(v,`form-${width}`);
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


for(const theme of ['light','dark'])check(`shared.contrast-${theme}`,'shared, 6.9, 6.12','text meets AA contrast in catalog and request form',async t=>{
 const v=await catalog(t,'form',{context:{theme}});await open(t,v);await sleep(200);const issues=await contrastIssues(v);t.ok(issues.length===0,JSON.stringify(issues.slice(0,5)));
});
const viewConsole = (d) => d.console.filter((m) => m.type === 'error' && (m.url === '' || m.url.startsWith(VIEW_URL)));

across('shared.no-network', '7.2 AC12, 3.2, 3.6', 'the view made no network request (routes, CSP, network APIs)', (t) => {
  for (const d of diagnostics) {
    const reqs = d.routed.filter((r) => r.frameUrl.startsWith(VIEW_URL));
    if (reqs.length) t.fail(`${d.check}: requests ${JSON.stringify(reqs.slice(0, 3))}`);
    if (d.inst?.network?.length) t.fail(`${d.check}: ${JSON.stringify(d.inst.network.slice(0, 3).map((n) => `${n.api} ${n.target}`))}`);
    if (d.inst?.csp?.length) t.fail(`${d.check}: CSP violations ${JSON.stringify(d.inst.csp.slice(0, 3))}`);
    if (d.inst?.popups?.length) t.fail(`${d.check}: window.open ${JSON.stringify(d.inst.popups)}`);
  }
});

across('shared.no-console-errors', '7.2 AC12', 'no console errors or uncaught exceptions in the view', (t) => {
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
across('ac15.no-timers', '7.2 AC12, 6.6, 6.5 (D27)', 'no setInterval; no timer longer than the 8 s slow notice', (t) => {
  for (const d of diagnostics) {
    if (!d.inst) continue;
    if (d.inst.intervals.length) t.fail(`${d.check}: setInterval(${d.inst.intervals[0].ms}) at ${d.inst.intervals[0].stack}`);
    const long = d.inst.timeouts.filter((x) => x.ms > SLOW_MAX_MS);
    if (long.length) t.fail(`${d.check}: setTimeout(${long[0].ms}) at ${long[0].stack}`);
  }
  if (!diagnostics.some((d) => d.inst)) t.fail('the instrumentation never loaded in a view frame');
});

across('bridge.protocol', '3.3, 3.4, 7.2 Tools', 'only allowed tools and arguments, capabilities respected, every call anticipated', (t) => {
  for (const d of diagnostics) {
    for (const x of d.state?.violations ?? []) t.fail(`${d.check}: ${x}`);
    for (const u of d.state?.unanswered ?? []) t.fail(`${d.check}: unexpected call ${u.name} ${JSON.stringify(u.args)}`);
  }
});
check('ac02.typing-while-switching','7.2 AC2, AC3','typing during a target read cannot enable old-target actions',async t=>{
 const report=fixture('report'),v=await catalog(t,'manager',{tools:{list_requestable_roles:[{result:report.result,hold:true}]}});
 await sleep(150);await v.frame.getByRole('group',{name:S.target,exact:true}).getByRole('radio',{name:person(),exact:true}).check();
 await v.frame.getByRole('searchbox',{name:S.search}).fill('database');
 t.ok(await v.button(S.reportLabel(rn,person())).isDisabled(),'old target became actionable');await v.release();await sleep(150);
 t.ok(!(await v.button(S.reportLabel(rn,person())).isDisabled()),'new target stayed disabled');
 await open(t,v,S.reportLabel(rn,person()));await submit(v);const sent=(await v.calls('request_role'))[0]?.args;t.ok(sent?.userOid===report.call.arguments.forUser,'wrong requestee');t.ok(sent?.userName===report.result.structuredContent.forUserRef.name,'requestee name not sent');
});
check('ac02.failed-switch','7.2 AC2, 6.8','failed target read retains the snapshot but cannot request from the wrong catalog',async t=>{
 const report=fixture('report'),error=derive('invalid-field','target-refused',M.error('not-authorized')),v=await catalog(t,'manager',{tools:listTools(error)});
 await sleep(150);await v.frame.getByRole('group',{name:S.target,exact:true}).getByRole('radio',{name:person(),exact:true}).check();t.ok(await v.waitFor(S.denied),'target error absent');
 t.ok(await v.button(S.reportLabel(rn,person())).isDisabled(),'old target actionable after failed switch');
});
check('ac07.drop-target-draft','7.2 refused inputs','target changes discard refused drafts and outcome notice',async t=>{
 const base=derive('form','manager-form',M.managerForm);
 const report=derive('report','report-form',M.reportForm);
 const v=await catalog(t,base,{tools:{...writeTools('invalid-field'),list_requestable_roles:[{when:{forUser:report.call.arguments.forUser},result:report.result},{result:base.result}]}});
 await open(t,v);await form(v);await submit(v);await sleep(150);
 const picker=v.frame.getByRole('group',{name:S.target,exact:true});await picker.getByRole('radio',{name:person(),exact:true}).check();await sleep(150);await picker.getByRole('radio',{name:S.self,exact:true}).check();await sleep(150);const d=await open(t,v);
 t.ok(await d.getByLabel(S.project,{exact:true}).inputValue()==='','draft retained across target switch');t.ok(!(await v.hasText(S.invalidField,{within:d})),'old refusal retained');
});
check('ac07.drop-success-draft','7.2 refused inputs','a successful request discards a refused draft',async t=>{
 const v=await catalog(t,'form',{tools:{request_role:[{result:fixture('invalid-field').result,times:1},{result:fixture('pending').result}],...listTools(fixture('form'))}});
 await open(t,v);await form(v);await submit(v);await open(t,v);await submit(v);await v.button(S.refresh).click();await sleep(150);const d=await open(t,v);t.ok(await d.getByLabel(S.project,{exact:true}).inputValue()==='','draft retained after success');
});
check('ac03.target-search','7.2 AC3','target switches preserve an active midPoint query',async t=>{
 const found=derive('manager','manager-search',M.search('zebra'));
 const initial=derive('manager','manager-cutoff',M.cutoff);
 const report=derive('report','report-search',M.search('zebra'));
 const v=await catalog(t,initial,{tools:{list_requestable_roles:[{when:{forUser:report.call.arguments.forUser},result:report.result},{result:found.result}]}});
 await v.frame.getByRole('searchbox',{name:S.search}).fill('zebra');await v.waitFor(S.results('zebra'));await v.frame.getByRole('group',{name:S.target,exact:true}).getByRole('radio',{name:person(),exact:true}).check();await sleep(100);
 const a=(await v.calls('list_requestable_roles')).at(-1)?.args;t.ok(a.query==='zebra'&&a.forUser===report.call.arguments.forUser,'active query dropped');
});
check('shared.no-polling','shared, 6.6','idle, focus and visibility cause no reads',async t=>{
 const v=await catalog(t);await sleep(150);const count=(await v.calls()).length;
 await v.frame.evaluate(()=>{window.dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'))});await sleep(1000);
 t.ok((await v.calls()).length===count,'background read');
});
check('shared.200-percent','shared, 6.12','200 percent text remains reachable without horizontal overflow',async t=>{
 const v=await catalog(t,'form',{width:320});await v.frame.evaluate(()=>{for(const el of document.querySelectorAll('style'))el.textContent+='\n.mp{font-size:28px}.mp-input,.mp-textarea,.mp-btn,.mp-field label{font-size:28px}'});await open(t,v);await sleep(100);
 const d=v.dialog();t.ok(await v.visible(d.getByRole('button',{name:S.submit})),'submit disappeared at 200 percent');const sizes=await v.frame.evaluate(()=>({w:document.documentElement.clientWidth,s:document.documentElement.scrollWidth}));t.ok(sizes.s<=sizes.w+1,'horizontal overflow at 200 percent');
});
check('ac05.zone-dst','7.2 AC5, 6.11','custom dates use different offsets across a host-zone clock change',async t=>{
 const v=await catalog(t,'catalog',{context:{timeZone:'Europe/Prague'}}),d=await open(t,v);await d.getByRole('radio',{name:S.custom}).check();
 await d.getByLabel(S.from,{exact:true}).fill('2099-10-01');await d.getByLabel(S.to,{exact:true}).fill('2099-10-31');await submit(v);const a=(await v.calls('request_role'))[0]?.args;
 t.ok(a?.validFrom==='2099-10-01T00:00:00+02:00'&&a?.validTo==='2099-10-31T23:59:59+01:00','DST offsets not independently computed');
});
check('ac09.no-approvers','7.2 AC9','pending without readable approvers keeps generic success',async t=>{
 const pending=derive('pending','no-approvers',M.noApprovers),cs=derive('case','no-case-approvers',M.noCaseApprovers);
 const v=await catalog(t,'catalog',{tools:{...writeTools(pending),get_case:[{result:cs.result}]}});await open(t,v);await submit(v);t.ok(await v.waitFor(S.pending),'generic pending missing');
});
check('shared.dialog-size','shared, 6.13','dialog grows flexible content and scrolls inside a fixed-height host',async t=>{
 for(const dim of [{width:720,maxHeight:1600},{width:720,height:380}]){
  const v=await catalog(t,'form',{context:{containerDimensions:dim}}),d=await open(t,v);await sleep(150);
  const m=await d.evaluate(el=>{const b=el.getBoundingClientRect(),r=el.parentElement.parentElement.getBoundingClientRect();return {top:b.top,bottom:b.bottom,root:r.bottom,height:r.height,scroll:getComputedStyle(el).overflowY}});
  t.ok(m.bottom<=m.root+1,'dialog extends beyond its painted area');if(dim.height)t.ok(m.height===dim.height&&m.scroll==='auto','fixed height not respected');else t.ok(m.height>600,'flexible dialog did not grow');
 }
});
check('shared.unknown-error-code','shared, 6.8','an unknown error code uses text fallback even when it names a JS property',async t=>{
 const error=derive('invalid-field','unknown-code',M.error('toString'));
 const v=await catalog(t,'catalog',{tools:writeTools(error)});await open(t,v);await submit(v);t.ok(await v.waitFor(S.invalidField),'unknown-code fallback failed');
});
