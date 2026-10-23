'use strict';

// Everything rendered from board data goes through textContent: tickets are
// written by agents and are never trusted as markup.

const COLUMNS = [
  ['inbox', 'À trier'],
  ['todo', 'À faire'],
  ['in_progress', 'En cours'],
  ['blocked', 'Bloqué'],
  ['to_validate', 'À valider'],
];
const STATE_LABEL = {
  inbox: 'à trier', todo: 'à faire', in_progress: 'en cours', blocked: 'bloqué',
  to_validate: 'à valider', done: 'fait', dropped: 'abandonné',
};
const EVENT_LABEL = {
  created: 'a créé le ticket', claimed: 'a pris le ticket', assigned: 'a passé le ticket',
  released: 'a relâché le ticket', note: 'a noté', triaged: 'a validé la trouvaille',
  request_created: 'a demandé', request_approved: 'demande validée', request_refused: 'demande refusée',
  request_failed: 'demande inapplicable', sent_back: 'a renvoyé le ticket', moved: 'a déplacé le ticket',
};
const DAY = 24 * 3600 * 1000;

// A reminder the owner did not do is dropped; say it in their words.
function stateLabel(kind, st) {
  return kind === 'reminder' && st === 'dropped' ? 'pas fait' : STATE_LABEL[st];
}
const ICON = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="#5847d8"/><g fill="#fff"><rect x="6" y="7" width="5.5" height="8" rx="1.5"/><rect x="6" y="17" width="5.5" height="6" rx="1.5" opacity=".55"/><rect x="13.25" y="7" width="5.5" height="12" rx="1.5"/><rect x="20.5" y="7" width="5.5" height="5" rx="1.5"/><rect x="20.5" y="14" width="5.5" height="11" rx="1.5" opacity=".55"/></g>BADGE</svg>';
const BADGE = '<circle cx="25.5" cy="6.5" r="6" fill="#f0a020" stroke="#fff" stroke-width="2"/>';

// The tab shows at a glance whether something waits for the owner.
function markTab(count) {
  document.title = count ? '(' + count + ') Board' : 'Board';
  const svg = ICON.replace('BADGE', count ? BADGE : '');
  document.getElementById('favicon').href = 'data:image/svg+xml,' + encodeURIComponent(svg);
}

const state = { csrf: '', owner: '', touchID: false, board: null, drawer: null, seen: new Map(), refreshing: null, tab: 'pour-toi', stale: false };

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : String(v));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === undefined || kid === null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

async function api(path, method = 'GET', payload) {
  const headers = method === 'GET' ? {} : { 'X-Board-CSRF': state.csrf };
  const init = { method, headers, credentials: 'same-origin' };
  if (payload !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(payload);
  }
  const res = await fetch(path, init);
  let body = null;
  try { body = await res.json(); } catch { /* empty body */ }
  return { status: res.status, body };
}

function hue(name) {
  let x = 0;
  for (const c of name) x = (x * 31 + c.codePointAt(0)) >>> 0;
  return x % 360;
}

function callsign(name) {
  if (!name) return h('span', { class: 'callsign free' }, 'libre');
  const el = h('span', { class: 'callsign', title: 'session ' + name }, name);
  el.style.setProperty('--h', hue(name));
  return el;
}

function ago(stamp) {
  const ms = Date.now() - new Date(stamp).getTime();
  const min = Math.round(ms / 60000);
  if (min < 60) return min + ' min';
  const hours = Math.round(min / 60);
  return hours < 48 ? hours + ' h' : Math.round(hours / 24) + ' j';
}

function shortTime(stamp) {
  const d = new Date(stamp);
  const pad = (n) => String(n).padStart(2, '0');
  return pad(d.getDate()) + '/' + pad(d.getMonth() + 1) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
}

function dueLabel(t) {
  if (!t.due) return null;
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const days = Math.round((new Date(t.due + 'T00:00:00') - today) / DAY);
  const text = days === 0 ? "aujourd'hui" : days === 1 ? 'demain' : days < 0 ? 'en retard (' + t.due + ')' : t.due;
  return h('span', { class: 'due' + (t.overdue ? ' overdue' : '') }, text);
}

function safeLink(url) {
  try {
    const u = new URL(url);
    if (u.protocol === 'https:' || u.protocol === 'http:') {
      return h('a', { href: u.href, target: '_blank', rel: 'noopener noreferrer' }, url);
    }
  } catch { /* not a URL */ }
  return h('span', {}, url);
}

const FINGERPRINT = 'M12 3a8 8 0 0 0-8 8v2M20 13v-2a8 8 0 0 0-12.6-6.5M12 7a4 4 0 0 0-4 4v3a8 8 0 0 0 1.2 4.2M16 11a4 4 0 0 0-4-4M16 11v3a12 12 0 0 1-1 4.8M12 11v3a16 16 0 0 1-2 7';

function fingerprint() {
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', FINGERPRINT);
  path.setAttribute('fill', 'none');
  path.setAttribute('stroke', 'currentColor');
  path.setAttribute('stroke-width', '1.8');
  path.setAttribute('stroke-linecap', 'round');
  svg.append(path);
  return svg;
}

function decisionButtons(req) {
  const feedback = h('span', { class: 'feedback' });
  const approve = h('button', { class: 'btn btn-approve', type: 'button' }, state.touchID ? fingerprint() : null, 'Valider');
  const refuse = h('button', { class: 'btn btn-refuse', type: 'button' }, 'Refuser');
  approve.addEventListener('click', async () => {
    approve.disabled = refuse.disabled = true;
    feedback.textContent = state.touchID ? 'Touch ID sur ton Mac…' : '';
    const res = await api('/api/requests/' + req.id + '/approve', 'POST');
    approve.disabled = refuse.disabled = false;
    if (res.status === 200) feedback.textContent = res.body.status === 'ok' ? 'Validée' : (res.body.message || 'Échec');
    else if (res.status === 408) feedback.textContent = 'Pas de réponse : la demande reste en attente';
    else feedback.textContent = (res.body && res.body.error) || 'Erreur ' + res.status;
  });
  refuse.addEventListener('click', async () => {
    if (!refuse.classList.contains('confirm')) {
      refuse.classList.add('confirm');
      refuse.textContent = 'Confirmer le refus';
      return;
    }
    approve.disabled = refuse.disabled = true;
    const res = await api('/api/requests/' + req.id + '/refuse', 'POST');
    feedback.textContent = res.status === 200 ? 'Refusée' : (res.body && res.body.error) || 'Erreur ' + res.status;
  });
  return h('div', { class: 'actions' }, feedback, approve, refuse);
}

function validationButtons(t) {
  const feedback = h('span', { class: 'feedback' });
  const close = h('button', { class: 'btn btn-approve', type: 'button' }, state.touchID ? fingerprint() : null, 'Clore');
  const back = h('button', { class: 'btn', type: 'button' }, 'Renvoyer');
  const box = h('div', { class: 'actions' }, feedback, close, back);
  close.addEventListener('click', async () => {
    close.disabled = back.disabled = true;
    feedback.textContent = state.touchID ? 'Touch ID sur ton Mac…' : '';
    const res = await api('/api/tickets/' + t.id + '/close', 'POST');
    close.disabled = back.disabled = false;
    if (res.status !== 200) feedback.textContent = (res.body && res.body.error) || 'Erreur ' + res.status;
    else feedback.textContent = { ok: 'Clos', refused: 'Annulé' }[res.body.status] || res.body.message || 'En attente';
  });
  back.addEventListener('click', () => {
    const input = h('input', { class: 'comment', type: 'text', placeholder: 'Ce qui manque', 'aria-label': 'Ce qui manque' });
    const send = h('button', { class: 'btn', type: 'button' }, 'Renvoyer');
    const submit = async () => {
      if (!input.value.trim()) return input.focus();
      send.disabled = true;
      const res = await api('/api/tickets/' + t.id + '/send-back', 'POST', { text: input.value });
      send.disabled = false;
      feedback.textContent = res.status === 200 ? 'Renvoyé' : (res.body && res.body.error) || 'Erreur ' + res.status;
      if (res.status === 200) input.blur();
    };
    send.addEventListener('click', submit);
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
    box.replaceChildren(feedback, input, send);
    input.focus();
  });
  return box;
}

// A reminder is the owner's own task: they say whether they did it, with
// an optional word that lands in the journal.
function reminderButtons(t) {
  const feedback = h('span', { class: 'feedback' });
  const input = h('input', { class: 'comment', type: 'text', placeholder: 'Commentaire (facultatif)', 'aria-label': 'Commentaire sur « ' + t.title + ' »' });
  const done = h('button', { class: 'btn btn-approve', type: 'button' }, state.touchID ? fingerprint() : null, 'Fait');
  const skip = h('button', { class: 'btn', type: 'button' }, state.touchID ? fingerprint() : null, 'Pas fait');
  const settle = (path, label) => async () => {
    done.disabled = skip.disabled = true;
    feedback.textContent = state.touchID ? 'Touch ID sur ton Mac…' : '';
    const res = await api('/api/tickets/' + t.id + '/' + path, 'POST', { text: input.value.trim() });
    done.disabled = skip.disabled = false;
    if (res.status !== 200) feedback.textContent = (res.body && res.body.error) || 'Erreur ' + res.status;
    else feedback.textContent = { ok: label, refused: 'Annulé' }[res.body.status] || res.body.message || 'En attente';
    if (res.status === 200) input.blur();
  };
  done.addEventListener('click', settle('close', 'Fait'));
  skip.addEventListener('click', settle('drop', 'Pas fait'));
  return h('div', { class: 'actions' }, feedback, input, done, skip);
}

function forYou(snap) {
  const items = [];
  const byId = new Map(snap.open.map((t) => [t.id, t]));
  for (const r of snap.pending_requests) {
    const t = byId.get(r.ticket_id);
    items.push(h('div', { class: 'item' },
      h('span', { class: 'kind k-request' }, 'Demande'),
      h('div', { class: 'text' },
        t ? h('button', { class: 'title', type: 'button', onclick: () => openTicket(t.id) }, r.summary) : h('span', {}, r.summary),
        h('span', { class: 'meta' }, h('span', { class: 'mono' }, '#' + r.id), callsign(r.actor === 'terminal' ? '' : r.actor), 'il y a ' + ago(r.created_at))),
      decisionButtons(r)));
  }
  const line = (kind, label, t, extra, actions) => items.push(h('div', { class: 'item' },
    h('span', { class: 'kind ' + kind }, label),
    h('div', { class: 'text' },
      h('button', { class: 'title', type: 'button', onclick: () => openTicket(t.id) }, t.title),
      h('span', { class: 'meta' }, h('span', { class: 'mono' }, '#' + t.id), t.project || 'sans projet', extra)),
    actions || h('span', {})));
  for (const t of snap.open) if (t.state === 'blocked') line('k-blocked', 'Bloqué', t, callsign(t.owner));
  for (const t of snap.open) if (t.state === 'to_validate') line('k-validate', 'À valider', t, [callsign(t.owner), ...(t.links || []).slice(0, 1).map(safeLink)], validationButtons(t));
  const horizon = new Date(Date.now() + DAY).toISOString().slice(0, 10);
  for (const t of snap.open) if (t.kind === 'reminder' && t.due && t.due <= horizon) line('k-reminder', 'Rappel', t, dueLabel(t), reminderButtons(t));
  for (const t of snap.open) if (t.silent) line('k-silent', 'Silencieux', t, [callsign(t.owner), 'depuis ' + ago(t.owner_activity_at)]);

  const section = h('section', { class: 'foryou', 'aria-label': 'Pour toi' },
    h('h2', {}, 'Pour toi'),
    items.length ? items : h('p', { class: 'empty' }, 'Rien ne t’attend.'));
  return { section, count: items.length };
}

function card(t) {
  const el = h('button', { class: 'card' + (t.silent ? ' silent' : ''), type: 'button', onclick: () => openTicket(t.id) },
    h('span', { class: 'row' },
      h('span', { class: 'mono' }, '#' + t.id),
      t.priority <= 1 ? h('span', { class: 'prio prio-' + t.priority }, 'P' + t.priority) : null,
      dueLabel(t)),
    h('span', { class: 'title' }, t.title),
    h('span', { class: 'row' }, callsign(t.owner), t.silent ? h('span', { class: 'silent-mark' }, 'silencieux depuis ' + ago(t.owner_activity_at)) : null));
  const prev = state.seen.get(t.id);
  if (prev !== undefined && prev !== t.updated_at) el.classList.add('fresh');
  return el;
}

function projectSection(p, tickets, closed) {
  const tasks = tickets.filter((t) => t.kind === 'task');
  const reminders = tickets.filter((t) => t.kind === 'reminder')
    .sort((a, b) => (a.due || '9999').localeCompare(b.due || '9999'));
  const columns = COLUMNS.map(([st, label]) => {
    const list = tasks.filter((t) => t.state === st);
    return h('div', { class: 'column col-' + st + (list.length ? '' : ' empty') },
      h('h3', {}, h('span', {}, label), h('span', { class: 'mono' }, list.length || '')),
      list.map(card));
  });
  return h('section', { class: 'project' },
    h('div', { class: 'project-head' },
      h('h2', {}, p.name), p.slug ? h('span', { class: 'slug' }, p.slug) : null, p.mode ? h('span', { class: 'mode' }, 'mode ' + p.mode) : null),
    reminders.length ? h('section', { class: 'reminder-list', 'aria-label': 'Rappels' },
      h('h3', {}, 'Rappels pour toi', h('span', { class: 'mono' }, reminders.length)),
      reminders.map((t) => h('div', { class: 'item' },
        t.due ? dueLabel(t) : h('span', { class: 'due' }, 'sans date'),
        h('div', { class: 'text' }, h('button', { class: 'title', type: 'button', onclick: () => openTicket(t.id) }, t.title)),
        reminderButtons(t)))) : null,
    tasks.length ? h('div', { class: 'columns' }, columns) : h('p', { class: 'empty-project' }, 'Aucune tâche ouverte dans ce projet.'),
    closed && closed.length ? h('details', { class: 'closed' },
      h('summary', {}, 'Fermés ces 3 derniers jours (' + closed.length + ')'),
      h('ul', {}, closed.map((t) => h('li', {},
        h('span', { class: 'mono' }, '#' + t.id + ' '), h('button', { type: 'button', onclick: () => openTicket(t.id) }, t.title),
        ' ' + stateLabel(t.kind, t.state))))) : null);
}

function tabFromHash() {
  const id = decodeURIComponent(location.hash.slice(1));
  return id || 'pour-toi';
}

function tabBar(snap, mineCount) {
  const counted = snap.projects.map((p) => ({ id: 'p/' + p.slug, label: p.slug, count: snap.open.filter((t) => t.project === p.slug).length }));
  counted.sort((a, b) => (b.count > 0) - (a.count > 0) || a.label.localeCompare(b.label));
  const entries = [{ id: 'pour-toi', label: 'Pour toi', count: mineCount }, ...counted];
  const loose = snap.open.filter((t) => !t.project).length;
  if (loose) entries.push({ id: 'sans-projet', label: 'sans projet', count: loose });
  return h('nav', { class: 'tabs', 'aria-label': 'Projets' }, entries.map((e) => h('a', {
    href: '#' + e.id,
    class: 'tab' + (state.tab === e.id ? ' active' : '') + (e.count ? '' : ' muted') + (e.id === 'pour-toi' ? ' mine' : ''),
    'aria-current': state.tab === e.id ? 'page' : null,
  }, e.label, e.count ? h('span', { class: 'tab-count' }, e.count) : null)));
}

function render() {
  const main = document.getElementById('main');
  const snap = state.board;
  if (!snap) return;
  if (snap.locked) {
    main.replaceChildren(h('p', { class: 'notice' }, 'Le board est verrouillé. Lance ', h('code', {}, 'board unlock'), ' dans ton terminal.'));
    document.getElementById('counts').replaceChildren();
    return;
  }
  const mine = forYou(snap);
  let content;
  const project = state.tab.startsWith('p/') && snap.projects.find((p) => 'p/' + p.slug === state.tab);
  if (project) {
    content = projectSection(project, snap.open.filter((t) => t.project === project.slug), snap.recently_closed.filter((t) => t.project === project.slug));
  } else if (state.tab === 'sans-projet') {
    content = projectSection({ name: 'Sans projet', slug: '' }, snap.open.filter((t) => !t.project), snap.recently_closed.filter((t) => !t.project));
  } else {
    state.tab = 'pour-toi';
    content = mine.section;
  }
  main.replaceChildren(tabBar(snap, mine.count), content);
  markTab(mine.count);

  document.getElementById('counts').replaceChildren(
    h('strong', {}, mine.count + ' pour toi'), ' · ' + snap.open.filter((t) => t.kind === 'task').length + ' ouverts');
  state.seen = new Map([...snap.open, ...snap.recently_closed].map((t) => [t.id, t.updated_at]));
}

async function refresh() {
  if (state.refreshing) return state.refreshing;
  state.refreshing = (async () => {
    const res = await api('/api/board');
    if (res.status === 401) return signedOut();
    if (res.status === 200) {
      state.board = res.body;
      // Never wipe what the owner is typing: render once the field is left.
      if (document.activeElement && document.activeElement.classList.contains('comment')) {
        state.stale = true;
        return;
      }
      render();
      if (state.drawer) await openTicket(state.drawer, true);
    }
  })().finally(() => { state.refreshing = null; });
  return state.refreshing;
}

function journalLine(kind, e) {
  let what = EVENT_LABEL[e.kind] || e.kind;
  if (e.kind === 'state') {
    try {
      const to = JSON.parse(e.data).to;
      what = kind === 'reminder' && (to === 'done' || to === 'dropped')
        ? 'a marqué le rappel « ' + stateLabel(kind, to) + ' »'
        : 'a passé le ticket en « ' + STATE_LABEL[to] + ' »';
    } catch { /* keep the raw kind */ }
  }
  return h('li', {},
    h('time', { datetime: e.at }, shortTime(e.at)),
    h('div', {}, callsign(e.actor === 'terminal' ? state.owner : e.actor), ' ', h('span', { class: 'what' }, what),
      e.proof ? h('span', { class: 'proof' }, e.proof) : null,
      e.body ? h('span', { class: 'body' }, e.body) : null));
}

async function openTicket(id, quiet) {
  const res = await api('/api/tickets/' + id);
  if (res.status !== 200) {
    if (!quiet) closeDrawer();
    return;
  }
  const d = res.body;
  const t = d.ticket;
  state.drawer = id;
  const fact = (text) => h('span', {}, text);
  const content = h('div', {},
    h('h2', {}, t.title),
    h('div', { class: 'facts' },
      h('span', { class: 'mono' }, '#' + t.id), fact(stateLabel(t.kind, t.state)), fact(t.project || 'sans projet'), fact('P' + t.priority), dueLabel(t),
      t.kind === 'reminder' ? fact('rappel pour toi') : callsign(t.owner)),
    t.state === 'to_validate' ? [h('h3', {}, 'Validation'), validationButtons(t)] : null,
    t.kind === 'reminder' && t.state === 'todo' ? [h('h3', {}, 'Rappel'), reminderButtons(t)] : null,
    t.details ? h('pre', { class: 'details' }, t.details) : null,
    d.pending_requests && d.pending_requests.length
      ? [h('h3', {}, 'Demandes en attente'), d.pending_requests.map((r) => h('div', { class: 'item' },
        h('span', { class: 'kind k-request' }, '#' + r.id), h('div', { class: 'text' }, r.summary), decisionButtons(r)))]
      : null,
    t.links && t.links.length ? [h('h3', {}, 'Liens'), h('div', { class: 'links' }, t.links.map((l) => h('div', {}, safeLink(l))))] : null,
    d.sub_tickets && d.sub_tickets.length
      ? [h('h3', {}, 'Sous-tickets'), d.sub_tickets.map((c) => h('div', {}, h('button', { class: 'card', type: 'button', onclick: () => openTicket(c.id) },
        h('span', { class: 'row' }, h('span', { class: 'mono' }, '#' + c.id), STATE_LABEL[c.state], callsign(c.owner)), h('span', { class: 'title' }, c.title))))]
      : null,
    d.circle.length ? [h('h3', {}, 'Cercle'), h('div', { class: 'chips' }, d.circle.map(callsign))] : null,
    h('h3', {}, 'Journal'),
    h('ol', { class: 'journal' }, d.events.map((e) => journalLine(t.kind, e))));
  document.getElementById('drawer-body').replaceChildren(content);
  const drawer = document.getElementById('drawer');
  if (drawer.hidden) {
    drawer.hidden = false;
    document.getElementById('drawer-close').focus();
  }
}

function closeDrawer() {
  state.drawer = null;
  document.getElementById('drawer').hidden = true;
}

function signedOut() {
  document.getElementById('main').replaceChildren(
    h('p', { class: 'notice' }, 'Session expirée. Lance ', h('code', {}, 'board open'), ' dans ton terminal.'));
  setLive(false, 'hors ligne');
}

function setLive(on, text) {
  const live = document.getElementById('live');
  live.dataset.state = on ? 'on' : 'off';
  live.textContent = text;
}

function connect() {
  const events = new EventSource('/api/events');
  let timer = null;
  events.addEventListener('open', () => { setLive(true, 'en direct'); refresh(); });
  events.addEventListener('error', () => setLive(false, 'reconnexion…'));
  events.addEventListener('changed', () => {
    clearTimeout(timer);
    timer = setTimeout(refresh, 120);
  });
}

async function init() {
  document.getElementById('drawer-close').addEventListener('click', closeDrawer);
  state.tab = tabFromHash();
  window.addEventListener('hashchange', () => { state.tab = tabFromHash(); render(); window.scrollTo(0, 0); });
  document.addEventListener('focusout', (e) => {
    if (state.stale && e.target.classList && e.target.classList.contains('comment')) {
      state.stale = false;
      setTimeout(() => { render(); if (state.drawer) openTicket(state.drawer, true); }, 200);
    }
  });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeDrawer(); });
  const session = await api('/api/session');
  if (session.status !== 200) return signedOut();
  state.csrf = session.body.csrf;
  state.owner = session.body.owner;
  state.touchID = session.body.touch_id === true;
  connect();
  await refresh();
  // Silence and due dates move with the clock, not with events.
  setInterval(refresh, 5 * 60 * 1000);
}

init();
