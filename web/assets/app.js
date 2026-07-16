const ownerName = document.querySelector('meta[name="owner-name"]').content || 'Me';
const state = { target: 'owner', filter: 'active', loading: false };

const linksNode = document.querySelector('#links');
const noticeNode = document.querySelector('#notice');
const inboxHeading = document.querySelector('#inbox-heading');

document.querySelector('#link-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const status = document.querySelector('#form-status');
  const button = event.currentTarget.querySelector('button[type="submit"]');
  status.textContent = 'Sharing…';
  button.disabled = true;
  try {
    const response = await api('/api/v1/links', {
      method: 'POST',
      body: JSON.stringify({
        url: document.querySelector('#url').value,
        title: document.querySelector('#title').value,
        note: document.querySelector('#note').value,
        target: document.querySelector('#target').value,
        submitted_by: ownerName,
      }),
    });
    event.currentTarget.reset();
    document.querySelector('#target').value = 'agents';
    status.textContent = `Shared #${response.id}`;
    await refresh();
    setTimeout(() => { status.textContent = ''; }, 2500);
  } catch (error) {
    status.textContent = error.message;
  } finally {
    button.disabled = false;
  }
});

document.querySelectorAll('.tab').forEach((button) => button.addEventListener('click', () => {
  state.target = button.dataset.target;
  document.querySelectorAll('.tab').forEach((item) => {
    const selected = item === button;
    item.classList.toggle('active', selected);
    item.setAttribute('aria-selected', selected);
  });
  inboxHeading.textContent = state.target === 'owner' ? 'Links waiting for you' : 'Links waiting for agents';
  refresh();
}));

document.querySelectorAll('.filter').forEach((button) => button.addEventListener('click', () => {
  state.filter = button.dataset.state;
  document.querySelectorAll('.filter').forEach((item) => item.classList.toggle('active', item === button));
  refresh();
}));

document.querySelector('#refresh').addEventListener('click', refresh);

async function refresh() {
  if (state.loading) return;
  state.loading = true;
  document.querySelector('#refresh').classList.add('spinning');
  try {
    const [result, ownerUnread, agentsUnread] = await Promise.all([
      api(`/api/v1/links?target=${state.target}&state=${state.filter}&limit=200`),
      api('/api/v1/links?target=owner&state=unread&limit=1'),
      api('/api/v1/links?target=agents&state=unread&limit=1'),
    ]);
    document.querySelector('#owner-count').textContent = ownerUnread.total;
    document.querySelector('#agents-count').textContent = agentsUnread.total;
    renderLinks(result.items);
    noticeNode.hidden = true;
  } catch (error) {
    noticeNode.textContent = error.message;
    noticeNode.hidden = false;
  } finally {
    state.loading = false;
    document.querySelector('#refresh').classList.remove('spinning');
  }
}

function renderLinks(items) {
  linksNode.replaceChildren();
  if (!items.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.innerHTML = '<span>✓</span><h3>Nothing here</h3><p>This inbox is clear.</p>';
    linksNode.append(empty);
    return;
  }
  items.forEach((item) => linksNode.append(linkCard(item)));
}

function linkCard(item) {
  const card = document.createElement('article');
  card.className = `link-card${item.read_at ? ' read' : ''}`;
  const parsed = new URL(item.url);
  const archived = Boolean(item.archived_at);

  const header = document.createElement('div');
  header.className = 'link-card-header';
  const text = document.createElement('div');
  const title = document.createElement('a');
  title.className = 'link-title';
  title.href = item.url;
  title.target = '_blank';
  title.rel = 'noopener noreferrer';
  title.textContent = item.title || parsed.hostname;
  title.addEventListener('click', () => {
    if (!item.read_at && !archived) perform(item.id, 'mark_read', false);
  });
  const host = document.createElement('p');
  host.className = 'link-host';
  host.textContent = parsed.hostname;
  text.append(title, host);
  const badge = document.createElement('span');
  badge.className = archived ? 'badge archived' : item.read_at ? 'badge read-badge' : 'badge unread';
  badge.textContent = archived ? 'Archived' : item.read_at ? 'Read' : 'Unread';
  header.append(text, badge);
  card.append(header);

  if (item.note) {
    const note = document.createElement('p');
    note.className = 'link-note';
    note.textContent = item.note;
    card.append(note);
  }

  const meta = document.createElement('p');
  meta.className = 'link-meta';
  meta.textContent = `From ${item.submitted_by} · ${formatTime(item.created_at)}`;
  if (item.read_by) meta.textContent += ` · Read by ${item.read_by}`;
  card.append(meta);

  const actions = document.createElement('div');
  actions.className = 'card-actions';
  actions.append(actionLink('Open', item.url), actionButton('Copy', () => copyText(item.url)));
  if (archived) {
    actions.append(actionButton('Restore', () => perform(item.id, 'restore')));
  } else {
    actions.append(actionButton(item.read_at ? 'Mark unread' : 'Mark read', () => perform(item.id, item.read_at ? 'mark_unread' : 'mark_read')));
    actions.append(actionButton('Archive', () => perform(item.id, 'archive')));
  }
  card.append(actions);
  return card;
}

function actionButton(label, handler) {
  const button = document.createElement('button');
  button.className = 'text-button';
  button.type = 'button';
  button.textContent = label;
  button.addEventListener('click', handler);
  return button;
}

function actionLink(label, href) {
  const link = document.createElement('a');
  link.className = 'text-button';
  link.href = href;
  link.target = '_blank';
  link.rel = 'noopener noreferrer';
  link.textContent = label;
  return link;
}

async function perform(id, action, refreshAfter = true) {
  try {
    await api(`/api/v1/links/${id}`, { method: 'PATCH', body: JSON.stringify({ action, actor: ownerName }) });
    if (refreshAfter) await refresh();
  } catch (error) {
    noticeNode.textContent = error.message;
    noticeNode.hidden = false;
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error?.message || `Request failed (${response.status})`);
  return data;
}

function formatTime(value) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

async function copyText(value) {
  try { await navigator.clipboard.writeText(value); } catch (_) { /* Clipboard can be blocked on plain HTTP. */ }
}

refresh();
setInterval(refresh, 15000);
