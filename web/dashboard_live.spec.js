import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '@playwright/test';

const FIXED = '2026-11-06T10:39:00.000Z';
const TODAY = '2026-11-06';
const dir = dirname(fileURLToPath(import.meta.url));
const html = readFileSync(join(dir, 'dashboard.html'));

const TIMES = ['08:00', '09:00', '10:00', '11:00', '12:00', '13:00', '13:10', '13:40', '15:00', '16:00', '17:00', '18:00', '19:00', '20:00'];

function appointment(time, id, extra = {}) {
  return {
    planning_id: id,
    time,
    patient_name: extra.patient_name || `Пациент ${time}`,
    doctor_name: extra.doctor_name === undefined ? 'Врач' : extra.doctor_name,
    confirmation: extra.confirmation === undefined ? 'confirmed' : extra.confirmation,
    confirmation_hint: '',
    is_waitlist: false,
    on_waitlist: false,
  };
}

function daySchedule(date, mutate) {
  const branches = [
    { branch_id: 106, branch_code: 'kashirka', name: 'Каширка' },
    { branch_id: 3, branch_code: 'kurkino', name: 'Куркино' },
    { branch_id: 496, branch_code: 'kurkino2', name: 'Куркино 2' },
  ].map((branch, bi) => ({
    ...branch,
    appointments: TIMES.map((time, i) => appointment(time, 1000 + bi * 100 + i, {
      confirmation: time === '19:00' ? null : 'confirmed',
      doctor_name: branch.branch_id === 496 && time === '20:00' ? '' : 'Врач',
    })),
  }));
  if (mutate) mutate(branches);
  return { success: true, data: { date, branches } };
}

function waitlistBody(item) {
  return {
    success: true,
    data: { open_total: 1, page: 1, total: 1, per_page: 50, items: [item] },
  };
}

function baseItem(extra = {}) {
  return {
    id: 7,
    status: 'new',
    outcome: '',
    patient_name: 'Иванов Иван',
    patient_phone: '+79990001122',
    patient_birth: '',
    created_at_msk: '06.11 09:00',
    reason: 'declined',
    orig_date_start: '06.11 10:00',
    branch_id: 106,
    orig_doctor: 'Петров',
    orig_service: 'Терапевт',
    medialog_state: 'запись есть',
    future_label: '—',
    ...extra,
  };
}

let server;
let base;

test.beforeAll(async () => {
  server = createServer((req, res) => {
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
    res.end(html);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  base = `http://127.0.0.1:${server.address().port}/dashboard`;
});

test.afterAll(async () => {
  await new Promise(resolve => server.close(resolve));
});

async function openDashboard(page, state) {
  await page.clock.install({ time: new Date(FIXED) });
  await page.addInitScript(() => {
    const orig = window.fetch.bind(window);
    window.fetch = (url, opts) => {
      const u = String(url);
      if (window.__changeOnNextData && u.includes('/dashboard/data')) {
        window.__changeOnNextData = false;
        const date = document.getElementById('date');
        date.value = '2026-11-05';
        date.dispatchEvent(new Event('change'));
      }
      return orig(url, opts);
    };
  });
  await page.route('**/dashboard/data**', async route => {
    state.unfilled++;
    state.maxUnfilled = Math.max(state.maxUnfilled, state.unfilled);
    const url = new URL(route.request().url());
    const date = url.searchParams.get('date');
    const slow = state.slowOnce;
    if (slow) state.slowOnce = false;
    if (slow) {
      await new Promise(resolve => setTimeout(resolve, 200));
      state.duringSlow = state.unfilled;
      await new Promise(resolve => setTimeout(resolve, 200));
    }
    let body;
    if (slow) body = daySchedule(date, branches => {
      branches[0].appointments = [appointment('10:00', 1, { patient_name: 'ПОЗДНИЙ', confirmation: 'confirmed' })];
    });
    else if (date !== TODAY && state.otherDay) body = daySchedule(date, branches => {
      branches.forEach(b => {
        b.appointments = [appointment('09:00', 3 + b.branch_id, { patient_name: 'Другой день', confirmation: 'confirmed' })];
      });
    });
    else if (date !== TODAY) body = daySchedule(date, branches => {
      branches.forEach(b => {
        b.appointments = [appointment('10:00', 2 + b.branch_id, { patient_name: 'НОВЫЙ', confirmation: 'confirmed' })];
      });
    });
    else body = daySchedule(date, branches => {
      if (state.confirm19) {
        branches[0].appointments.forEach(a => {
          if (a.time === '19:00') a.confirmation = state.confirm19;
        });
      }
      if (state.insertEarly) {
        branches[0].appointments.unshift(appointment('07:30', 50, { patient_name: 'Ранний', confirmation: 'confirmed' }));
      }
    });
    state.unfilled--;
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  await page.route('**/dashboard/waitlist**', async route => {
    if (route.request().method() !== 'GET') {
      try { state.saved = route.request().postDataJSON(); } catch (e) { state.saved = {}; }
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true,"data":{}}' });
      return;
    }
    const saved = state.saved || {};
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(waitlistBody(baseItem({
        patient_name: state.wlName,
        status: state.forceStatus || saved.status || state.wlStatus,
        outcome: state.wlOutcome || saved.outcome || '',
      }))),
    });
  });
  await page.goto(base);
  await expect(page.locator('#board .row').first()).toBeVisible();
}

function freshState() {
  return {
    confirm19: null,
    insertEarly: false,
    otherDay: false,
    slowOnce: false,
    unfilled: 0,
    maxUnfilled: 0,
    duringSlow: 0,
    wlName: 'Иванов Иван',
    wlStatus: 'new',
    wlOutcome: '',
    forceStatus: '',
    saved: null,
  };
}

test('scroll survives three status ticks and unchanged rows stay the same nodes', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  const list = page.locator('#board .col').first().locator('.list');
  const before = await list.evaluate(el => {
    el.scrollTop = el.scrollHeight;
    window.__rows = [...el.querySelectorAll('.row')];
    return { scrollTop: el.scrollTop, count: window.__rows.length };
  });
  expect(before.scrollTop).toBeGreaterThan(120);
  expect(before.count).toBeGreaterThan(5);
  await page.screenshot({ path: join(dir, 'live-shots', 'before-tick.png') });

  state.confirm19 = 'confirmed';
  for (let i = 0; i < 3; i++) {
    await page.clock.fastForward(10_000);
    await expect(list.locator('.row[data-time="19:00"] .pill').first()).toHaveText('придёт');
  }
  const after = await list.evaluate(el => {
    const now = [...el.querySelectorAll('.row')];
    return {
      scrollTop: el.scrollTop,
      same: window.__rows.length === now.length && window.__rows.every((node, i) => node === now[i]),
    };
  });
  expect(after.scrollTop).toBe(before.scrollTop);
  expect(after.same).toBe(true);
  await page.screenshot({ path: join(dir, 'live-shots', 'after-tick.png') });
});

test('a row inserted above the viewport leaves the visible row in place', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  const list = page.locator('#board .col').first().locator('.list');
  await list.evaluate(el => { el.scrollTop = el.scrollHeight; });
  const row = list.locator('.row[data-time="20:00"]');
  const top = await row.evaluate(el => el.getBoundingClientRect().top);
  state.insertEarly = true;
  await page.clock.fastForward(10_000);
  await expect(list.locator('.row[data-time="07:30"]')).toHaveCount(1);
  const topAfter = await row.evaluate(el => el.getBoundingClientRect().top);
  expect(Math.abs(topAfter - top)).toBeLessThan(2);
});

test('an open waitlist choice survives a tick and applies after five idle seconds', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-outcome')).toBeVisible();
  await page.clock.fastForward(8_000);
  await page.locator('.wl-status').selectOption('closed');
  const posted = page.waitForResponse(r => r.url().includes('/dashboard/waitlist') && r.request().method() === 'POST');
  const reloaded = page.waitForResponse(r => r.url().includes('/dashboard/waitlist') && r.request().method() === 'GET');
  await page.locator('.wl-outcome').selectOption('refused');
  await posted;
  await reloaded;
  await page.locator('.wl-outcome').focus();
  await expect(page.locator('.wl-outcome')).toBeFocused();
  state.wlName = 'Петров Пётр';
  state.wlOutcome = 'call_back';
  await page.clock.fastForward(2_000);
  await expect(page.locator('.wl-outcome')).toHaveValue('refused');
  await expect(page.locator('.wl-status')).toHaveValue('closed');
  await expect(page.locator('.wl-outcome')).toBeFocused();
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
  await page.locator('.wl-outcome').evaluate(el => el.blur());
  await page.clock.fastForward(5_000);
  await expect(page.locator('#waitlistList')).toContainText('Петров Пётр');
  await expect(page.locator('.wl-outcome')).toHaveValue('call_back');
});

test('a server status change during editing shows the other-staff notice', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('.wl-status')).toBeVisible();
  await page.clock.fastForward(8_000);
  await page.locator('.wl-status').selectOption('closed');
  const posted = page.waitForResponse(r => r.url().includes('/dashboard/waitlist') && r.request().method() === 'POST');
  const reloaded = page.waitForResponse(r => r.url().includes('/dashboard/waitlist') && r.request().method() === 'GET');
  await page.locator('.wl-outcome').selectOption('refused');
  await posted;
  await reloaded;
  state.forceStatus = 'contacted';
  state.wlName = 'Чужое имя';
  await page.clock.fastForward(2_000);
  await expect(page.locator('.wl-conflict')).toContainText('Изменено другим сотрудником');
  await expect(page.getByRole('button', { name: 'Обновить строку' })).toBeVisible();
  await expect(page.locator('.wl-status')).toHaveValue('closed');
  await expect(page.locator('.wl-outcome')).toHaveValue('refused');
  await expect(page.locator('#waitlistList')).toContainText('Иванов Иван');
});

test('reload after another date returns to today and scrolls to 13:09', async ({ page }) => {
  const state = freshState();
  state.otherDay = true;
  await openDashboard(page, state);
  await page.locator('#date').evaluate(el => {
    el.value = '2026-11-04';
    el.dispatchEvent(new Event('change'));
  });
  await expect(page.locator('#board')).toContainText('Другой день');
  await page.reload();
  await expect(page.locator('#date')).toHaveValue(TODAY);
  await expect(page.locator('#board .row[data-time="13:10"]').first()).toBeVisible();
  const cols = page.locator('#board .col');
  await expect(cols).toHaveCount(3);
  for (const col of await cols.all()) {
    const delta = await col.locator('.list').evaluate(list => {
      const row = [...list.querySelectorAll('.row')].find(r => r.dataset.time === '13:10');
      return row.getBoundingClientRect().top - list.getBoundingClientRect().top;
    });
    expect(Math.abs(delta)).toBeLessThan(2);
    const scrollTop = await col.locator('.list').evaluate(list => list.scrollTop);
    expect(scrollTop).toBeGreaterThan(0);
  }
});

test('a late response after a date change is dropped and requests do not overlap', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  state.maxUnfilled = 0;
  await page.evaluate(() => { window.__changeOnNextData = true; });
  state.slowOnce = true;
  await page.clock.fastForward(10_000);
  await expect(page.locator('#board')).toContainText('НОВЫЙ');
  await expect(page.locator('#board')).not.toContainText('ПОЗДНИЙ');
  await expect(page.locator('#date')).toHaveValue('2026-11-05');
  expect(state.duringSlow).toBe(1);
});

test('the waitlist panel stays hidden on the schedule tab', async ({ page }) => {
  const state = freshState();
  await openDashboard(page, state);
  await expect(page.locator('#waitlistPanel')).toBeHidden();
  await expect(page.locator('#noteWaitlist')).toBeHidden();
  const display = await page.locator('#waitlistPanel').evaluate(el => getComputedStyle(el).display);
  expect(display).toBe('none');
  const note = await page.locator('#noteWaitlist').evaluate(el => getComputedStyle(el).display);
  expect(note).toBe('none');
  const doctor = await page.locator('#board .col').nth(2).locator('.row[data-time="20:00"] .doctor').evaluate(el => getComputedStyle(el).display);
  expect(doctor).toBe('none');
  await expect(page.getByText('Открытых строк нет.')).toHaveCount(0);
});
