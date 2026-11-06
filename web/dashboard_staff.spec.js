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
  const state = { posts: [], role: opts.role || 'registrar', closedHidden: !!opts.closedHidden };
  await page.clock.install({ time: new Date(opts.time || '2026-11-06T10:39:00.000Z') });
  await page.route('**/dashboard/me', async route => {
    await route.fulfill({
      json: {
        success: true,
        data: {
          login: 'kash',
          full_name: 'Каширка Регистратор',
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
    await route.fulfill({ json: opts.schedule || schedule() });
  });
  await page.route('**/dashboard/waitlist**', async route => {
    if (route.request().method() !== 'GET') {
      const body = route.request().postDataJSON();
      state.posts.push(body);
      const saved = row({
        status: body.action === 'reopen' ? 'contacted' : (body.outcome === 'no_answer' || body.outcome === 'call_back' ? 'contacted' : 'closed'),
        outcome: body.action === 'reopen' ? '' : body.outcome,
        version: (body.version || 1) + 1,
      });
      await route.fulfill({ json: { success: true, data: saved } });
      return;
    }
    const url = new URL(route.request().url());
    const items = opts.items || [row()];
    const visible = state.closedHidden && url.searchParams.get('status') === 'open'
      ? items.filter(it => (state.posts.at(-1) || {}).status !== 'closed' || it.id !== 7)
      : items;
    await route.fulfill({
      json: {
        success: true,
        data: { open_total: visible.length, page: 1, total: visible.length, per_page: 50, items: visible },
      },
    });
  });
  await page.route('**/dashboard/staff**', async route => {
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
  await expect(page.locator('#branch')).toBeVisible();
  await expect(page.locator('#branch option')).toHaveCount(4);
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
  await page.locator('.wl-outcome').selectOption('no_answer');
  const first = await saved;
  expect(first.postDataJSON()).toMatchObject({ outcome: 'no_answer', version: 1 });
  await expect(page.locator('#waitlistList .wl-saved')).toContainText('Сохранено ✓');
  await page.screenshot({ path: join(shots, 'waitlist-saved.png') });
  await page.locator('.wl-outcome').selectOption('refused');
  await expect(page.locator('#confirmModal')).toBeVisible();
  await expect(page.locator('#confirmText')).toHaveText(CONFIRM);
  await page.screenshot({ path: join(shots, 'close-confirm.png') });
  await page.locator('#confirmNo').click();
  expect(state.posts).toHaveLength(1);
  await expect(page.locator('.wl-outcome')).toHaveValue('no_answer');
  const closed = page.waitForRequest(r => r.url().includes('/dashboard/waitlist/max/7') && r.method() === 'PATCH');
  await page.locator('.wl-outcome').selectOption('refused');
  await page.locator('#confirmYes').click();
  const second = await closed;
  expect(second.postDataJSON()).toMatchObject({ outcome: 'refused', version: 2 });
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

test('one branch is a label and the pager is hidden on a single page', async ({ page }) => {
  await openBoard(page, { branches: [{ id: 106, name: 'Каширка' }], items: [row()] });
  await expect(page.locator('#branch')).toBeHidden();
  await expect(page.locator('#branchLabel')).toHaveText('Каширка');
  await page.locator('#tabWaitlist').click();
  await expect(page.locator('#wlPager')).toBeHidden();
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
