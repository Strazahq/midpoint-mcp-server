import { S, text } from './strings.mjs';
import { fixture, derive, M, item, nameOf, slot } from './derive.mjs';
import { defaults, diagnostics, VIEW_URL, sleep, inLiveRegion, norm } from '../harness.mjs';

defaults.allowedTools={list_my_requests:['limit'],get_case:['oid'],cancel_request:['caseOid','userName','roleName'],whoami:[]};
defaults.tools={whoami:[{result:fixture('whoami').result}]};
export const checks=[],runWide=[];
const check=(id,criterion,title,run)=>checks.push({id,criterion,title,run});
const across=(id,criterion,title,run)=>runWide.push({id,criterion,title,run});
const button=(v,key)=>v.frame.getByRole('button',{name:S[key],exact:true});
const result=name=>({result:fixture(name).result});
const ready=async v=>v.waitFor(v.frame.getByRole('heading',{name:S['app.title.myRequests'],exact:true}));
async function open(t,entry='list',s={}){const fx=typeof entry==='string'?fixture(entry):entry;const v=await t.open({entry:fx,...s});await ready(v);return v;}
const settle=()=>sleep(180);
const row=(v,name='Database admin')=>v.frame.getByRole('article').filter({has:v.frame.getByRole('heading',{name,exact:true})});
const withdraw=(v)=>v.frame.getByRole('button',{name:/^(Withdraw your request|Preview withdrawal of your request)/}).first();
async function confirm(v){await withdraw(v).click();const dlg=v.dialog();await v.waitFor(dlg);return dlg;}
async function submit(v,key='confirm.withdraw.submit'){const d=await confirm(v);await d.getByRole('button',{name:S[key],exact:true}).click();await settle();}
const writeTools=(name,after='closed',extra={})=>({cancel_request:[{...result(name),...extra}],list_my_requests:[result(after)]});
const caseTools=fx=>({get_case:[{result:(fx??fixture('case')).result}]});
const NO_TOOLS={openLinks:{},updateModelContext:{}};

check('ac01.groups','7.3 AC1','waiting then finished, newest first, status and human names',async t=>{
 const fx=derive('list',M.mixed),v=await open(t,fx);
 const waiting=v.frame.getByRole('region',{name:S['myRequests.group.open'],exact:true}),finished=v.frame.getByRole('region',{name:S['myRequests.group.closed'],exact:true});
 t.ok(await v.visible(waiting),'no waiting group');t.ok(await v.visible(finished),'no finished group');
 const names=await v.frame.getByRole('article').getByRole('heading').allTextContents();
 t.ok(names.join('|')==='Release manager|Database admin|Archive reader|Finance reports|Directory reader',`order ${names}`);
 for(const [label,status] of [['Database admin','waiting'],['Finance reports','approved'],['Archive reader','rejected'],['Directory reader','closed']])t.ok(await v.hasText(S['status.case.'+status],{within:row(v,label)}),`missing ${status}`);
 const surface=await v.surfaceStrings();
 for(const forbidden of ['bstone','mkovac','Assigning role','40000000-','RoleType'])t.ok(!surface.includes(forbidden),`surface leaked ${forbidden}`);
 t.ok(await v.hasText('Waiting for Mia Kovac'),'missing waiting assignee');t.ok(await v.hasText(S['validity.permanent']),'missing unlimited phrase');
 t.ok((await v.calls()).length===0,'entry list caused calls');
});
check('ac01.other-person','7.3 AC1','a request for someone else names that person',async t=>{const v=await open(t,derive('list',M.other));t.ok(await v.hasText(text('myRequests.row.forOther',{user:'Dana Lee'})),'missing requestee');const d=await confirm(v);t.ok(await v.hasText(text('confirm.withdraw.body',{role:'Database admin',user:'Dana Lee'}),{within:d}),'wrong other-person confirm');});
check('ac01.hidden','7.3 AC1, 4.5','unreadable people and unnamed roles use human fallbacks',async t=>{const v=await open(t,derive('list',M.hidden));t.ok(await v.hasText(S['common.personHidden']),'missing hidden person');t.ok(await v.hasText(S['common.itemHidden']),'missing hidden role');t.ok(!(await v.surfaceStrings()).includes('hidden-login'),'hidden login leaked');});
check('ac01.created','7.3 AC1, S16','created requests are waiting and withdrawable',async t=>{const v=await open(t,derive('list',M.created));t.ok(await v.visible(withdraw(v)),'created request has no withdrawal');t.ok(await v.visible(v.frame.getByRole('region',{name:S['myRequests.group.open']})),'created request in wrong group');});
for(const [mutation,pattern] of [[M.permanent,/No end date/],[M.dates,/Access for 5 days/],[M.future,/Access from[\s\S]* for 4 days/]])check('ac01.validity.'+mutation.name,'7.3 AC1, 4.5','requested access: '+mutation.name,async t=>{const v=await open(t,derive('list',mutation));t.ok(pattern.test(norm(await v.text())),'wrong requested access phrase: '+await v.text());});
check('ac02.details','7.3 AC2, 6.15','details read once, retain names, justification and comments',async t=>{
 const fx=fixture('case'),v=await open(t,'list',{tools:caseTools()});
 await row(v).getByRole('button',{name:S['myRequests.action.details'],exact:true}).click();await settle();
 for(const s of [S['timeline.justification'],fx.result.structuredContent.justification,'Step 1, Team leads: Dana Lee and Mia Kovac, both needed','Decided by Dana Lee','Comment from Dana Lee','Fine by me.'])t.ok(await v.hasText(s),`missing ${s}`);
 await button(v,'myRequests.action.hideDetails').click();await button(v,'myRequests.action.details').click();await settle();
 t.ok((await v.calls('get_case')).length===1,'details read more than once');t.ok((await v.calls('get_case'))[0].args.oid===item(fixture('list')).oid,'wrong detail oid');
});
check('ac02.timeline','7.3 AC2, 6.15','parallel assignees deduplicate, put you first, and retain stage strategies',async t=>{
 const v=await open(t,derive('case',M.timeline));
 t.ok(await v.hasText('Step 1, Team leads: you and Dana Lee, both needed'),'all-must-agree wrong');t.ok(await v.hasText('Step 2, Role approvers: you or Mia Kovac, the first decision counts'),'first-decides wrong');
 t.ok((await v.frame.getByText('Decided by Dana Lee',{exact:true}).count())===1,'decision attribution duplicated');t.ok(!(await v.text()).includes('Decided by Bob Stone'),'cancelled item reported decided');
});
check('ac02.detail-error','7.3 states, 6.8','case read failure stays in row',async t=>{const v=await open(t,'list',{tools:caseTools(derive('case',M.stale))});await button(v,'myRequests.action.details').click();await settle();t.ok(await v.hasText(S['error.notFound'],{within:row(v)}),'missing row failure');t.ok(await v.visible(withdraw(v)),'detail failure hid withdrawal');});
check('ac03.case-mode','7.3 AC3','case entry is expanded, then show all reads list once',async t=>{
 const v=await open(t,'case',{tools:{list_my_requests:[result('list')]}});t.ok(await v.hasText(S['timeline.justification']),'case not expanded');t.ok((await v.calls('get_case')).length===0,'case entry re-read');await button(v,'myRequests.action.showAll').click();await settle();t.ok((await v.calls('list_my_requests')).length===1,'show all count');t.ok(!(await v.visible(button(v,'myRequests.action.showAll'))),'case mode remains');
});
for(const mutation of [M.caseOther,M.caseClosed])check('ac04.scope.'+mutation.name,'7.3 AC4','no withdrawal for '+mutation.name,async t=>{const v=await open(t,derive('case',mutation));t.ok(!(await v.visible(withdraw(v))),'ineligible withdrawal offered');});
check('ac04.confirm','7.3 AC4, 6.5','confirm names role and person, no comment or OID; Cancel focused',async t=>{
 const v=await open(t);const d=await confirm(v);t.ok(await v.isFocused(d.getByRole('button',{name:S['common.cancel'],exact:true})),'Cancel not initially focused');
 for(const s of [S['confirm.withdraw.title'],text('confirm.withdraw.bodySelf',{role:'Database admin'}),'Database admin','Bob Stone'])t.ok(await v.hasText(s,{within:d}),`missing ${s}`);
 t.ok((await d.getByRole('textbox').count())===0,'comment field exists');t.ok(!/[0-9a-f]{8}-[0-9a-f-]{20,}/.test(await v.surfaceStrings(d)),'dialog OID');
 await d.getByRole('button',{name:S['common.cancel'],exact:true}).click();t.ok(await v.isFocused(withdraw(v)),'Cancel did not return focus');t.ok((await v.calls('cancel_request')).length===0,'cancel wrote');
});
check('ac04.once','7.3 AC4, 6.5','working confirm cannot submit twice or close on Escape',async t=>{
 const v=await open(t,'list',{tools:writeTools('withdrawn','closed',{hold:true})});await submit(v);const d=v.dialog();
 t.ok(await d.getByRole('button',{name:S['confirm.working']}).isDisabled(),'working button enabled');t.ok(await d.getByRole('button',{name:S['common.cancel'],exact:true}).isDisabled(),'Cancel enabled');
 await v.frame.getByRole('dialog').press('Escape');t.ok(await v.visible(d),'Escape closed pending dialog');const calls=await v.calls('cancel_request');t.ok(calls.length===1,'write count');t.ok(JSON.stringify(calls[0].args)===JSON.stringify({caseOid:item(fixture('list')).oid,userName:item(fixture('list')).objectRef.name,roleName:item(fixture('list')).targetRef.name}),'write arguments not caseOid and names only');await v.release();await settle();
});
for(const outcome of ['withdrawn','unconfirmed','preview'])check('ac05.'+outcome,'7.3 AC5','distinct '+outcome+' outcome and correct list re-read',async t=>{
 const entry=outcome==='preview'?derive('list',M.preview):fixture('list');const v=await open(t,entry,{tools:writeTools(outcome,outcome==='withdrawn'?'closed':'list')});await submit(v,outcome==='preview'?'dryrun.submit':'confirm.withdraw.submit');
 t.ok(await v.hasText(S[outcome==='preview'?'dryrun.result.title':'myRequests.outcome.'+outcome]),'wrong outcome');t.ok((await v.calls('list_my_requests')).length===(outcome==='preview'?0:1),'re-read count');
 if(outcome==='preview'){t.ok(await v.hasText('Database admin') && await v.hasText('Bob Stone'),'preview lacks summary');t.ok(!(await v.text()).includes('/ws/rest'),'preview leaks endpoint');}
 const contexts=await v.sent('ui/update-model-context');t.ok(contexts.length===(outcome==='preview'?0:1),'model context count');if(contexts.length)t.ok(JSON.stringify(contexts[0].params.content)===JSON.stringify(fixture(outcome).result.content),'model context altered server text');
});
for(const name of ['request-closed','not-your-request','not-authorized'])check('ac06.'+name,'7.3 AC6','stable error '+name+' and withdrawal eligibility',async t=>{
 const v=await open(t,'list',{tools:writeTools(name)});await submit(v);const key={'request-closed':'error.requestClosed','not-your-request':'error.notYourRequest','not-authorized':'error.notAuthorized'}[name];
 t.ok(await v.hasText(S[key],{within:row(v)}),'missing row error');t.ok(await withdraw(v).isDisabled()===(name!=='not-authorized'),'wrong disabled state');t.ok((await v.calls('list_my_requests')).length===0,'error re-read');t.ok(await inLiveRegion(v.frame,S[key],'assertive'),'error not announced');
 if(name!=='not-authorized')t.ok(await v.visible(row(v).getByRole('button',{name:S['common.refresh'],exact:true})),'Refresh not suggested');
});
check('ac07.lifetime','7.3 AC7','withdrawn remembered after refresh; a new list view says Closed',async t=>{
 const v=await open(t,'list',{tools:writeTools('withdrawn')});await submit(v);await button(v,'common.refresh').click();await settle();t.ok(await v.hasText(S['status.case.withdrawn']),'withdrawn forgotten');t.ok(!(await v.hasText(S['myRequests.outcome.withdrawn'])),'Refresh kept notice');
 const fresh=await open(t,'closed');t.ok(await v.hasText(S['status.case.withdrawn']) && await fresh.hasText(S['status.case.closed']),'withdrawal memory lifetime wrong');
});
check('ac07.outcome-entry','7.3 AC7, D13','agent withdrawal enters outcome mode without repeating the write',async t=>{
 const v=await open(t,'withdrawn',{tools:{list_my_requests:[result('closed')]},echo:true});await settle();t.ok(await v.hasText(S['status.case.withdrawn']),'entry withdrawal has no chip');t.ok(await v.hasText(S['myRequests.outcome.withdrawn']),'entry has no notice');t.ok((await v.calls('list_my_requests')).length===1,'entry list count');t.ok((await v.calls('cancel_request')).length===0,'entry wrote again');
 await v.notify('ui/notifications/tool-result',fixture('withdrawn').result);await settle();t.ok((await v.calls('list_my_requests')).length===1,'duplicate outcome re-read');
});
for(const decision of ['held','denied'])check('ac08.'+decision,'7.3 AC8, 5.5','intermediary '+decision+' never becomes success',async t=>{
 const fx=slot('withdrawn',decision,{source:'Access connection',reason:'Review required.'});const v=await open(t,'list',{tools:{cancel_request:[{result:fx.result}]}});await submit(v);
 t.ok(await v.hasText(S['strip.'+decision+'.title']),'missing slot');t.ok(await v.hasText(text('strip.source',{source:'Access connection'})),'missing reported source');t.ok(!(await v.hasText(S['myRequests.outcome.withdrawn'])),'slot became success');t.ok(!(await withdraw(v).isDisabled()),'slot disabled request');t.ok((await v.calls('list_my_requests')).length===0,'slot re-read');
});

// Shared contract mechanics, states, security, accessibility, and sizing.
check('shared.refresh','6.6, 7.3 Tools','Refresh reads 50 and keeps stale content while pending',async t=>{
 const v=await open(t,'list',{inputArgs:{limit:7},tools:{list_my_requests:[{...result('closed'),hold:true}]}});await button(v,'common.refresh').click();await settle();
 t.ok(await v.hasText('Database admin'),'refresh removed snapshot');t.ok(await button(v,'common.refreshing').isDisabled(),'refresh not disabled');t.ok((await v.calls('list_my_requests'))[0].args.limit===50,'view did not use limit 50');await v.release();await settle();t.ok(await v.hasText(S['status.case.closed']),'new snapshot not shown');
});
check('shared.echo','3.5','echoed tool responses are idempotent',async t=>{const v=await open(t,'list',{tools:writeTools('withdrawn'),echo:true});await submit(v);await settle();t.ok((await v.calls('cancel_request')).length===1,'duplicate write');t.ok((await v.calls('list_my_requests')).length===1,'duplicate read');t.ok((await v.sent('ui/update-model-context')).length===1,'duplicate model context');});
check('shared.read-only','6.6, 6.7, D11','read-only hides all tool controls and wins over preview banner',async t=>{const v=await open(t,derive('list',M.preview),{caps:NO_TOOLS});for(const key of ['common.refresh','myRequests.action.details','myRequests.action.previewWithdraw'])t.ok(!(await v.visible(button(v,key))),`read-only ${key}`);t.ok(!(await v.visible(withdraw(v))),'read-only withdrawal');t.ok(await v.hasText(S['common.readOnlyHost']),'missing read-only');t.ok(!(await v.hasText(S['dryrun.banner.title'])),'both banners');t.ok((await v.calls()).length===0,'read-only called tool');});
check('shared.read-only-case','6.15, 6.6','case entry data displayed on read-only host with no Details or Show all',async t=>{const v=await open(t,'case',{caps:NO_TOOLS});t.ok(await v.hasText(S['timeline.justification']),'case entry data missing');t.ok(!(await v.visible(button(v,'myRequests.action.showAll'))),'read-only Show all');t.ok(!(await v.visible(button(v,'myRequests.action.hideDetails'))),'read-only Details');});
check('shared.personal','6.2','personal account header and explanation',async t=>{const fx=fixture('personal'),v=await open(t,fx);t.ok(await v.hasText(text('header.mode.personal',{name:fx.result.structuredContent.acting.fullName})),'personal header');await button(v,'common.whatsThis').click();t.ok(await v.hasText(S['header.mode.personal.help']),'header help');});
check('shared.identity','6.2','personal header absent with per-person sign-in',async t=>{const v=await open(t);t.ok(!(await v.visible(button(v,'common.whatsThis'))),'unneeded identity header');t.ok(!(await v.text()).includes('bstone'),'login in header');});
for(const entry of ['list','personal'])check('shared.empty.'+entry,'7.3 states','empty state '+entry,async t=>{const v=await open(t,derive(entry,M.empty));t.ok(await v.hasText(S['myRequests.empty']),'missing empty');if(entry==='personal')t.ok(await v.hasText(text('myRequests.empty.personal',{name:'Bob Stone'})),'missing personal empty');});
check('shared.shared-credential','6.2, 7.3 states','shared credential refusal uses header fallback once',async t=>{const v=await open(t,'shared',{tools:{list_my_requests:[result('list')]}});await settle();t.ok(await v.hasText(S['error.sharedCredential']),'missing refusal');t.ok((await v.calls('whoami')).length===1,'fallback count');});
for(const decision of ['held','denied'])check('shared.entry-slot.'+decision,'5.5, 6.2','entry slot replaces list and calls identity fallback',async t=>{const v=await open(t,slot('list',decision));await settle();t.ok(await v.hasText(S['strip.'+decision+'.title']),'slot missing');t.ok(!(await v.hasText('Database admin')),'slot leaked list');t.ok((await v.calls('whoami')).length===1,'identity fallback');});
check('shared.allowed-slot','5.5, D18','allowed slot adds no intermediary copy',async t=>{const fx=derive('list',function allowed(res){res._meta={'intermediary/decision':{v:1,decision:'allowed',audited:true,source:'Invisible intermediary'}};});const v=await open(t,fx);t.ok(!(await v.text()).includes('Invisible intermediary'),'allowed slot visible');});
check('shared.invalid-slot','5.3','invalid slot ignored',async t=>{const fx=derive('list',function invalid(res){res._meta={'intermediary/decision':{v:2,decision:'held',audited:true}};});const v=await open(t,fx);t.ok(await v.hasText('Database admin'),'invalid slot replaced list');});
check('shared.text-only','6.7','unknown shape shows plain tool text',async t=>{const v=await open(t,derive('list',M.textOnly));t.ok(await v.hasText(S['state.textOnly']),'missing shape notice');t.ok(await v.hasText('The server supplied a plain text answer.'),'missing text');});
check('shared.version','6.7','major mismatch shows text only without writes',async t=>{const v=await t.open({entry:derive('list',M.mismatch)});await v.waitFor(v.frame.getByText(S['state.versionMismatch'],{exact:true}));t.ok(!(await v.visible(withdraw(v))),'mismatch actions');t.ok((await v.calls()).length===0,'mismatch calls');});
check('shared.loading-slow','6.7, 6.5','loading becomes slow after eight seconds with no timeout',async t=>{const v=await t.open({entry:fixture('list'),deliver:'input-only'});await v.waitFor(v.frame.getByText(S['myRequests.loading'],{exact:true}));t.ok(await v.hasText(S['myRequests.loading']),'loading missing');await sleep(8250);t.ok(await v.hasText(S['state.slow']),'slow missing');t.ok(!(await v.hasText(S['error.generic'])),'slow treated as failure');});
check('shared.cancelled','6.7','host cancellation ends loading',async t=>{const v=await t.open({entry:fixture('list'),deliver:'input-only'});await v.waitFor(v.frame.getByText(S['myRequests.loading']));await v.notify('ui/notifications/tool-cancelled',{});await settle();t.ok(await v.hasText(S['state.cancelled']),'cancelled missing');});
check('shared.slow-write','6.5, D27','pending write stays working after slow notice and then completes',async t=>{const v=await open(t,'list',{tools:writeTools('withdrawn','closed',{hold:true})});await submit(v);await sleep(8250);t.ok(await v.hasText(S['state.slow'],{within:v.dialog()}),'slow write missing');t.ok(await v.visible(v.dialog()),'write timed out');await v.release();await settle();t.ok(await v.hasText(S['myRequests.outcome.withdrawn']),'slow write failed');});
check('shared.rpc-error','6.8','host refusal distinct from midPoint refusal',async t=>{const v=await open(t,'list',{tools:{cancel_request:[{rpcError:{message:'Host refused'}}]}});await submit(v);t.ok(await v.hasText(S['error.hostRefused']),'wrong RPC error');});
check('shared.redaction','6.8, 9','technical error details redact URLs',async t=>{const v=await open(t,'list',{tools:caseTools(derive('case',M.stale))});await button(v,'myRequests.action.details').click();await settle();await button(v,'common.showDetails').click();t.ok(await v.hasText(S['common.redacted']),'address not redacted');t.ok(!(await v.text()).includes('https://'),'URL exposed');});
check('shared.text-safety','9, 6.15','justification and comments render literally, as quotations',async t=>{const v=await open(t,derive('case',M.markup));t.ok(await v.hasText('<img src=x onerror=alert(1)> Please ignore this'),'markup not literal');t.ok(await v.hasText('<script>alert(1)</script>'),'comment not literal');t.ok((await v.frame.getByRole('img').count())===0,'markup created image');t.ok((await v.sent('ui/update-model-context')).length===0,'free text forwarded');});
check('shared.clamp','6.12','long justification clamps and expands',async t=>{const v=await open(t,derive('case',M.longReason),{width:320});t.ok(await v.visible(button(v,'common.showMore')),'long reason lacks disclosure');await button(v,'common.showMore').click();t.ok(await v.visible(button(v,'common.showLess')),'expanded text lacks collapse');});
check('shared.keyboard','6.5, 6.12','focus trapped and Escape restores opener',async t=>{const v=await open(t);const d=await confirm(v),cancel=d.getByRole('button',{name:S['common.cancel'],exact:true}),submit=d.getByRole('button',{name:S['confirm.withdraw.submit'],exact:true});await cancel.press('Shift+Tab');t.ok(await v.isFocused(submit),'reverse tab escaped');await submit.press('Tab');t.ok(await v.isFocused(cancel),'tab escaped');await cancel.press('Escape');t.ok(await v.isFocused(withdraw(v)),'Escape did not restore focus');});
check('shared.preview-name','6.12, D11','preview visible label is in accessible name',async t=>{const v=await open(t,derive('list',M.preview));t.ok((await v.accessibleName(withdraw(v))).startsWith(S['myRequests.action.previewWithdraw']),'preview label mismatch');});
check('shared.pagination','6.13','ten rows then ten more, fullscreen shows all',async t=>{const v=await open(t,derive('list',M.many));t.ok((await v.frame.getByRole('article').count())===10,'initial pagination');await button(v,'common.showMore').click();t.ok((await v.frame.getByRole('article').count())===20,'pagination increment');await button(v,'common.expand').click();await settle();t.ok((await v.frame.getByRole('article').count())===23,'fullscreen count');await button(v,'common.collapse').click();await settle();t.ok((await v.calls()).length===0,'pagination called tools');});
check('shared.narrow','6.1, 6.12','no overflow at 320px including timeline and confirm',async t=>{const v=await open(t,'case',{width:320,context:{containerDimensions:{width:320,maxHeight:1600}}});for(const dialog of [false,true]){if(dialog)await confirm(v);const m=await v.frame.evaluate(()=>({w:document.documentElement.clientWidth,s:document.documentElement.scrollWidth}));t.ok(m.s<=m.w+1,`horizontal overflow ${JSON.stringify(m)}`);}await t.shot(v,'narrow');});
check('shared.sizing','6.13','fixed host height limits view and emits changed sizes',async t=>{const v=await open(t,derive('list',M.many),{height:400,context:{containerDimensions:{width:720,height:400}}});await settle();const state=await v.state();t.ok(state.lastSize?.height<=400 && state.lastSize?.height>=96,`size ${JSON.stringify(state.lastSize)}`);});
check('shared.themes','6.9','host theme and font hints update live; palette stays owned by view',async t=>{const v=await open(t);await v.notify('ui/notifications/host-context-changed',{theme:'dark',styles:{variables:{'--font-sans':'Arial','--mp-canvas':'red'}}});await settle();const got=await v.frame.evaluate(()=>({theme:document.documentElement.dataset.theme,font:getComputedStyle(document.documentElement).getPropertyValue('--font-sans'),canvas:getComputedStyle(document.documentElement).getPropertyValue('--mp-canvas')}));t.ok(got.theme==='dark' && got.font==='Arial' && got.canvas!=='red',`theme ${JSON.stringify(got)}`);});
check('shared.no-links','4.9','absence of server.gui shows no links or link footer',async t=>{const v=await open(t);t.ok(!(await v.hasText(S['common.openInMidpoint'])),'link without gui');t.ok(!(await v.hasText(S['common.openInMidpointNote'])),'link note without links');});
check('bridge.teardown','3.4','teardown is acknowledged',async t=>{const v=await open(t);const r=await v.request('ui/resource-teardown',{});t.ok(JSON.stringify(r.result)==='{}','teardown not acknowledged');});
check('bridge.unrelated','3.5','unrelated results and partial input never write',async t=>{const v=await open(t);await v.notify('ui/notifications/tool-input-partial',{arguments:{caseOid:item(fixture('list')).oid}});await v.notify('ui/notifications/tool-result',fixture('withdrawn').result);await settle();t.ok((await v.calls()).length===0,'unsolicited write/reads');t.ok(!(await v.hasText(S['myRequests.outcome.withdrawn'])),'unrelated outcome applied');});

const viewConsole = (d) => d.console.filter((m) => m.type === 'error' && (m.url === '' || m.url.startsWith(VIEW_URL)));

across('shared.no-network', '7.3 AC9, 3.2, 3.6', 'the view made no network request (routes, CSP, network APIs)', (t) => {
  for (const d of diagnostics) {
    const reqs = d.routed.filter((r) => r.frameUrl.startsWith(VIEW_URL));
    if (reqs.length) t.fail(`${d.check}: requests ${JSON.stringify(reqs.slice(0, 3))}`);
    if (d.inst?.network?.length) t.fail(`${d.check}: ${JSON.stringify(d.inst.network.slice(0, 3).map((n) => `${n.api} ${n.target}`))}`);
    if (d.inst?.csp?.length) t.fail(`${d.check}: CSP violations ${JSON.stringify(d.inst.csp.slice(0, 3))}`);
    if (d.inst?.popups?.length) t.fail(`${d.check}: window.open ${JSON.stringify(d.inst.popups)}`);
  }
});

across('shared.no-console-errors', '7.3 AC9', 'no console errors or uncaught exceptions in the view', (t) => {
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
across('ac15.no-timers', '7.3 AC9, 6.6, 6.5 (D27)', 'no setInterval; no timer longer than the 8 s slow notice', (t) => {
  for (const d of diagnostics) {
    if (!d.inst) continue;
    if (d.inst.intervals.length) t.fail(`${d.check}: setInterval(${d.inst.intervals[0].ms}) at ${d.inst.intervals[0].stack}`);
    const long = d.inst.timeouts.filter((x) => x.ms > SLOW_MAX_MS);
    if (long.length) t.fail(`${d.check}: setTimeout(${long[0].ms}) at ${long[0].stack}`);
  }
  if (!diagnostics.some((d) => d.inst)) t.fail('the instrumentation never loaded in a view frame');
});

across('bridge.protocol', '3.3, 3.4, 7.3 Tools', 'only allowed tools and arguments, capabilities respected, every call anticipated', (t) => {
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

for(const theme of ['light','dark'])check('shared.contrast.'+theme,'6.9, 6.12','contrast on rows, timeline, preview, and confirm in '+theme,async t=>{
 const v=await open(t,'case',{context:{theme}});t.ok((await contrastIssues(v)).length===0,'case contrast: '+JSON.stringify(await contrastIssues(v)));await t.shot(v,theme+'-case');
 await confirm(v);t.ok((await contrastIssues(v)).length===0,'dialog contrast: '+JSON.stringify(await contrastIssues(v)));
 const preview=await open(t,derive('list',M.preview),{context:{theme},tools:writeTools('preview')});await submit(preview,'dryrun.submit');t.ok((await contrastIssues(preview)).length===0,'preview contrast: '+JSON.stringify(await contrastIssues(preview)));
});
check('shared.invalid-shape','6.7','invalid structured result without tool falls back to text',async t=>{const fx=derive('list',function invalidShape(res){res.structuredContent={};});const v=await open(t,fx);t.ok(await v.hasText(S['state.textOnly']),'invalid shape stuck loading');});
check('shared.code-first','6.8','stable error code overrides contradictory raw text',async t=>{const fx=derive('not-authorized',function misleading(res){res.content=[{type:'text',text:'only the requester can withdraw'}];});const v=await open(t,'list',{tools:{cancel_request:[{result:fx.result}]}});await submit(v);t.ok(await v.hasText(S['error.notAuthorized']),'text overrode code');});
check('shared.legacy-error','6.8','older server error text still maps to requester error',async t=>{const fx=derive('not-your-request',function noCode(res){delete res._meta;});const v=await open(t,'list',{tools:{cancel_request:[{result:fx.result}]}});await submit(v);t.ok(await v.hasText(S['error.notYourRequest']),'legacy error not classified');});
check('shared.no-context-capability','6.14','no model context call without host capability',async t=>{const v=await open(t,'list',{caps:{serverTools:{}},tools:writeTools('withdrawn')});await submit(v);t.ok((await v.sent('ui/update-model-context')).length===0,'sent context without capability');});
check('shared.detail-slow','6.5','slow Details stay pending until the host answers',async t=>{const v=await open(t,'list',{tools:{get_case:[{...result('case'),hold:true}]}});await button(v,'myRequests.action.details').click();await sleep(8250);t.ok(await v.hasText(S['state.slow'],{within:row(v)}),'missing detail slow notice');await v.release();await settle();t.ok(await v.hasText(S['timeline.justification']),'detail did not finish');});
check('shared.refresh-clears-details','6.6','Refresh clears the details cache; next disclosure re-reads',async t=>{const v=await open(t,'list',{tools:{...caseTools(),list_my_requests:[result('list')]}});await button(v,'myRequests.action.details').click();await settle();await button(v,'common.refresh').click();await settle();await button(v,'myRequests.action.details').click();await settle();t.ok((await v.calls('get_case')).length===2,'stale details cache survived Refresh');});
check('shared.read-slot','5.5','held Refresh replaces list content without polling',async t=>{const fx=slot('list','held');const v=await open(t,'list',{tools:{list_my_requests:[{result:fx.result}]}});await button(v,'common.refresh').click();await settle();t.ok(await v.hasText(S['strip.held.title']),'missing held read');t.ok(!(await v.hasText('Database admin')),'held read kept content');t.ok((await v.calls('list_my_requests')).length===1,'held read polled');});
check('shared.entry-preview','7.3 outcome mode','preview entry has preview notice and one list read, never a write',async t=>{const v=await open(t,'preview',{tools:{list_my_requests:[{result:derive('list',M.preview).result}]}});await settle();t.ok(await v.hasText(S['dryrun.result.title']),'preview entry notice');t.ok((await v.calls('list_my_requests')).length===1,'entry list count');t.ok((await v.calls('cancel_request')).length===0,'preview entry wrote');});
check('shared.size-stable','6.13','size observer reports changed dimensions without a message loop',async t=>{const v=await open(t);await settle();const before=await v.sent('ui/notifications/size-changed');await sleep(350);const after=await v.sent('ui/notifications/size-changed');t.ok(before.length>0 && before.length===after.length,'unstable size notifications');});
check('bridge.handshake','3.4, 3.5','declares stable protocol and display modes before initialized',async t=>{const v=await open(t);const messages=await v.log();const init=messages.find(x=>x.dir==='view>host' && x.msg.method==='ui/initialize'),done=messages.find(x=>x.dir==='view>host' && x.msg.method==='ui/notifications/initialized');t.ok(init?.msg.params.protocolVersion==='2026-01-26','wrong protocol');t.ok(JSON.stringify(init?.msg.params.appCapabilities.availableDisplayModes)==='["inline","fullscreen"]','wrong display modes');t.ok(done?.seq>init?.seq,'initialize order');});
check('shared.fullscreen-absent','6.13','Expand absent when fullscreen unsupported',async t=>{const v=await open(t,'list',{context:{availableDisplayModes:['inline']}});t.ok(!(await v.visible(button(v,'common.expand'))),'Expand unsupported');});
check('shared.names-cap','6.11','long assignee lists show first three and remaining count',async t=>{const fx=derive('list',function manyPeople(res){res.structuredContent.requests[0].waitingFor=Array.from({length:5},(_,i)=>({oid:String(i),type:'User',displayName:'Person '+(i+1)}));});const v=await open(t,fx);t.ok(await v.hasText(text('myRequests.row.waitingFor',{names:'Person 1, Person 2, Person 3, and 2 more'})),'long names not capped');});
check('shared.closed-time','6.11','closed request details include absolute closure time',async t=>{const fx=derive('case',function recentClose(res){res.structuredContent.state='closed';res.structuredContent.closedAt=new Date(Date.now()-60000).toISOString();});const v=await open(t,fx);const texts=await v.frame.getByText(/^Finished /).allTextContents();t.ok(texts.some(s=>s.includes('(') && s.includes(')')),'absolute close timestamp missing from Details');});
check('shared.opaque-oid','4.1, 9','opaque OIDs remain data and pass back unchanged',async t=>{const fx=derive('list',function opaqueOid(res){res.structuredContent.requests[0].oid='__proto__';});const detail=derive('case',function opaqueCase(res){res.structuredContent.oid='__proto__';});const v=await open(t,fx,{tools:caseTools(detail)});await button(v,'myRequests.action.details').click();await settle();t.ok(await v.hasText(S['timeline.justification']),'opaque OID broke row');t.ok((await v.calls('get_case'))[0].args.oid==='__proto__','OID altered');});
