import {S} from './strings.mjs';
import {fixture,derive,mutations as M,role,roleName,person,dateIn,endOfDayIn,EU,LEAD,APP} from './derive.mjs';
import {defaults,diagnostics,VIEW_URL,inLiveRegion,sleep} from '../harness.mjs';
export const checks=[],runWide=[];
const check=(id,criterion,title,run)=>checks.push({id,criterion,title,run});
const across=(id,criterion,title,run)=>runWide.push({id,criterion,title,run});
defaults.allowedTools={list_requestable_roles:['limit','forUser','query'],request_role:['roleOid','roleName','userOid','userName','relation','validFrom','validTo','fields'],list_request_targets:['query','limit'],list_my_team:['limit'],list_my_managers:['limit'],get_case:['oid'],whoami:[]};
defaults.tools={whoami:[{result:fixture('identity').result}],list_my_managers:[{result:fixture('managers').result}],list_my_team:[{result:fixture('team').result}],list_requestable_roles:[{result:fixture('catalog').result}],get_case:[{result:fixture('case').result}],request_role:[{result:fixture('pending').result}],list_request_targets:[{result:fixture('targets-self').result}]};
const rn=roleName(role());
async function catalog(t,name='catalog',opts={}){const v=await t.open({entry:typeof name==='string'?fixture(name):name,...opts});t.ok(await v.waitFor(v.frame.getByRole('heading',{name:S.title,exact:true})),'Get access did not load');return v;}
async function open(t,v,label=S.requestLabel(rn)){await v.button(label).click();t.ok(await v.waitFor(v.dialog()),'no confirm dialog');return v.dialog();}
async function submit(v,label=S.submit){await v.dialog().getByRole('button',{name:label,exact:true}).click();await v.waitGone(v.dialog());}
async function form(v){await v.dialog().getByLabel(S.project,{exact:true}).fill('OPS-7');}
const listTools=fx=>({list_requestable_roles:[{result:fx.result}]});
const writeTools=(name,extra={})=>({request_role:[{result:(typeof name==='string'?fixture(name):name).result,...extra}]});
const noTools={message:{},updateModelContext:{},openLinks:{}};

check('ac01.self','7.2 AC1','non-managers see only self, named in the list heading; manager hint fetched once',async t=>{
 const v=await catalog(t);t.ok(await v.visible(v.frame.getByRole('heading',{name:S.listTitle(2),exact:true})),'self heading absent');t.ok(await v.frame.getByRole('group',{name:S.target,exact:true}).count()===0,'non-manager picker');t.ok(await v.waitFor(S.managerHint('Jane Doe')),'manager hint absent');
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
check('ac06.form','7.2 AC6, D43','every field type renders, a checkbox has no required mark, other fields are named, and only filled values are sent',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);
 t.ok(await d.getByLabel(S.justification,{exact:true}).evaluate(el=>el.tagName==='INPUT'&&el.type==='text'),'string is not a text input');
 t.ok(await d.getByLabel(S.needed,{exact:true}).getAttribute('type')==='date','date type');t.ok(await d.getByLabel(S.handover,{exact:true}).getAttribute('type')==='datetime-local','datetime type');
 t.ok(await d.getByLabel(S.ack,{exact:true}).getAttribute('type')==='checkbox','checkbox missing, or marked required');
 const level=d.getByLabel(S.level,{exact:true}),region=d.getByLabel(S.region,{exact:true}),envs=d.getByLabel(S.environments,{exact:true});
 t.ok(await level.evaluate(el=>el.tagName==='SELECT'&&[...el.options].map(o=>o.textContent).join('|')==='Choose…|Read only|Read and write'),'enumeration is not a select of its labels');
 t.ok(await region.evaluate(el=>[...el.options].map(o=>o.value+'='+o.textContent).join('|')==='=Choose…|eu=Europe|us=United States|apac=apac'),'lookup table is not a select of its rows');
 t.ok(await envs.evaluate(el=>el.tagName==='TEXTAREA'),'multiple field is not a list');t.ok(await v.hasText(S.onePerLine,{within:d}),'one-per-line note absent');
 t.ok(await v.hasText(S.otherFields('Sponsor'),{within:d}),'fields only midPoint can fill are not named');
 await form(v);await d.getByLabel(S.ticket,{exact:true}).fill('42');await d.getByLabel(S.needed,{exact:true}).fill('2099-10-02');await d.getByLabel(S.handover,{exact:true}).fill('2099-10-02T14:30');
 await level.selectOption('write');await region.selectOption('eu');await envs.fill('dev\n\n test \n');await d.getByLabel(S.cost,{exact:true}).fill('0.25');
 await submit(v);const f=(await v.calls('request_role'))[0]?.args.fields;
 t.ok(f?.projectCode==='OPS-7'&&f.ticket===42&&f.acknowledged===false&&f.neededOn==='2099-10-02'&&/T14:30:00[+-]/.test(f.handover),'typed form fields');
 t.ok(f?.accessLevel==='write'&&f.region==='eu'&&JSON.stringify(f.environments)==='["dev","test"]'&&f.costShare===0.25,'choice, list and decimal values');
 t.ok(f&&!('justification'in f)&&Object.keys(f).length===9,'an empty form item was sent');
});
check('ac06.lists','7.2 AC6, D43','a multiple choice is one checkbox per option sent as a list, a required one needs a tick, and each listed value is checked',async t=>{
 const v=await catalog(t,derive('form','lists',M.lists)),d=await open(t,v);
 const group=d.getByRole('group',{name:S.level+' (required)',exact:true});t.ok(await group.getByRole('checkbox').count()===2,'not one checkbox per option');
 await form(v);await d.getByLabel(S.ticket,{exact:true}).fill('1\n2.5');await d.getByRole('button',{name:S.submit}).click();
 t.ok(await v.hasText(S.required.replace('Project code','Access level'),{within:d}),'required multiple choice accepted empty');t.ok(await v.hasText(S.whole,{within:d}),'a fraction in a whole-number list accepted');
 t.ok((await v.calls('request_role')).length===0,'sent with errors');
 await group.getByRole('checkbox',{name:'Read only',exact:true}).check();await group.getByRole('checkbox',{name:'Read and write',exact:true}).check();await d.getByLabel(S.ticket,{exact:true}).fill('1\n2');
 await submit(v);const f=(await v.calls('request_role'))[0]?.args.fields;
 t.ok(JSON.stringify(f?.accessLevel)==='["read","write"]'&&JSON.stringify(f?.ticket)==='[1,2]','lists not sent');
});
check('ac06.decimal','7.2 AC6, D43','a decimal field refuses text',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);await form(v);await d.getByLabel(S.cost,{exact:true}).fill('half');await d.getByRole('button',{name:S.submit}).click();
 t.ok(await v.hasText(S.number,{within:d}),'number error absent');t.ok((await v.calls('request_role')).length===0,'sent');
});
check('ac06.required','7.2 AC6','required field blocks submission and receives focus',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(S.required,{within:d}),'required error');t.ok(await v.isFocused(d.getByLabel(S.project,{exact:true})),'required focus');t.ok((await v.calls('request_role')).length===0,'required bypassed');
});
check('ac06.integer','7.2 AC6','integer field rejects fraction without a call',async t=>{
 const v=await catalog(t,'form'),d=await open(t,v);await form(v);await d.getByLabel(S.ticket,{exact:true}).fill('7.5');await d.getByRole('button',{name:S.submit}).click();t.ok(await v.hasText(S.whole,{within:d}),'integer error');t.ok((await v.calls('request_role')).length===0,'fraction sent');
});
for(const [name,sentence,reason] of [['refused',S.refused,'Requests for this role need a justification.'],['refused-not-authorized',S.denied,"User 'bstone' not authorized for operation with assignment on bstone with target Database admin"]])check(`d42.${name}`,'7.2, 6.8, D42',"a midPoint refusal shows its sentence and midPoint's own reason, and announces both",async t=>{
 const v=await catalog(t,'catalog',{tools:writeTools(name)});await open(t,v);await submit(v);
 t.ok(await v.waitFor(sentence),'sentence absent');t.ok(await v.waitFor(S.reason(reason)),'reason absent');await sleep(150);
 t.ok(await inLiveRegion(v.frame,sentence+' '+S.reason(reason),'assertive'),'reason not announced');
 t.ok(!(await v.frame.locator('body').innerText()).includes('20000000-0000-0000-0000-0000000000f1'),'an OID shown outside the details');
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
 if(outcome==='granted'){const r=fixture('granted').result.structuredContent.request;t.ok(await v.waitFor(S.granted(roleName(r.role),r.user.displayName)),'grant outcome');const warning=v.frame.getByRole('status').filter({hasText:S.granted(roleName(r.role),r.user.displayName)});t.ok(await warning.evaluate(el=>getComputedStyle(el).backgroundColor)==='rgb(255, 243, 205)','grant not warning');t.ok(await v.hasText(S.grantedPill)&&!(await v.hasText(S.requested)),'granted row not marked granted');}
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
check('ac10.ledger','7.2 AC10, 6.13 (D39)','full screen lists one row per role with its risk and Request button, report heading names the person',async t=>{
 const v=await catalog(t,'catalog',{context:{displayMode:'fullscreen'},width:860});const rows=v.frame.getByRole('listitem');t.ok(await rows.count()===2,'not one row per role');
 const row=rows.filter({has:v.button(S.requestLabel(rn))});t.ok(await row.count()===1,'row lost its Request button');t.ok((await row.textContent()).includes(S.risk(role().riskLevel)),'risk words lost');t.ok(!(await v.text(row)).split('\n').includes('Role'),'kind label shown');
 const report=fixture('report'),m=await catalog(t,'manager',{context:{displayMode:'fullscreen'},width:860,tools:listTools(report)});await sleep(150);await m.frame.getByRole('group',{name:S.target,exact:true}).getByRole('radio',{name:person(),exact:true}).check();
 t.ok(await m.waitFor(m.frame.getByRole('heading',{name:S.listTitle(report.result.structuredContent.roles.length,person()),exact:true})),'report heading does not name the person');
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

// draft.14 (D44, D45): what midPoint's request rules offer. Names and rules come from the fixtures.
const offered=(fx,name)=>fixture(fx).result.structuredContent.roles.find(r=>r.name===name);
const RM=offered('rules-manager','release-manager'),DB=offered('rules-manager','db-admin');
const BOB=fixture('rules-report').call.arguments.forUser,bob=fixture('rules-report').result.structuredContent.forUserRef.displayName,jane=fixture('rules-manager').result.structuredContent.acting.fullName;
const picker=v=>v.frame.getByRole('group',{name:S.target,exact:true});
const group=(d,name)=>d.getByRole('group',{name,exact:true});
const janeTools=(more={})=>({list_request_targets:[{result:fixture('targets').result}],...more});
const approver=S.outcomeRelation(S.relApprover.toLowerCase());
check('d44.targets','7.2 draft.14 (1), D44','with the rules, "Request for" lists list_request_targets once instead of list_my_team, the acting person as Myself; choosing a person re-reads with forUser',async t=>{
 const report=fixture('rules-report'),v=await catalog(t,'rules-manager',{tools:janeTools({list_requestable_roles:[{when:{forUser:BOB},result:report.result}]})}),p=picker(v);
 t.ok(await v.waitFor(p.getByRole('radio',{name:bob,exact:true})),'person of the rules not offered');t.ok(await p.getByRole('radio',{name:S.self,exact:true}).isChecked(),'Myself not chosen');
 t.ok(await p.getByRole('radio',{name:jane,exact:true}).count()===0,'acting person listed by name');t.ok(await v.frame.getByRole('searchbox',{name:S.findPerson}).count()===0,'person search for two people');
 const asked=await v.calls('list_request_targets');t.ok(asked.length===1&&JSON.stringify(asked[0].args)==='{"limit":100}','targets not asked once with limit 100');t.ok((await v.calls('list_my_team')).length===0,'list_my_team called with the rules');
 await p.getByRole('radio',{name:bob,exact:true}).check();
 t.ok(await v.waitFor(v.frame.getByRole('heading',{name:S.listTitle(report.result.structuredContent.roles.length,bob),exact:true})),'catalog for the person not shown');
 const calls=await v.calls('list_requestable_roles');t.ok(calls.length===1&&calls[0].args.forUser===BOB&&calls[0].args.limit===100,'not re-read with forUser');t.ok((await v.calls('list_request_targets')).length===1,'targets asked again');
});
check('d44.targets-self','7.2 draft.14 (1)','with the rules and nobody else to request for, there is no picker and the heading names the person',async t=>{
 const v=await catalog(t,'rules');await sleep(200);t.ok((await v.calls('list_request_targets')).length===1,'targets not asked once');t.ok((await v.calls('list_my_team')).length===0,'team called');
 t.ok(await picker(v).count()===0,'picker for one person');t.ok(await v.visible(v.frame.getByRole('heading',{name:S.listTitle(2),exact:true})),'self heading absent');
});
check('d44.target-search','7.2 draft.14 (1)','a cut-off list of people adds "Find a person": a 600 ms pause and 2+ characters ask list_request_targets with query, once',async t=>{
 const carol={oid:'10000000-0000-0000-0000-0000000000d9',name:'cdiaz',displayName:'Carol Diaz',type:'User',because:[LEAD]};
 const base=derive('targets','targets-cut',M.targetsCut),found=derive('targets','targets-carol',M.targetsFound([carol]));
 const v=await catalog(t,'rules-manager',{tools:{list_request_targets:[{when:{query:'car'},result:found.result},{result:base.result}]}}),s=v.frame.getByRole('searchbox',{name:S.findPerson,exact:true});
 t.ok(await v.waitFor(s),'no person search');await s.fill('c');await sleep(720);t.ok((await v.calls('list_request_targets')).length===1,'one character asked');
 await s.fill('ca');await sleep(200);await s.fill('car');await sleep(200);t.ok((await v.calls('list_request_targets')).length===1,'asked before the pause');
 t.ok(await v.waitFor(picker(v).getByRole('radio',{name:carol.displayName,exact:true})),'found person not offered');
 const calls=await v.calls('list_request_targets');t.ok(calls.length===2&&calls[1].args.query==='car'&&calls[1].args.limit===100,'search not asked once with query');
 t.ok(await s.inputValue()==='car'&&await v.isFocused(s),'search text or focus lost');
});
check('d44.relation','7.2 draft.14 (2, 6), D45','a role offered as approver only shows the relation choice and sends relation, and the outcome names it; a member-only role shows no choice and sends none',async t=>{
 const v=await catalog(t,'rules-manager',{tools:janeTools(writeTools('pending-approver'))});let d=await open(t,v,S.requestLabel(roleName(RM)));const g=group(d,S.relation),r=g.getByRole('radio',{name:S.relApprover,exact:true});
 t.ok(await v.visible(g),'no relation choice');t.ok(await g.getByRole('radio').count()===1,'not one option per offer');t.ok(await r.isChecked(),'the only relation not chosen');t.ok(await v.isFocused(r),'focus not on the first input');
 await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a?.relation==='approver'&&a.roleOid===RM.oid&&!('userOid' in a),'relation not sent');t.ok(await v.waitFor(approver),'outcome does not name the relation');
 d=await open(t,v,S.requestLabel(roleName(DB)));t.ok(await group(d,S.relation).count()===0,'relation choice for a member-only role');t.ok(await v.visible(group(d,S.validity)),'dates hidden though offered');
 await submit(v);const b=(await v.calls('request_role'))[1]?.args;t.ok(b&&b.roleOid===DB.oid&&!('relation' in b),'relation sent for member');
});
check('d44.offer-follows','7.2 draft.14 (2-4), D45','fields, dates and rules follow the chosen relation; a relation midPoint refuses shows error.notRequestable',async t=>{
 const fx=derive('form','rules-two-offers',M.rules({0:[{relation:'default',allFields:true,validity:true,because:[EU]},{relation:'approver',fields:['projectCode'],because:[APP]}]}));
 const v=await catalog(t,fx,{tools:writeTools('relation-refused')}),d=await open(t,v),g=group(d,S.relation);
 t.ok(await g.getByRole('radio').count()===2,'not one option per offer');t.ok(await g.getByRole('radio',{name:S.relDefault,exact:true}).isChecked(),'member not preselected');
 t.ok(await v.visible(group(d,S.validity))&&await v.visible(d.getByLabel(S.justification,{exact:true})),'member offer lost its dates or fields');
 await d.getByRole('button',{name:S.why,exact:true}).click();t.ok(await v.hasText(EU,{within:d})&&!(await v.hasText(APP,{within:d})),'member rules not listed');
 await g.getByRole('radio',{name:S.relApprover,exact:true}).check();
 t.ok(await group(d,S.validity).count()===0,'dates offered for approver');t.ok(await d.getByLabel(S.justification,{exact:true}).count()===0&&await v.visible(d.getByLabel(S.project,{exact:true})),'approver fields not narrowed');
 t.ok(await v.hasText(APP,{within:d})&&!(await v.hasText(EU,{within:d})),'rules did not follow the relation');t.ok(await v.hasText(S.permanent,{within:d}),'summary not "No end date"');
 await form(v);await submit(v);const a=(await v.calls('request_role'))[0]?.args;
 t.ok(a?.relation==='approver'&&!('validTo' in a)&&!('validFrom' in a)&&JSON.stringify(a.fields)==='{"projectCode":"OPS-7"}','approver request arguments');t.ok(await v.waitFor(S.notRequestable),'refused relation not shown');
});
check('d44.fields','7.2 draft.14 (3)','only the fields an offer names are shown and sent; an offer without fields or dates has neither, and focus starts on Cancel',async t=>{
 const fx=derive('form','rules-fields',M.rules({0:[{relation:'default',fields:['projectCode'],validity:true,because:[EU]}],1:[{relation:'default',because:[EU]}]}));
 const v=await catalog(t,fx);let d=await open(t,v);t.ok(await v.visible(d.getByLabel(S.project,{exact:true})),'offered field missing');
 for(const l of [S.justification,S.ticket,S.ack,S.needed,S.handover,S.level,S.region,S.environments,S.cost])t.ok(await d.getByLabel(l,{exact:true}).count()===0,`field not offered shown: ${l}`);
 await form(v);await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(JSON.stringify(a?.fields)==='{"projectCode":"OPS-7"}','fields outside the offer sent');
 d=await open(t,v,S.requestLabel('Finance reports'));t.ok(await d.getByRole('heading',{name:S.form,exact:true}).count()===0,'form for an offer without fields');t.ok(await group(d,S.validity).count()===0,'dates for an offer without them');
 t.ok(await v.isFocused(d.getByRole('button',{name:S.cancel,exact:true})),'focus not on Cancel');
 await submit(v);const b=(await v.calls('request_role'))[1]?.args;t.ok(b&&Object.keys(b).join(',')==='roleOid,roleName','extra arguments sent');
});
check('d44.no-dates','7.2 draft.14 (3)','for a person the team-lead rule allows without dates: no validity group, the summary says No end date, and no dates are sent',async t=>{
 const report=fixture('rules-report'),v=await catalog(t,'rules-manager',{tools:janeTools({list_requestable_roles:[{when:{forUser:BOB},result:report.result}]})}),p=picker(v);
 await v.waitFor(p.getByRole('radio',{name:bob,exact:true}));await p.getByRole('radio',{name:bob,exact:true}).check();
 const label=S.reportLabel(roleName(DB),bob);t.ok(await v.waitFor(v.button(label)),'catalog for the person absent');
 const d=await open(t,v,label);t.ok(await group(d,S.validity).count()===0,'validity group shown');t.ok(await v.hasText(S.permanent,{within:d}),'summary not No end date');t.ok(await v.isFocused(d.getByRole('button',{name:S.cancel,exact:true})),'focus not on Cancel');
 await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a&&a.userOid===BOB&&!('validFrom' in a)&&!('validTo' in a),'dates sent');
});
check('d44.why','7.2 draft.14 (4)','"Why you can request this" starts closed and lists the offer\'s rules, one per line, as text',async t=>{
 const v=await catalog(t,'rules-manager',{tools:janeTools()}),d=await open(t,v,S.requestLabel(roleName(DB))),b=d.getByRole('button',{name:S.why,exact:true});
 t.ok(await b.getAttribute('aria-expanded')==='false'&&!(await v.hasText(EU,{within:d})),'disclosure not closed');await b.click();t.ok(await b.getAttribute('aria-expanded')==='true','disclosure not open');
 t.ok(JSON.stringify(await d.getByRole('listitem').allInnerTexts())===JSON.stringify(DB.offers[0].because),'rules not one per line');
});
check('d44.unsure','7.2 draft.14 (5)','preview.unsure adds one muted line under the list title; without it there is none',async t=>{
 const v=await catalog(t,derive('rules','rules-unsure',M.unsure));t.ok(await v.waitFor(S.unsure),'unsure line absent');
 const at=await v.frame.evaluate(([title,line])=>{const h=[...document.querySelectorAll('h2')].find(e=>e.textContent===title),p=[...document.querySelectorAll('p')].filter(e=>e.textContent===line);
  return {one:p.length===1,after:!!h&&p.length===1&&!!(h.compareDocumentPosition(p[0])&Node.DOCUMENT_POSITION_FOLLOWING),small:p.length===1&&parseFloat(getComputedStyle(p[0]).fontSize)<14};},[S.listTitle(2),S.unsure]);
 t.ok(at.one&&at.after,'not one line under the list title');t.ok(at.small,'unsure line not muted');
 const plain=await catalog(t,'rules');t.ok(!(await plain.hasText(S.unsure)),'unsure line without unsure rules');
});
check('d44.outcome-entry','7.2 draft.14 (6)','an agent request as approver names the relation in its notice',async t=>{
 const v=await catalog(t,'pending-approver');t.ok(await v.waitFor(approver),'relation not named');t.ok(await v.waitFor(v.button(S.track)),'outcome notice absent');
});
check('d44.requestable','7.2 draft.14, D44','with basis requestable none of it appears: list_my_team, no list_request_targets, no relation choice, rules or unsure line, no relation sent or named',async t=>{
 const v=await catalog(t,'manager');t.ok(await v.waitFor(picker(v).getByRole('radio',{name:person(),exact:true})),'reports not offered');await sleep(150);
 t.ok((await v.calls('list_request_targets')).length===0,'targets asked without the rules');t.ok((await v.calls('list_my_team')).length===1,'team not asked once');
 t.ok(!(await v.hasText(S.unsure)),'unsure line');t.ok(await v.frame.getByRole('searchbox',{name:S.findPerson}).count()===0,'person search');
 const d=await open(t,v);t.ok(await group(d,S.relation).count()===0,'relation choice');t.ok(await d.getByRole('button',{name:S.why}).count()===0,'rules disclosure');t.ok(await v.visible(group(d,S.validity)),'validity hidden');
 await submit(v);const a=(await v.calls('request_role'))[0]?.args;t.ok(a&&!('relation' in a),'relation sent');t.ok(await v.waitFor(v.button(S.track)),'outcome absent');t.ok(!(await v.hasText('Requested to:',{loose:true})),'relation named for member');
});
