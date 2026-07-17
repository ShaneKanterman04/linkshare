const ownerName = document.querySelector('meta[name="owner-name"]').content || 'Me';
const state = { target: 'owner', loading: false };

const linksNode = document.querySelector('#links');
const noticeNode = document.querySelector('#notice');
const inboxHeading = document.querySelector('#inbox-heading');
const form = document.querySelector('#link-form');

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const status = document.querySelector('#form-status');
  const button = form.querySelector('button[type="submit"]');
  status.textContent = 'Sharing…';
  button.disabled = true;
  try {
    const created = await api('/api/v1/links', {
      method: 'POST',
      body: JSON.stringify({
        url: document.querySelector('#url').value,
        title: document.querySelector('#title').value,
        note: document.querySelector('#note').value,
        target: document.querySelector('#target').value,
        submitted_by: ownerName,
      }),
    });
    form.reset();
    document.querySelector('#target').value = 'agents';
    document.querySelector('#details').open = false;
    status.textContent = `Shared · expires ${formatExpiry(created.expires_at)}`;
    await refresh();
    setTimeout(() => { status.textContent = ''; }, 3000);
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
  inboxHeading.textContent = state.target === 'owner' ? 'Waiting for you' : 'Waiting for agents';
  refresh();
}));

document.querySelector('#refresh').addEventListener('click', refresh);

async function refresh() {
  if (state.loading) return;
  state.loading = true;
  document.querySelector('#refresh').classList.add('spinning');
  try {
    const [result, owner, agents] = await Promise.all([
      api(`/api/v1/links?target=${state.target}&limit=200`),
      api('/api/v1/links?target=owner&limit=1'),
      api('/api/v1/links?target=agents&limit=1'),
    ]);
    document.querySelector('#owner-count').textContent = owner.total;
    document.querySelector('#agents-count').textContent = agents.total;
    renderLinks(result.items);
    noticeNode.hidden = true;
  } catch (error) {
    showError(error.message);
  } finally {
    state.loading = false;
    document.querySelector('#refresh').classList.remove('spinning');
  }
}

function renderLinks(items) {
  linksNode.replaceChildren();
  if (!items.length) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = 'Nothing waiting.';
    linksNode.append(empty);
    return;
  }
  items.forEach((item) => linksNode.append(linkRow(item)));
}

function linkRow(item) {
  const row = document.createElement('article');
  row.className = 'link-row';
  const parsed = new URL(item.url);

  const content = document.createElement('div');
  content.className = 'link-content';
  const title = document.createElement('a');
  title.className = 'link-title';
  title.href = item.url;
  title.target = '_blank';
  title.rel = 'noopener noreferrer';
  title.textContent = item.title || parsed.hostname;
  title.addEventListener('click', () => consume(item.id, row));
  content.append(title);

  if (item.note) {
    const note = document.createElement('p');
    note.className = 'link-note';
    note.textContent = item.note;
    content.append(note);
  }

  const meta = document.createElement('p');
  meta.className = 'link-meta';
  const expiry = document.createElement('span');
  expiry.textContent = formatExpiry(item.expires_at);
  expiry.title = new Date(item.expires_at).toLocaleString();
  meta.append(`${parsed.hostname} · from ${item.submitted_by} · `, expiry);
  content.append(meta);

  const actions = document.createElement('div');
  actions.className = 'row-actions';
  const open = document.createElement('a');
  open.className = 'open-link';
  open.href = item.url;
  open.target = '_blank';
  open.rel = 'noopener noreferrer';
  open.setAttribute('aria-label', `Open ${title.textContent}`);
  open.title = 'Open and clear';
  open.textContent = '↗';
  open.addEventListener('click', () => consume(item.id, row));
  const dismiss = document.createElement('button');
  dismiss.className = 'dismiss';
  dismiss.type = 'button';
  dismiss.setAttribute('aria-label', `Dismiss ${title.textContent}`);
  dismiss.title = 'Dismiss';
  dismiss.textContent = '×';
  dismiss.addEventListener('click', () => consume(item.id, row));
  actions.append(open, dismiss);

  row.append(content, actions);
  return row;
}

async function consume(id, row) {
  row.classList.add('leaving');
  try {
    await api(`/api/v1/links/${id}`, { method: 'DELETE' });
    row.remove();
    await refresh();
  } catch (error) {
    row.classList.remove('leaving');
    showError(error.message);
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
  });
  if (response.status === 204) return null;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error?.message || `Request failed (${response.status})`);
  return data;
}

function formatExpiry(value) {
  const milliseconds = new Date(value).getTime() - Date.now();
  const hours = Math.max(0, Math.ceil(milliseconds / 3600000));
  if (hours < 24) return `expires in ${hours}h`;
  return `expires in ${Math.ceil(hours / 24)}d`;
}

function showError(message) {
  noticeNode.textContent = message;
  noticeNode.hidden = false;
}

refresh();
setInterval(refresh, 30000);
