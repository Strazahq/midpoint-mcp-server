#!/usr/bin/env node
// Runs the view checks in headless Chromium against a view document.
//
//   node test/views/run.mjs [--suite inbox] [--view views/approval-inbox.html] [--only text]
//                           [--jobs 4] [--screenshots dir] [--json file] [--list]
//
// A suite is one view's checks: its module exports checks and runWide, and
// SUITES names its default view document.
//
// Exit code: 0 all PASS, 1 any FAIL, 2 the harness could not run.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { T, loadPlaywright } from './harness.mjs';

const repo = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');

const SUITES = {
  inbox: { module: './checks.mjs', view: 'views/approval-inbox.html' },
  'request-access': { module: './request-access/checks.mjs', view: 'views/request-access.html' },
  'my-requests': { module: './my-requests/checks.mjs', view: 'views/my-requests.html' },
  'access-review': { module: './access-review/checks.mjs', view: 'views/access-review.html' },
};

function parseArgs(argv) {
  const o = { suite: 'inbox', view: null, jobs: 4, only: null, screenshots: null, json: null, list: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`${a} needs a value`);
      return argv[++i];
    };
    if (a === '--suite') o.suite = next();
    else if (a === '--view') o.view = next();
    else if (a === '--only') o.only = next();
    else if (a === '--jobs') o.jobs = Math.max(1, parseInt(next(), 10) || 1);
    else if (a === '--screenshots') o.screenshots = next();
    else if (a === '--json') o.json = next();
    else if (a === '--list') o.list = true;
    else if (a === '-h' || a === '--help') o.help = true;
    else throw new Error(`unknown argument ${a}`);
  }
  return o;
}

const CHECK_TIMEOUT_MS = 60000;

function withTimeout(p, ms, what) {
  let timer;
  return Promise.race([p, new Promise((_, rej) => { timer = setTimeout(() => rej(new Error(`${what} took longer than ${ms / 1000} s`)), ms); })])
    .finally(() => clearTimeout(timer));
}

function report(r) {
  const head = `${r.failures.length ? 'FAIL' : 'PASS'}  ${r.id}  [${r.criterion}]  ${r.title}`;
  const body = r.failures.slice(0, 6).map((f) => `        - ${f}`);
  if (r.failures.length > 6) body.push(`        - … and ${r.failures.length - 6} more`);
  console.log([head, ...body].join('\n'));
}

async function main() {
  let o;
  try {
    o = parseArgs(process.argv.slice(2));
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  if (o.help) {
    console.log(readFileSync(fileURLToPath(import.meta.url), 'utf8').split('\n').slice(1, 11).join('\n'));
    return 0;
  }
  const suite = SUITES[o.suite];
  if (!suite) {
    console.error(`unknown suite ${o.suite} (one of ${Object.keys(SUITES).join(', ')})`);
    return 2;
  }
  if (!existsSync(resolve(dirname(fileURLToPath(import.meta.url)), suite.module))) {
    console.error(`suite ${o.suite} has no checks yet (${suite.module})`);
    return 2;
  }
  const { checks, runWide } = await import(suite.module);
  o.view ??= suite.view;
  const match = (c) => !o.only || c.id.includes(o.only) || c.criterion.includes(o.only);
  const selected = checks.filter(match);
  if (o.list) {
    for (const c of [...checks, ...runWide]) console.log(`${c.id}\t[${c.criterion}]\t${c.title}`);
    return 0;
  }

  const viewPath = isAbsolute(o.view) ? o.view : existsSync(resolve(o.view)) ? resolve(o.view) : resolve(repo, o.view);
  if (!existsSync(viewPath)) {
    console.error(`no view document at ${viewPath} (use --view)`);
    return 2;
  }
  const viewDoc = readFileSync(viewPath, 'utf8');
  if (o.screenshots) mkdirSync(o.screenshots, { recursive: true });

  let pw;
  try {
    pw = loadPlaywright();
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  const browser = await pw.pw.chromium.launch({ headless: true });
  console.log(`view: ${viewPath} (${viewDoc.length} bytes)\nplaywright: ${pw.from}\nchecks: ${selected.length} + ${runWide.length} run-wide, ${o.jobs} at a time\n`);

  const results = new Array(selected.length);
  let printed = 0;
  const flush = () => {
    while (printed < results.length && results[printed]) report(results[printed++]);
  };
  let next = 0;
  const worker = async () => {
    while (next < selected.length) {
      const i = next++;
      const c = selected[i];
      const t = new T(c.id, browser, viewDoc, o.screenshots);
      try {
        await withTimeout(c.run(t), CHECK_TIMEOUT_MS, c.id);
      } catch (e) {
        t.fail(`exception: ${String(e?.message ?? e).split('\n')[0]}`);
      } finally {
        await t.done().catch(() => {});
      }
      results[i] = { id: c.id, criterion: c.criterion, title: c.title, failures: t.failures };
      flush();
    }
  };
  await Promise.all(Array.from({ length: o.jobs }, worker));
  await browser.close();

  console.log('\nrun-wide:');
  const wide = [];
  for (const c of runWide) {
    const t = new T(c.id, null, null, null);
    try {
      await c.run(t);
    } catch (e) {
      t.fail(`exception: ${String(e?.message ?? e).split('\n')[0]}`);
    }
    const r = { id: c.id, criterion: c.criterion, title: c.title, failures: t.failures };
    wide.push(r);
    report(r);
  }

  const all = [...results, ...wide];
  const failed = all.filter((r) => r.failures.length);
  console.log(`\n${all.length - failed.length} PASS, ${failed.length} FAIL`);
  if (o.json) writeFileSync(o.json, `${JSON.stringify({ view: viewPath, results: all }, null, 2)}\n`);
  return failed.length ? 1 : 0;
}

main().then((code) => process.exit(code), (e) => {
  console.error(e);
  process.exit(2);
});
