import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '@playwright/test';

const dir = dirname(fileURLToPath(import.meta.url));
const html = readFileSync(join(dir, 'dashboard.html'));
const shots = join(dir, 'live-shots');
const CONFIRM = 'Запись в Медиалоге ещё действует — отмените её в клиенте (правый щелчок → „Пациент не придёт“). Закрыть строку?';

const loginHTML = `<!DOCTYPE html><html lang="ru"><head><meta charset="utf-8"><title>Вход — дашборд МедКвадрат</title>
<style>body{font-family:system-ui,sans-serif;background:#EEF1EE;color:#0F1E1C;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}
form{background:#fff;padding:28px 32px;border-radius:10px;border:1px solid #D5DBD7;min-width:280px}
h1{font-size:18px;margin:0 0 16px}label{display:block;font-size:13px;margin-bottom:6px}
input{width:100%;box-sizing:border-box;padding:8px 10px;font-size:15px;border:1px solid #D5DBD7;border-radius:6px}
button{margin-top:14px;width:100%;padding:10px;background:#0E4F45;color:#fff;border:0;border-radius:6px;font-size:14px;cursor:pointer}
</style></head><body>
<form method="post" action="/dashboard/login"><h1>Дашборд расписания</h1>
<label for="login">Логин</label><input id="login" name="login" autocomplete="username" required>
<label for="password">Пароль</label><input id="password" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Войти</button></form></body></html>`;

function schedule() {
  return {
    success: true,
    data: {
      date: '2026-11-06',
      branches: [106, 3, 496].map(id => ({
        branch_id: id,
        branch_code: String(id),
        name: { 106: 'Каширка', 3: 'Куркино', 496: 'Куркино 2' }[id],
        appointments: [],
      })),
    },
  };
}

function row(extra) {
  return {
    id: 7,
    source: 'max',
    status: 'new',
    outcome: '',
    patient_name: 'Иванов Иван',
    patient_phone: '+79990001122',
    patient_birth: '1980-01-01',
    created_at_msk: '06.11 09:00',
    reason: 'declined',
    orig_date_start: '06.11 10:00',
    branch_id: 106,
    orig_doctor: 'Петров',
    orig_service: 'Терапевт',
    medialog_state: 'запись действует',
    future_label: '—',
    updated_by: 'kash',
    updated_at: '06.11 12:10',
    version: 1,
    ...extra,
  };
}

let server;
let base;

test.beforeAll(async () => {
  server = createServer((req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
    res.end(html);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${server.address().port}/dashboard`;
});

test.afterAll(async () => {
  await new Promise(resolve => server.close(resolve));
});

test('login form', async ({ page }) => {
  await page.setContent(loginHTML);
  await expect(page.getByRole('heading', { name: 'Дашборд расписания' })).toBeVisible();
  await expect(page.locator('#login')).toBeVisible();
  await expect(page.locator('#password')).toBeVisible();
  await page.screenshot({ path: join(shots, 'login.png') });
});

async function openBoard(page, opts) {
  opts = opts || {};
  const state = {
    posts: [],
    role: opts.role || 'registrar',
    tempPassword: opts.tempPassword || '',
    closedHidden: !!opts.closedHidden,
    banner: opts.banner || '',
    list: (opts.items || [row()]).map(it => ({ ...it })),
  };
  await page.clock.install({ time: new Date(opts.time || '2026-11-06T10:39:00.000Z') });
  await page.route('**/dashboard/me', async route => {
    await route.fulfill({
      json: {
        success: true,
        data: {
          login: opts.login || 'kash',
          full_name: opts.fullName === undefined ? 'Каширка Регистратор' : opts.fullName,
          role: state.role,
          branches: opts.branches || [
            { id: 106, name: 'Каширка' },
            { id: 3, name: 'Куркино' },
            { id: 496, name: 'Куркино 2' },
          ],
          must_change_password: false,
        },
      },
    });
  });
  await page.route('**/dashboard/data**', async route => {
    if (state.holdData) await state.holdData;
    if (state.abortData) {
      state.abortData = false;
      await route.abort('failed');
      return;
    }
    if (state.dataReply) {
      const reply = state.dataReply;
      state.dataReply = null;
      await route.fulfill(reply);
      return;
    }
    const body = JSON.parse(JSON.stringify(opts.schedule || schedule()));
    const raw = new URL(route.request().url()).searchParams.get('branch') || '';
    const ids = raw.split(',').map(s => s.trim()).filter(Boolean);
    if (ids.length && body.data && body.data.branches) {
      body.data.branches = body.data.branches.filter(b => ids.includes(String(b.branch_id)));
    }
    await route.fulfill({ json: body });
  });
  await page.route('**/dashboard/waitlist**', async route => {
    if (route.request().method() !== 'GET') {
      const body = route.request().postDataJSON();
      state.posts.push(body);
      if (state.conflictNext) {
        state.conflictNext = false;
        const fresh = row({
          status: 'contacted',
          outcome: 'call_back',
          version: body.version || 1,
          medialog_state: 'запись действует',
        });
        await route.fulfill({
          status: 409,
          json: {
            success: false,
            error: 'WAITLIST_CHANGED',
            error_details: { message: 'Строку уже изменил другой сотрудник' },
            data: {
              row: {
                status: fresh.status,
                outcome: fresh.outcome,
                version: fresh.version,
                updated_by: 'kash',
                updated_at: '06.11 12:20',
              },
              enriched: true,
              full: fresh,
            },
          },
        });
        return;
      }
      if (state.bare409 && body.outcome === 'call_back') {
        state.bare409 = false;
        await route.fulfill({
          status: 409,
          json: {
            success: false,
            error: 'WAITLIST_CHANGED',
            error_details: { message: 'Строку уже изменил другой сотрудник' },
            data: {
              row: { status: 'new', outcome: '', version: body.version || 1, updated_by: '', updated_at: '' },
              enriched: false,
              full: { id: 7, patient_name: 'Чужой', patient_phone: '70000000000', medialog_state: 'запись отменена' },
            },
          },
        });
        return;
      }
      if (state.thinNext) {
        state.thinNext = false;
        state.mirrorWork = true;
        await route.fulfill({
          json: {
            success: true,
            data: {
              row: {
                status: 'contacted',
                outcome: body.outcome || '',
                version: (body.version || 1) + 1,
                updated_by: 'kash',
                updated_at: '06.11 12:20',
              },
              enriched: false,
              full: { id: 7, patient_name: 'Чужой', patient_phone: '70000000000', medialog_state: 'запись отменена' },
            },
          },
        });
        return;
      }
      if (state.holdPatch) await state.holdPatch;
      if (state.reply) {
        const reply = state.reply;
        state.reply = null;
        await route.fulfill(reply);
        return;
      }
      const saved = row({
        status: body.action === 'reopen' ? 'contacted' : (body.outcome === 'no_answer' || body.outcome === 'call_back' ? 'contacted' : 'closed'),
        outcome: body.action === 'reopen' ? '' : body.outcome,
        version: (body.version || 1) + 1,
      });
      await route.fulfill({
        json: {
          success: true,
          data: {
            row: {
              status: saved.status,
              outcome: saved.outcome,
              version: saved.version,
              updated_by: saved.updated_by || '',
              updated_at: saved.updated_at || '',
            },
            enriched: !!(saved.patient_name || saved.patient_phone),
            full: saved,
          },
        },
      });
      return;
    }
    if (state.holdList) {
      const gate = state.holdList;
      state.holdList = null;
      await gate;
    }
    if (state.abortList) {
      state.abortList = false;
      await route.abort('failed');
      return;
    }
    const url = new URL(route.request().url());
    let items = state.list.map(it => ({ ...it }));
    if (state.mirrorWork) {
      const last = state.posts.at(-1);
      if (last && last.outcome) {
        items = items.map(it => it.id === 7 ? { ...it, outcome: last.outcome, status: 'contacted', version: (last.version || 1) + 1 } : it);
      }
    }
    let visible = state.closedHidden && url.searchParams.get('status') === 'open'
      ? items.filter(it => (state.posts.at(-1) || {}).status !== 'closed' || it.id !== 7)
      : items;
    const branch = url.searchParams.get('branch') || '';
    if (branch) {
      const ids = branch.split(',').map(s => s.trim());
      visible = visible.filter(it => ids.includes(String(it.branch_id)));
    }
    const q = (url.searchParams.get('q') || '').trim().toLowerCase();
    const matched = [...q].length >= 2
      ? visible.filter(it => (it.patient_name || '').toLowerCase().includes(q))
      : visible;
    await route.fulfill({
      json: {
        success: true,
        data: { open_total: visible.length, page: 1, total: matched.length, per_page: 50, items: matched, banner: state.banner || '' },
      },
    });
  });
  await page.route('**/dashboard/staff**', async route => {
    if (route.request().method() !== 'GET') {
      if (state.holdStaff) await state.holdStaff;
      await route.fulfill({
        json: {
          success: true,
          data: {
            temporary_password: state.tempPassword || '',
            user: { id: 2, login: 'new', full_name: 'Новый', role: 'registrar', branches: '106', active: true, must_change_password: true },
          },
        },
      });
      return;
    }
    await route.fulfill({
      json: {
        success: true,
        data: {
          users: [{
            id: 1, login: 'admin', full_name: 'Администратор', role: 'admin', branches: '*', active: true, must_change_password: false,
          }],
        },
      },
    });
  });
  await page.goto(base);
  return state;
}

test('header branch select and two waitlist sources', async ({ page }) => {
  await openBoard(page, {
    items: [
      row(),
      row({
        id: 88001,
        source: 'registry',
        patient_name: 'Сидорова Анна',
        patient_phone: '+79991112233',
        reason: '',
        creator_name: 'Иванова Р.',
        orig_service: 'Лист ожидания 10 мин',
        branch_id: 3,
      }),
    ],
  });
  await expect(page.locator('#branchButton')).toBeVisible();
  await expect(page.locator('#branch option')).toHaveCount(4);
  await expect(page.locator('#whoami')).toContainText('Регистратор');
  await page.screenshot({ path: join(shots, 'branch-select.png') });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#wlWork')).toBeVisible();
  await expect(page.locator('#wlSource')).toBeVisible();
  await expect(page.locator('#waitlistList')).toContainText('MAX');
  await expect(page.locator('#waitlistList')).toContainText('Регистратура');
  await expect(page.locator('#waitlistList')).toContainText('Сидорова Анна');
  await expect(page.locator('#waitlistList')).toContainText('kash');
  await page.screenshot({ path: join(shots, 'waitlist-sources.png') });
  const filtered = page.waitForRequest(r => r.url().includes('/dashboard/waitlist') && r.url().includes('status=closed'));
  await page.locator('#wlWork').selectOption('closed');
  await filtered;
});

test('outcome on a new row saves immediately and close asks before the request', async ({ page }) => {
  const state = await openBoard(page, { closedHidden: true });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  await expect(page.locator('.wl-status')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Сохранить' })).toHaveCount(0);
  const saved = page.waitForRequest(r => r.url().includes('/dashboard/waitlist/max/7') && r.method() === 'PATCH');
  await page.locator('.wl-outcome').selectOption('call_back');
  const first = await saved;
  expect(first.postDataJSON()).toMatchObject({ outcome: 'call_back', version: 1 });
  await expect(page.locator('#waitlistList .wl-saved')).toContainText('Сохранено ✓');
  await page.screenshot({ path: join(shots, 'waitlist-saved.png') });
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await expect(page.locator('#confirmText')).toHaveText(CONFIRM);
  await page.screenshot({ path: join(shots, 'close-confirm.png') });
  await page.locator('#confirmNo').click();
  expect(state.posts).toHaveLength(1);
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
  const closed = page.waitForRequest(r => r.url().includes('/dashboard/waitlist/max/7') && r.method() === 'PATCH');
  await page.locator('.wl-outcome').selectOption('refused');
  await page.locator('#confirmYes').click();
  const second = await closed;
  expect(second.postDataJSON()).toMatchObject({ outcome: 'refused', version: 2 });
});

test('failed enrichment keeps the patient and the close dialog', async ({ page }) => {
  const state = await openBoard(page, {});
  state.thinNext = true;
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').selectOption('call_back');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await expect(page.locator('#waitlistList')).not.toContainText('Чужой');
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await expect(page.locator('#confirmText')).toHaveText(CONFIRM);
});

test('a conflict without enrichment keeps the active appointment dialog', async ({ page }) => {
  const state = await openBoard(page, {});
  state.bare409 = true;
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  await expect(page.locator('#banner')).toContainText('Строку уже изменил другой сотрудник');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await expect(page.locator('#waitlistList')).not.toContainText('Чужой');
  await expect(page.locator('#waitlistList')).toContainText('действует — отменить в клиенте');
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await expect(page.locator('#confirmText')).toHaveText(CONFIRM);
  await page.locator('#confirmNo').click();
});

test('registry keeps the phone when enrichment fails', async ({ page }) => {
  const state = await openBoard(page, {
    items: [row({
      id: 88001,
      source: 'registry',
      patient_name: 'Сидорова Анна',
      patient_phone: '+79991112233',
      reason: '',
      medialog_state: 'запись действует',
    })],
  });
  state.thinNext = true;
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79991112233');
  await page.locator('.wl-outcome').selectOption('call_back');
  await expect(page.locator('#waitlistList')).toContainText('+79991112233');
  await expect(page.locator('#waitlistList')).not.toContainText('Чужой');
  await expect(page.locator('#waitlistList')).not.toContainText('70000000000');
});

test('focused row clears medialog fields and keeps the outcome', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  const saved = page.waitForRequest(r => r.method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  await page.locator('.wl-outcome').selectOption('call_back');
  await saved;
  state.banner = 'Медиалог недоступен';
  state.list = [row({
    patient_name: '',
    patient_phone: '',
    patient_birth: '',
    orig_date_start: '',
    orig_doctor: '',
    orig_service: '',
    medialog_state: '',
    future_label: '',
    future_date_start: '',
  })];
  const refresh = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist'));
  await page.clock.fastForward(11000);
  await refresh;
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).not.toContainText('+79990001122');
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
  release();
});

test('a poll at the saved version is not a conflict while the patch is in flight', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  await page.locator('.wl-outcome').selectOption('call_back');
  const refresh = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist'));
  await page.clock.fastForward(11000);
  await refresh;
  await expect(page.locator('.wl-conflict')).toHaveCount(0);
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
  release();
});

async function selectRowText(page, locator) {
  await locator.evaluate(el => {
    const range = document.createRange();
    range.selectNodeContents(el);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  });
}

test('a text selection still clears medialog fields', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await selectRowText(page, page.locator('#waitlistList a'));
  state.banner = 'Медиалог недоступен';
  state.list = [row({
    patient_name: '',
    patient_phone: '',
    patient_birth: '',
    orig_date_start: '',
    orig_doctor: '',
    orig_service: '',
    medialog_state: '',
    future_label: '',
  })];
  const refresh = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist'));
  await page.clock.fastForward(11000);
  await refresh;
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).not.toContainText('+79990001122');
});

test('a text selection still drops a moved row', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList tbody tr')).toHaveCount(1);
  await selectRowText(page, page.locator('#waitlistList'));
  state.list = [];
  const refresh = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist'));
  await page.clock.fastForward(11000);
  await refresh;
  await expect(page.locator('#waitlistList tbody tr')).toHaveCount(0);
});

test('conflict without enrichment reloads the row', async ({ page }) => {
  const state = await openBoard(page, {});
  state.bare409 = true;
  await page.locator('#tabWaitlist').click();
  const reload = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist'));
  await page.locator('.wl-outcome').selectOption('call_back');
  await reload;
  await expect(page.locator('#banner')).toContainText('Строку уже изменил другой сотрудник');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await expect(page.locator('#waitlistList')).not.toContainText('Чужой');
});

test('medialog banner stays on the waitlist', async ({ page }) => {
  await openBoard(page, { banner: 'Медиалог недоступен', items: [row()] });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
});

test('refusing an active appointment still asks after a conflict response', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  await page.locator('.wl-outcome').selectOption('call_back');
  await expect(page.locator('#waitlistList .wl-saved')).toContainText('Сохранено ✓');
  state.conflictNext = true;
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await page.locator('#confirmYes').click();
  await expect(page.locator('#banner')).toContainText('Строку уже изменил другой сотрудник');
  await expect(page.locator('#waitlistList')).toContainText('действует — отменить в клиенте');
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await page.locator('#confirmNo').click();
});

function busyDay() {
  return {
    success: true,
    data: {
      date: '2026-11-06',
      branches: [106, 3, 496].map(id => ({
        branch_id: id,
        name: { 106: 'Каширка', 3: 'Куркино', 496: 'Куркино 2' }[id],
        appointments: Array.from({ length: 8 }, (_, i) => ({
          planning_id: id * 100 + i,
          time: '17:00',
          patient_name: 'Пациент ' + (i + 1),
          doctor_name: 'Врач',
          confirmation: 'pending',
        })),
      })),
    },
  };
}

test('schedule keeps the scroll and shows the now line in every column', async ({ page }) => {
  await openBoard(page, { time: '2026-11-06T14:14:00.000Z', schedule: busyDay(), items: [] });
  const lists = page.locator('#board .list');
  await expect(lists).toHaveCount(3);
  for (const list of await lists.all()) {
    const visible = await list.evaluate(el => {
      const now = el.querySelector('.now');
      if (!now) return false;
      const box = el.getBoundingClientRect();
      const line = now.getBoundingClientRect();
      return line.top >= box.top - 1 && line.bottom <= box.bottom + 1;
    });
    expect(visible).toBe(true);
  }
  await page.screenshot({ path: join(shots, 'now-line.png') });
  const before = await lists.first().evaluate(el => el.scrollTop);
  await lists.first().evaluate(el => { el.scrollTop = 40; el.dispatchEvent(new Event('scroll')); });
  const scrolled = await lists.first().evaluate(el => el.scrollTop);
  expect(scrolled).toBeLessThan(before);
  await page.locator('#tabWaitlist').click();
  await page.locator('#tabSchedule').click();
  const after = await lists.first().evaluate(el => el.scrollTop);
  expect(Math.abs(after - scrolled)).toBeLessThan(2);
});

test('schedule fits the window at both desk sizes', async ({ page }) => {
  await openBoard(page, { schedule: busyDay(), items: [] });
  for (const size of [{ width: 1920, height: 1080 }, { width: 1366, height: 768 }]) {
    await page.setViewportSize(size);
    const overflow = await page.evaluate(() => document.documentElement.scrollHeight > document.documentElement.clientHeight + 2);
    expect(overflow).toBe(false);
    const inside = await page.locator('.col').first().evaluate(el => {
      const box = el.getBoundingClientRect();
      return box.bottom <= window.innerHeight + 1 && box.bottom > window.innerHeight * 0.5;
    });
    expect(inside).toBe(true);
    await expect(page.locator('.schedule-end').first()).toHaveCount(1);
  }
  await page.screenshot({ path: join(shots, 'schedule-bottom.png') });
});

test('one branch is a label and the pager stays on the page', async ({ page }) => {
  await openBoard(page, { branches: [{ id: 106, name: 'Каширка' }], items: [row()] });
  await expect(page.locator('#branchButton')).toBeHidden();
  await expect(page.locator('#branchLabel')).toHaveText('Каширка');
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#wlPager')).toBeVisible();
  await expect(page.locator('#noteWaitlist')).toContainText('отмену или перенос делайте в клиенте');
  await page.screenshot({ path: join(shots, 'waitlist-one-result.png') });
});

test('staff tab is admin-only and lists employees without a password hash', async ({ page }) => {
  await openBoard(page, { role: 'admin', items: [] });
  await expect(page.locator('#tabStaff')).toBeVisible();
  await page.locator('#tabStaff').click();
  await expect(page.locator('#staffTable')).toContainText('admin');
  await expect(page.locator('#staffTable')).toContainText('ИТ');
  await expect(page.locator('#staffPanel')).not.toContainText('password_hash');
  await page.screenshot({ path: join(shots, 'staff.png') });
});

test('registrar does not see the staff tab', async ({ page }) => {
  await openBoard(page, { role: 'registrar', items: [] });
  await expect(page.locator('#tabStaff')).toBeHidden();
});

test('branch option text is darker than its background', async ({ page }) => {
  await openBoard(page, { role: 'manager', fullName: 'Мартынов Д. Н.', items: [] });
  const colors = await page.locator('select.top-select option').first().evaluate(el => {
    const style = getComputedStyle(el);
    return { color: style.color, background: style.backgroundColor };
  });
  expect(colors.color).not.toBe(colors.background);
  await expect(page.locator('#whoami')).toHaveText('Мартынов Д. Н. · Директор / колл-центр · все филиалы');
  await page.screenshot({ path: join(shots, 'header-manager.png') });
});

test('registrar header names the one branch', async ({ page }) => {
  await openBoard(page, {
    role: 'registrar', login: 'reg106', fullName: '',
    branches: [{ id: 106, name: 'Каширка' }], items: [],
  });
  await expect(page.locator('#whoami')).toHaveText('reg106 · Регистратор · Каширка');
  await expect(page.locator('#branchButton')).toBeHidden();
  await page.screenshot({ path: join(shots, 'header-registrar.png') });
  await expect(page.locator('.col')).toHaveCount(1);
  const wide = await page.evaluate(() => {
    const board = document.getElementById('board').getBoundingClientRect();
    const col = document.querySelector('#board .col').getBoundingClientRect();
    return col.width / board.width;
  });
  expect(wide).toBeGreaterThanOrEqual(0.9);
  await page.screenshot({ path: join(shots, 'board-one-branch.png') });
});

test('manager can keep two branches and the choice survives reload', async ({ page }) => {
  await openBoard(page, { role: 'manager', fullName: 'Мартынов Д. Н.', items: [row(), row({ id: 8, branch_id: 3, patient_name: 'Петров' }), row({ id: 9, branch_id: 496, patient_name: 'Сидоров' })] });
  await page.locator('#branchButton').click();
  await expect(page.locator('#branchMenu')).toBeVisible();
  await page.screenshot({ path: join(shots, 'branch-menu.png') });
  await page.locator('#branchChecks input[value="496"]').uncheck();
  await page.locator('#branchButton').click();
  await expect(page.locator('#branchMenu')).toBeHidden();
  await expect(page.locator('.col')).toHaveCount(2);
  const halves = await page.evaluate(() => {
    const board = document.getElementById('board').getBoundingClientRect();
    return [...document.querySelectorAll('#board .col')].map(col => col.getBoundingClientRect().width / board.width);
  });
  expect(halves).toHaveLength(2);
  halves.forEach(part => expect(part).toBeGreaterThan(0.4));
  await page.screenshot({ path: join(shots, 'board-two-branches.png') });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов');
  await expect(page.locator('#waitlistList')).toContainText('Петров');
  await expect(page.locator('#waitlistList')).not.toContainText('Сидоров');
  await page.reload();
  await expect(page.locator('#branchButton')).toHaveText('Каширка, Куркино');
  await expect(page.locator('.col')).toHaveCount(2);
});

test('waitlist scrolls inside the page and keeps its place', async ({ page }) => {
  const items = Array.from({ length: 50 }, (_, i) => row({ id: i + 1, patient_name: `Пациент ${i + 1}` }));
  await openBoard(page, { role: 'registrar', branches: [{ id: 106, name: 'Каширка' }], items });
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList tbody tr')).toHaveCount(50);
  const pageFits = await page.evaluate(() => document.documentElement.scrollHeight <= document.documentElement.clientHeight + 2);
  expect(pageFits).toBe(true);
  const pager = await page.locator('#wlPager').boundingBox();
  expect(pager.y + pager.height).toBeLessThanOrEqual(768);
  await page.locator('#waitlistList').evaluate(el => { el.scrollTop = el.scrollHeight; });
  await expect(page.locator('#waitlistList tbody tr').last()).toBeVisible();
  await page.screenshot({ path: join(shots, 'waitlist-scroll.png') });
  const top = await page.locator('#waitlistList').evaluate(el => el.scrollTop);
  expect(top).toBeGreaterThan(0);
  await page.clock.fastForward(11000);
  const after = await page.locator('#waitlistList').evaluate(el => el.scrollTop);
  expect(after).toBe(top);
});

test('waitlist search keeps the query and the scroll position', async ({ page }) => {
  const items = [
    row({ patient_name: 'Иванов Иван' }),
    ...Array.from({ length: 30 }, (_, i) => row({ id: 100 + i, patient_name: `Сидорова ${i + 1}` })),
  ];
  await openBoard(page, { items });
  await page.locator('#tabWaitlist').click();
  await page.locator('#wlQuery').fill('сидор');
  await expect(page.locator('#waitlistList')).toContainText('Сидорова');
  await expect(page.locator('#waitlistList')).not.toContainText('Иванов');
  await page.locator('#waitlistList').evaluate(el => { el.scrollTop = 80; });
  await page.screenshot({ path: join(shots, 'waitlist-search.png') });
  await page.clock.fastForward(11000);
  await expect(page.locator('#wlQuery')).toHaveValue('сидор');
  expect(await page.locator('#waitlistList').evaluate(el => el.scrollTop)).toBe(80);
  await page.locator('#wlClear').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов');
  await expect(page.locator('#waitlistList')).toContainText('Сидорова');
});

test('a temporary password can be hidden and leaves the staff tab', async ({ page }) => {
  await openBoard(page, { role: 'admin', tempPassword: 'Temp-pass-1', items: [] });
  await page.locator('#tabStaff').click();
  await page.locator('#staffLogin').fill('reg106');
  await page.locator('#staffName').fill('Регистратор');
  await page.locator('#staffForm button[type="submit"]').click();
  await expect(page.locator('#staffTemp')).toContainText('Temp-pass-1');
  await expect(page.locator('#staffTemp')).toContainText('Передайте сотруднику лично');
  await page.locator('#tabSchedule').click();
  await expect(page.locator('#staffTemp')).toBeHidden();
});

test('logout asks the server and opens the login page', async ({ page }) => {
  await openBoard(page, { role: 'admin', fullName: 'Мартынов Д. Н.', items: [] });
  await expect(page.locator('#whoami')).toHaveText('Мартынов Д. Н. · ИТ · все филиалы');
  await page.locator('#accountMenu summary').click();
  const gone = page.waitForRequest(r => r.url().includes('/dashboard/logout') && r.method() === 'POST');
  await page.locator('#logoutBtn').click();
  await gone;
  await expect(page).toHaveURL(/\/dashboard\/login$/);
});

test('logout does not poll while the request is in flight', async ({ page }) => {
  await openBoard(page, { role: 'admin', items: [] });
  let release;
  const held = new Promise(resolve => { release = resolve; });
  await page.route('**/dashboard/logout', async route => {
    await held;
    await route.fulfill({ status: 200, contentType: 'text/html', body: 'ok' });
  });
  const polls = [];
  page.on('request', req => {
    if (req.url().includes('/dashboard/data') || req.url().includes('/dashboard/waitlist')) polls.push(req.url());
  });
  await page.locator('#accountMenu summary').click();
  const gone = page.waitForRequest(r => r.url().includes('/dashboard/logout') && r.method() === 'POST');
  await page.locator('#logoutBtn').click();
  await gone;
  polls.length = 0;
  await page.evaluate(() => {
    document.dispatchEvent(new Event('visibilitychange'));
    window.dispatchEvent(new Event('focus'));
    window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
  });
  await page.clock.fastForward(15000);
  expect(polls).toEqual([]);
  release();
  await expect(page).toHaveURL(/\/dashboard\/login$/);
});

test('a failed logout stays on the dashboard', async ({ page }) => {
  await openBoard(page, { role: 'admin', items: [] });
  await page.route('**/dashboard/logout', route => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: JSON.stringify({ success: false, error: 'Не удалось выйти, повторите', code: 'LOGOUT_FAILED' }),
  }));
  await page.locator('#accountMenu summary').click();
  await page.locator('#logoutBtn').click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.locator('#banner')).toContainText('Не удалось выйти — повторите');
  await expect(page.locator('#board')).toBeVisible();
});

test('a late staff password is not put back on screen', async ({ page }) => {
  const state = await openBoard(page, { role: 'admin', items: [] });
  const absent = async (secret) => {
    const found = await page.evaluate(pw => {
      if ((document.body.textContent || '').includes(pw)) return 'text';
      const nodes = document.body.querySelectorAll('*');
      for (const node of nodes) {
        for (const attr of node.attributes) {
          if ((attr.value || '').includes(pw)) return attr.name;
        }
      }
      return '';
    }, secret);
    expect(found).toBe('');
  };
  let release;
  state.holdStaff = new Promise(resolve => { release = resolve; });
  state.tempPassword = 'Create-secret-91';
  await page.locator('#tabStaff').click();
  await page.locator('#staffLogin').fill('reg106');
  await page.locator('#staffName').fill('Регистратор');
  await page.locator('#staffForm button[type="submit"]').click();
  await page.locator('#tabSchedule').click();
  await page.locator('#tabStaff').click();
  release();
  await expect(page.locator('#staffStatus')).toContainText('Пароль для reg106 создан');
  await absent('Create-secret-91');

  state.tempPassword = 'Reset-secret-92';
  state.holdStaff = new Promise(resolve => { release = resolve; });
  await page.getByRole('button', { name: 'Сбросить пароль' }).click();
  await page.locator('#tabSchedule').click();
  await page.locator('#tabStaff').click();
  release();
  await expect(page.locator('#staffStatus')).toContainText('Пароль для admin создан');
  await absent('Reset-secret-92');
  await absent('Create-secret-91');
});

test('copy uses the plain HTTP fallback', async ({ page }) => {
  await page.addInitScript(() => {
    const hide = { configurable: true, get: () => undefined };
    try { Object.defineProperty(navigator, 'clipboard', hide); } catch (e) {}
    try { Object.defineProperty(Object.getPrototypeOf(navigator), 'clipboard', hide); } catch (e) {}
    window.__copyCalls = [];
    document.execCommand = cmd => { window.__copyCalls.push(cmd); return true; };
  });
  await openBoard(page, { role: 'admin', tempPassword: 'Copy-secret-7', items: [] });
  await page.locator('#tabStaff').click();
  await page.locator('#staffLogin').fill('reg106');
  await page.locator('#staffName').fill('Регистратор');
  await page.locator('#staffForm button[type="submit"]').click();
  await expect(page.locator('#staffTempText')).toHaveText('Copy-secret-7');
  await page.locator('#staffCopy').click();
  await expect(page.locator('#staffCopyNote')).toHaveText('Скопировано');
  expect(await page.evaluate(() => window.__copyCalls)).toContain('copy');
});

test('a search keeps the saved row and the same node', async ({ page }) => {
  const state = await openBoard(page, { items: [row({ patient_name: 'Иванов Иван' })] });
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await line.evaluate(el => { el.dataset.pin = 'kept'; });
  await line.locator('.wl-outcome').selectOption('call_back');
  const searched = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('q='));
  await page.locator('#wlQuery').fill('иван');
  await page.clock.fastForward(400);
  await searched;
  await expect(page.locator('#waitlistList tr[data-pin="kept"]')).toBeVisible();
  release();
  const kept = page.locator('#waitlistList tr[data-pin="kept"]');
  await expect(kept).toHaveAttribute('data-version', '2');
  await expect(kept.locator('.wl-outcome')).toHaveValue('call_back');
});

test('a late patch does not put the phone back', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').selectOption('call_back');
  state.banner = 'Медиалог недоступен';
  state.list = [row({
    patient_name: '',
    patient_phone: '',
    patient_birth: '',
    orig_doctor: '',
    orig_service: '',
    medialog_state: '',
    version: 1,
  })];
  await page.clock.fastForward(11000);
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).not.toContainText('79990001122');
  state.reply = {
    json: {
      success: true,
      data: {
        row: { status: 'contacted', outcome: 'call_back', version: 2, updated_by: 'kash', updated_at: '06.11 12:20' },
        enriched: true,
        full: row({ patient_name: 'Иванов Иван', patient_phone: '+70001112233', version: 2, outcome: 'call_back' }),
      },
    },
  };
  release();
  await expect(page.locator('#waitlistList tr[data-key="max:7"]')).toHaveAttribute('data-version', '2');
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(page.locator('#waitlistList')).not.toContainText('70001112233');
  await expect(page.locator('#waitlistList')).not.toContainText('79990001122');
});

test('a late conflict does not lower the row version', async ({ page }) => {
  const state = await openBoard(page, { items: [row({ version: 3, outcome: 'call_back', status: 'contacted' })] });
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toHaveAttribute('data-version', '3');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await line.locator('.wl-outcome').selectOption('no_answer');
  state.reply = {
    status: 409,
    json: {
      success: false,
      error: 'WAITLIST_CHANGED',
      error_details: { message: 'Строку уже изменил другой сотрудник' },
      data: {
        row: { status: 'contacted', outcome: 'refused', version: 2, updated_by: 'other', updated_at: '06.11 11:00' },
        enriched: true,
        full: row({ patient_name: 'Чужой', patient_phone: '+70001112233', version: 2, outcome: 'refused', medialog_state: 'запись отменена' }),
      },
    },
  };
  release();
  await expect(line).toHaveAttribute('data-version', '3');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(page.locator('#waitlistList')).not.toContainText('70001112233');
  await expect(page.locator('#waitlistList')).not.toContainText('Чужой');
});

test('a late save does not replace a newer row that came back', async ({ page }) => {
  const state = await openBoard(page, { items: [row({ patient_name: 'Иванов Иван', version: 1 })] });
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  const gone = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('q='));
  await page.locator('#wlQuery').fill('неттакого');
  await page.clock.fastForward(400);
  await gone;
  await expect(page.locator('#waitlistList tr')).toHaveCount(0);
  state.list = [row({ patient_name: 'Иванов Иван', version: 4, outcome: 'no_answer', status: 'contacted' })];
  const back = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('per_page=50') && !r.url().includes('q='));
  await page.locator('#wlClear').click();
  await back;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('no_answer');
  state.reply = {
    json: {
      success: true,
      data: {
        row: { status: 'contacted', outcome: 'call_back', version: 3, updated_by: 'kash', updated_at: '06.11 12:20' },
        enriched: true,
        full: row({ patient_phone: '+70001112233', version: 3, outcome: 'call_back' }),
      },
    },
  };
  release();
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('no_answer');
  await expect(page.locator('#waitlistList')).not.toContainText('70001112233');
});

test('logout blocks dashboard requests and polling resumes after failure', async ({ page }) => {
  await openBoard(page, { role: 'admin', items: [row()] });
  let release;
  const held = new Promise(resolve => { release = resolve; });
  await page.route('**/dashboard/logout', async route => {
    await held;
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ success: false, code: 'LOGOUT_FAILED' }),
    });
  });
  let hits = 0;
  await page.route('**/dashboard/**', async route => {
    if (!route.request().url().includes('/dashboard/logout')) hits++;
    await route.fallback();
  });
  await page.locator('#accountMenu summary').click();
  const gone = page.waitForRequest(r => r.url().includes('/dashboard/logout') && r.method() === 'POST');
  await page.locator('#logoutBtn').click();
  await gone;
  hits = 0;
  await page.locator('#tabWaitlist').click();
  await page.locator('#wlQuery').evaluate(el => {
    el.value = 'иван';
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await page.locator('#tabSchedule').click();
  await page.locator('#tabStaff').click();
  await page.locator('#date').evaluate(el => {
    el.value = '2026-11-05';
    el.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await page.locator('#branchButton').click();
  await page.locator('#branchChecks input').first().click();
  await page.locator('#wlNext').evaluate(el => el.click());
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
  await page.clock.fastForward(500);
  expect(hits).toBe(0);
  release();
  await expect(page.locator('#banner')).toContainText('Не удалось выйти — повторите');
  const polled = page.waitForRequest(r => r.url().includes('/dashboard/data'));
  await page.clock.fastForward(11000);
  await polled;
});

function listResponse(page) {
  return page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
}

test('a deferred row does not bring the phone back', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.clock.fastForward(6000);
  await page.locator('.wl-outcome').focus();
  const first = listResponse(page);
  await page.clock.fastForward(5000);
  await first;
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').evaluate(el => el.blur());
  await page.clock.fastForward(6000);
  state.banner = 'Медиалог недоступен';
  state.list = [row({
    patient_name: '',
    patient_phone: '',
    patient_birth: '',
    orig_doctor: '',
    orig_service: '',
    medialog_state: '',
    version: 1,
  })];
  const second = listResponse(page);
  await page.clock.fastForward(10000);
  await second;
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).not.toContainText('79990001122');
  await page.locator('.wl-outcome').focus();
  await page.locator('.wl-outcome').evaluate(el => el.blur());
  await page.clock.fastForward(6000);
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  expect(await page.locator('#waitlistList').evaluate(el => el.innerHTML)).not.toContain('79990001122');
});

test('update row applies newer work without restoring the phone', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.clock.fastForward(6000);
  await page.locator('.wl-outcome').focus();
  state.list = [row({ version: 2, status: 'contacted', outcome: 'call_back', patient_phone: '+79990001122' })];
  const first = listResponse(page);
  await page.clock.fastForward(5000);
  await first;
  await expect(page.getByRole('button', { name: 'Обновить строку' })).toBeVisible();
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.clock.fastForward(6000);
  await page.locator('#waitlistList tr[data-key="max:7"]').dispatchEvent('pointerdown');
  state.banner = 'Медиалог недоступен';
  state.list = [row({
    version: 2,
    status: 'contacted',
    outcome: 'call_back',
    patient_name: '',
    patient_phone: '',
    patient_birth: '',
    medialog_state: '',
  })];
  const second = listResponse(page);
  await page.clock.fastForward(5000);
  await second;
  await expect(page.locator('#banner')).toContainText('Медиалог недоступен');
  await expect(page.locator('#waitlistList')).not.toContainText('79990001122');
  await page.getByRole('button', { name: 'Обновить строку' }).click();
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(line).toHaveAttribute('data-version', '2');
  expect(await page.locator('#waitlistList').evaluate(el => el.innerHTML)).not.toContain('79990001122');
});

test('search keeps work fields of rows that leave and come back', async ({ page }) => {
  const state = await openBoard(page, {
    items: [
      row({ id: 7, patient_name: 'Иванов Иван', version: 4, status: 'contacted', outcome: 'call_back' }),
      row({ id: 8, patient_name: 'Петров Пётр', version: 4, status: 'closed', outcome: 'refused' }),
    ],
  });
  await page.locator('#tabWaitlist').click();
  const open = page.locator('#waitlistList tr[data-key="max:7"]');
  const closed = page.locator('#waitlistList tr[data-key="max:8"]');
  await expect(open.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(open).toHaveAttribute('data-version', '4');
  await expect(closed.getByRole('button', { name: 'Вернуть в работу' })).toBeVisible();
  await expect(closed).toHaveAttribute('data-version', '4');
  const searched = page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('q='));
  await page.locator('#wlQuery').fill('zzz');
  await page.clock.fastForward(400);
  await searched;
  await expect(page.locator('#waitlistList tr[data-key]')).toHaveCount(0);
  const back = page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('per_page=50') && !r.url().includes('q='));
  await page.locator('#wlClear').click();
  await back;
  await expect(open.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(open).toHaveAttribute('data-version', '4');
  await expect(closed.getByRole('button', { name: 'Вернуть в работу' })).toBeVisible();
  await expect(closed).toHaveAttribute('data-version', '4');
  const saved = page.waitForRequest(r => r.method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/8'));
  await closed.getByRole('button', { name: 'Вернуть в работу' }).click();
  expect((await saved).postDataJSON()).toMatchObject({ action: 'reopen', version: 4 });
  expect(state.posts.at(-1).version).toBe(4);
});

test('a save that lands off the list comes back at the saved version', async ({ page }) => {
  const state = await openBoard(page, {
    items: [row({ patient_name: 'Иванов Иван', version: 4, status: 'contacted', outcome: 'call_back' })],
  });
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('no_answer');
  const gone = page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('q='));
  await page.locator('#wlQuery').fill('неттакого');
  await page.clock.fastForward(400);
  await gone;
  await expect(page.locator('#waitlistList tr[data-key]')).toHaveCount(0);
  state.reply = {
    json: {
      success: true,
      data: {
        row: { status: 'contacted', outcome: 'no_answer', version: 6, updated_by: 'kash', updated_at: '06.11 12:40' },
      },
    },
  };
  state.list = [row({ patient_name: 'Иванов Иван', version: 4, status: 'contacted', outcome: 'call_back' })];
  const patched = page.waitForResponse(r => r.request().method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  release();
  await patched;
  const back = page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('per_page=50') && !r.url().includes('q='));
  await page.locator('#wlClear').click();
  await back;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toHaveAttribute('data-version', '6');
  await expect(line.locator('.wl-outcome')).toHaveValue('no_answer');
});

test('a late patch does not undo a newer list while the row is focused', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.clock.fastForward(6000);
  await page.locator('.wl-outcome').selectOption('call_back');
  await page.locator('.wl-outcome').focus();
  state.list = [row({ version: 4, status: 'contacted', outcome: 'no_answer' })];
  const first = listResponse(page);
  await page.clock.fastForward(5000);
  await first;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toHaveAttribute('data-version', '1');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  state.reply = {
    json: {
      success: true,
      data: {
        row: { status: 'contacted', outcome: 'call_back', version: 2, updated_by: 'kash', updated_at: '06.11 12:20' },
      },
    },
  };
  release();
  await expect(line.locator('.wl-saved')).not.toContainText('Сохранено');
  await expect(line).toHaveAttribute('data-version', '1');
  await page.locator('.wl-outcome').evaluate(el => el.blur());
  await page.clock.fastForward(6000);
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('no_answer');
});

test('a late moved error keeps a row that a newer list already returned', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.clock.fastForward(6000);
  await page.locator('.wl-outcome').selectOption('call_back');
  await page.locator('.wl-outcome').focus();
  state.list = [row({ version: 4, status: 'contacted', outcome: 'no_answer' })];
  const first = listResponse(page);
  await page.clock.fastForward(5000);
  await first;
  state.reply = {
    status: 403,
    json: {
      success: false,
      error: 'Строка перенесена',
      error_details: { code: 'WAITLIST_MOVED', message: 'Строка перенесена в другой филиал' },
    },
  };
  const reread = listResponse(page);
  release();
  await reread;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toBeVisible();
  await page.locator('.wl-outcome').evaluate(el => el.blur());
  await page.clock.fastForward(6000);
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('no_answer');
});

test('a password change that arrives during logout does not navigate', async ({ page }) => {
  const state = await openBoard(page, { items: [] });
  await page.waitForLoadState('networkidle');
  let releaseGet;
  state.holdData = new Promise(resolve => { releaseGet = resolve; });
  let releaseOut;
  await page.route('**/dashboard/logout', async route => {
    await new Promise(resolve => { releaseOut = resolve; });
    await route.fulfill({ status: 503, contentType: 'application/json', body: '{"success":false}' });
  });
  const started = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/data'));
  await page.evaluate(() => { load(); });
  await started;
  await page.locator('#accountMenu summary').click();
  const logging = page.waitForRequest(r => r.url().includes('/dashboard/logout') && r.method() === 'POST');
  await page.locator('#logoutBtn').click();
  await logging;
  state.dataReply = {
    status: 403,
    contentType: 'application/json',
    body: JSON.stringify({
      success: false,
      error: 'Нужно сменить пароль',
      error_details: { code: 'PASSWORD_CHANGE_REQUIRED' },
    }),
  };
  const answered = page.waitForResponse(r => r.url().includes('/dashboard/data') && r.status() === 403);
  releaseGet();
  await answered;
  await expect(page).not.toHaveURL(/password/);
  await expect(page.locator('#board')).toBeVisible();
  releaseOut();
});

test('a network failure during logout does not show a banner', async ({ page }) => {
  const state = await openBoard(page, { items: [] });
  await page.waitForLoadState('networkidle');
  let releaseGet;
  state.holdData = new Promise(resolve => { releaseGet = resolve; });
  let releaseOut;
  await page.route('**/dashboard/logout', async route => {
    await new Promise(resolve => { releaseOut = resolve; });
    await route.fulfill({ status: 503, contentType: 'application/json', body: '{"success":false}' });
  });
  const started = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/data'));
  await page.evaluate(() => { load(); });
  await started;
  await page.locator('#accountMenu summary').click();
  const logging = page.waitForRequest(r => r.url().includes('/dashboard/logout') && r.method() === 'POST');
  await page.locator('#logoutBtn').click();
  await logging;
  state.abortData = true;
  const failed = page.waitForEvent('requestfailed', r => r.url().includes('/dashboard/data'));
  releaseGet();
  await failed;
  await expect(page.locator('#banner')).not.toContainText('Не удалось обновить');
  releaseOut();
});

test('the staff tab does not reload the waitlist after a save', async ({ page }) => {
  const state = await openBoard(page, { role: 'admin', items: [row()] });
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await expect(page.locator('#tabStaff')).toBeVisible();
  await page.locator('#tabWaitlist').click();
  const sent = page.waitForRequest(r => r.method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  await page.locator('.wl-outcome').selectOption('call_back');
  await sent;
  await page.locator('#tabStaff').click();
  const leaked = page.waitForRequest(
    r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'),
    { timeout: 1500 },
  ).then(() => true).catch(() => false);
  const patched = page.waitForResponse(r => r.request().method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  release();
  await patched;
  expect(await leaked).toBe(false);
  const again = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  await page.locator('#tabWaitlist').click();
  await again;
  await expect(page.locator('#waitlistList tr[data-key="max:7"]')).toHaveAttribute('data-version', '2');
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
});

test('confirming a close does not save a version the dialog did not show', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  state.list = [row({ version: 4, status: 'contacted', outcome: 'call_back', updated_by: 'other' })];
  const polled = listResponse(page);
  await page.clock.fastForward(10000);
  await polled;
  const before = state.posts.length;
  await page.locator('#confirmYes').click();
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(line).toContainText('Строку изменил другой сотрудник — проверьте и повторите');
  await expect(line.locator('.wl-saved')).not.toContainText('Сохраняется');
  expect(state.posts.length).toBe(before);
});

test('reopening does not save a version newer than the row on screen', async ({ page }) => {
  const state = await openBoard(page, {
    items: [row({ version: 1, status: 'closed', outcome: 'refused' })],
  });
  await page.locator('#tabWaitlist').click();
  await page.clock.fastForward(6000);
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await line.getByRole('button', { name: 'Вернуть в работу' }).focus();
  state.list = [row({ version: 4, status: 'contacted', outcome: 'call_back', updated_by: 'other' })];
  const polled = listResponse(page);
  await page.clock.fastForward(5000);
  await polled;
  await expect(line).toHaveAttribute('data-version', '1');
  const before = state.posts.length;
  await line.getByRole('button', { name: 'Вернуть в работу' }).click();
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(line).toContainText('Строку изменил другой сотрудник — проверьте и повторите');
  await expect(line.locator('.wl-saved')).not.toContainText('Сохраняется');
  expect(state.posts.length).toBe(before);
});

test('a busy row sends the version on screen and shows the newer row', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await page.clock.fastForward(6000);
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await line.locator('.wl-outcome').focus();
  state.list = [row({ version: 4, status: 'contacted', outcome: 'call_back', updated_by: 'other' })];
  const polled = listResponse(page);
  await page.clock.fastForward(5000);
  await polled;
  await expect(line).toHaveAttribute('data-version', '1');
  state.reply = {
    status: 409,
    json: {
      success: false,
      error: 'WAITLIST_CHANGED',
      error_details: { message: 'Строку уже изменил другой сотрудник' },
      data: {
        row: { status: 'contacted', outcome: 'call_back', version: 4, updated_by: 'other', updated_at: '06.11 12:40' },
      },
    },
  };
  const sent = page.waitForRequest(r => r.method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  await line.locator('.wl-outcome').selectOption('no_answer');
  expect((await sent).postDataJSON()).toMatchObject({ outcome: 'no_answer', version: 1 });
  await expect(line).toHaveAttribute('data-version', '4');
  await expect(line.locator('.wl-outcome')).toHaveValue('call_back');
  await expect(line.locator('.wl-saved')).not.toContainText('Сохраняется');
});

test('a moved error hides the patient when the reread fails', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').selectOption('call_back');
  state.abortList = true;
  state.reply = {
    status: 403,
    json: {
      success: false,
      error: 'Строка перенесена',
      error_details: { code: 'WAITLIST_MOVED', message: 'Строка перенесена в другой филиал' },
    },
  };
  const failed = page.waitForEvent('requestfailed', r => r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  release();
  await failed;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Доступ к строке проверяется');
  await expect(line).not.toContainText('Иванов');
  await expect(line).not.toContainText('79990001122');
  await expect(line.locator('.wl-outcome')).toBeHidden();
  await expect(line.getByRole('button')).toHaveCount(0);
});

test('a reread after a moved error brings the patient back', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  state.reply = {
    status: 403,
    json: {
      success: false,
      error: 'Строка перенесена',
      error_details: { code: 'WAITLIST_MOVED', message: 'Строка перенесена в другой филиал' },
    },
  };
  const reread = listResponse(page);
  release();
  await reread;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Иванов Иван');
  await expect(line).toContainText('+79990001122');
  await expect(line.locator('.wl-outcome')).toBeVisible();
  await expect(line.locator('.wl-saved')).not.toContainText('Доступ к строке проверяется');
});

test('a list sent before a moved error does not bring the patient back', async ({ page }) => {
  const state = await openBoard(page, {});
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
  let releaseList;
  state.holdList = new Promise(resolve => { releaseList = resolve; });
  const sent = page.waitForRequest(r => r.method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  await page.evaluate(() => { loadWaitlist(); });
  await sent;
  let releasePatch;
  state.holdPatch = new Promise(resolve => { releasePatch = resolve; });
  const patching = page.waitForRequest(r => r.method() === 'PATCH' && r.url().includes('/dashboard/waitlist/max/7'));
  await page.locator('.wl-outcome').selectOption('no_answer');
  await patching;
  let releaseNext;
  state.holdList = new Promise(resolve => { releaseNext = resolve; });
  state.reply = {
    status: 403,
    json: {
      success: false,
      error: 'Строка перенесена',
      error_details: { code: 'WAITLIST_MOVED', message: 'Строка перенесена в другой филиал' },
    },
  };
  const stale = page.waitForResponse(r => r.request().method() === 'GET' && r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  releasePatch();
  await expect(page.locator('#waitlistList tr[data-key="max:7"]')).toContainText('Доступ к строке проверяется');
  releaseList();
  await stale;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).not.toContainText('Иванов');
  await expect(line).not.toContainText('79990001122');
  await expect(line.locator('.wl-outcome')).toBeHidden();
  await expect(line.getByRole('button')).toHaveCount(0);
  releaseNext();
});

const notFound = {
  status: 404,
  json: { success: false, error: 'Строка листа не найдена' },
};

test('a not-found save hides the patient when the reread fails', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').selectOption('call_back');
  state.abortList = true;
  state.reply = notFound;
  const failed = page.waitForEvent('requestfailed', r => r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  release();
  await failed;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Строка больше не доступна — список обновляется');
  await expect(line).not.toContainText('Иванов');
  await expect(line).not.toContainText('79990001122');
  await expect(line.locator('.wl-outcome')).toBeHidden();
  await expect(line.getByRole('button')).toHaveCount(0);
});

test('a not-found save drops the row when the reread omits it', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList tr[data-key="max:7"]')).toBeVisible();
  await page.locator('.wl-outcome').selectOption('call_back');
  state.list = [];
  state.reply = notFound;
  const reread = listResponse(page);
  release();
  await reread;
  await expect(page.locator('#waitlistList tr[data-key="max:7"]')).toHaveCount(0);
});

test('a not-found save brings the row back when the reread has it', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  state.reply = notFound;
  const reread = listResponse(page);
  release();
  await reread;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Иванов Иван');
  await expect(line).toContainText('+79990001122');
  await expect(line.locator('.wl-outcome')).toBeVisible();
  await expect(line.locator('.wl-saved')).not.toContainText('Строка больше не доступна');
});

const forbidden = {
  status: 403,
  json: {
    success: false,
    error: 'Нет доступа',
    error_details: { code: 'FORBIDDEN', message: 'Филиал строки недоступен' },
  },
};

test('a forbidden save hides the patient when the reread fails', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
  await expect(page.locator('#waitlistList')).toContainText('+79990001122');
  await page.locator('.wl-outcome').selectOption('call_back');
  state.abortList = true;
  state.reply = forbidden;
  const failed = page.waitForEvent('requestfailed', r => r.url().includes('/dashboard/waitlist') && r.url().includes('per_page=50'));
  release();
  await failed;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Нет доступа к строке — список обновляется');
  await expect(line).not.toContainText('Иванов');
  await expect(line).not.toContainText('79990001122');
  await expect(line.locator('.wl-outcome')).toBeHidden();
  await expect(line.getByRole('button')).toHaveCount(0);
});

test('a forbidden save brings the row back when the reread has it', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  state.reply = forbidden;
  const reread = listResponse(page);
  release();
  await reread;
  const line = page.locator('#waitlistList tr[data-key="max:7"]');
  await expect(line).toContainText('Иванов Иван');
  await expect(line).toContainText('+79990001122');
  await expect(line.locator('.wl-outcome')).toBeVisible();
  await expect(line.locator('.wl-saved')).not.toContainText('Нет доступа к строке');
});

test('a forbidden save closes the close dialog and does not send it', async ({ page }) => {
  const state = await openBoard(page, {});
  let release;
  state.holdPatch = new Promise(resolve => { release = resolve; });
  await page.locator('#tabWaitlist').click();
  await page.locator('.wl-outcome').selectOption('call_back');
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  state.reply = forbidden;
  release();
  await expect(page.locator('#confirmModal')).toBeHidden();
  expect(state.posts).toHaveLength(1);
  expect(state.posts[0]).toMatchObject({ outcome: 'call_back', version: 1 });
});
