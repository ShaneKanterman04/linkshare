const base = window.location.origin;
const instruction = `Use Linkshare at ${base} to exchange links with me.

- To discover all available operations, GET /api/v1.
- To send me a link, POST JSON to /api/v1/links with target "owner", a useful free-form submitted_by name, url, and optional title/note.
- To get links I left for agents, GET /api/v1/links?target=agents&state=unread.
- After consuming one, PATCH /api/v1/links/{id} with action "mark_read" and your actor name.
- Do not archive links unless I ask you to.
- The service is trusted-LAN only and has no authentication.`;

document.querySelector('#agent-instruction').textContent = instruction;
document.querySelector('#discover-example').textContent = `curl '${base}/api/v1'`;
document.querySelector('#post-example').textContent = `curl -X POST ${base}/api/v1/links \\
  -H 'Content-Type: application/json' \\
  -d '{"url":"https://example.com","title":"Useful reference","note":"Why it matters","target":"owner","submitted_by":"codex-agent"}'`;
document.querySelector('#get-example').textContent = `curl '${base}/api/v1/links?target=agents&state=unread'`;
document.querySelector('#patch-example').textContent = `curl -X PATCH ${base}/api/v1/links/42 \\
  -H 'Content-Type: application/json' \\
  -d '{"action":"mark_read","actor":"codex-agent"}'`;

document.querySelectorAll('[data-copy]').forEach((button) => button.addEventListener('click', async () => {
  const source = document.querySelector(`#${button.dataset.copy}`);
  await navigator.clipboard.writeText(source.textContent);
  button.textContent = 'Copied';
  setTimeout(() => { button.textContent = 'Copy'; }, 1600);
}));
