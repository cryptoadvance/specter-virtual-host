const byId = id => document.getElementById(id);
let state;
let busy = false;
const notifiedRequests = new Set();

function backend() {
  const api = window.go?.main?.Application;
  if (!api) throw new Error('Desktop bridge is not ready yet.');
  return api;
}

async function execute(command, args = {}) {
  try {
    busy = true;
    const result = await backend().Execute(command, args);
    if (command !== 'status') hideError();
    return result;
  } catch (error) {
    showError(error?.message || String(error));
    throw error;
  } finally {
    busy = false;
  }
}

async function refresh() {
  if (busy) return;
  try {
    state = await execute('status');
    render(state);
  } catch (_) { /* visible error is handled by execute */ }
}

function render(snapshot) {
  const running = snapshot.bridge.running;
  const trusted = snapshot.settings.originPolicy === 'trusted';
  const bridgeError = !running ? snapshot.bridge.error : '';
  const browserConnected = snapshot.bridge.browserConnected;
  const browserRecoveryRequired = snapshot.bridge.browserRecoveryRequired;
  byId('hero-status').classList.toggle('stopped', !running);
  byId('bridge-title').textContent = running ? 'Bridge Running' : 'Bridge Stopped';
  byId('bridge-subtitle').textContent = running
    ? browserRecoveryRequired
      ? 'An interrupted wallet request requires reloading the simulator tab'
      : browserConnected
      ? 'Local bridge is active and the simulator is connected'
      : 'Bridge is active; waiting for a simulator connection'
    : bridgeError
      ? 'Free the endpoint shown below, then select Start Bridge to retry.'
      : 'Start the bridge to accept simulator connections';
  byId('operational-label').textContent = !running
    ? 'Bridge is offline'
    : browserRecoveryRequired
      ? 'Simulator recovery required'
    : browserConnected
      ? 'All systems operational'
      : 'Waiting for simulator';
  if (bridgeError) showError(bridgeError);
  byId('usb-status').textContent = running ? 'Active' : 'Inactive';
  byId('usb-dot').classList.toggle('active', running);
  byId('website-value').textContent = snapshot.bridge.browserOrigin || 'None';
  byId('website-caption').textContent = browserRecoveryRequired
    ? 'Reload this simulator tab to safely resume'
    : trusted ? 'Approved simulator origin' : 'Compatible simulator origin';
  byId('wallet-value').textContent = !snapshot.bridge.walletAllowed
    ? 'Connections blocked'
    : browserRecoveryRequired
      ? 'Reload simulator tab to recover'
    : !browserConnected
      ? 'Simulator not connected'
      : snapshot.bridge.walletConnected
        ? 'Wallet app connected'
        : 'Ready for a wallet request';

  const bridgeButton = byId('bridge-toggle');
  bridgeButton.classList.toggle('primary-danger', running);
  bridgeButton.classList.toggle('primary-start', !running);
  bridgeButton.querySelector('use').setAttribute('href', running ? '#icon-stop' : '#icon-play');
  bridgeButton.querySelector('span').textContent = running ? 'Stop Bridge' : 'Start Bridge';
  byId('wallet-toggle').querySelector('span').textContent = snapshot.bridge.walletAllowed ? 'Disconnect Wallet App' : 'Allow Wallet App';
  const openSimulatorButton = byId('open-simulator');
  openSimulatorButton.disabled = !running || snapshot.bridge.browserConnected;
  openSimulatorButton.title = snapshot.bridge.browserConnected
    ? browserRecoveryRequired
      ? 'Reload the connected simulator tab to safely recover the interrupted request.'
      : 'A simulator is already connected. Use its existing tab to preserve its wallet session.'
    : '';
  openSimulatorButton.querySelector('span').textContent = snapshot.bridge.browserConnected
    ? browserRecoveryRequired ? 'Simulator Reload Required' : 'Simulator Already Connected'
    : 'Open Simulator';

  byId('trusted-only').checked = trusted;
  byId('notify-sites').checked = snapshot.settings.notifyNewSite;
  byId('notify-sites').disabled = !trusted;
  byId('notify-row').classList.toggle('disabled', !trusted);
  byId('trusted-panel').hidden = !trusted;
  byId('trusted-panel').setAttribute('aria-hidden', String(!trusted));
  renderSites(snapshot.settings.trustedSites || []);
  renderRequests(snapshot.requests || [], trusted);
  renderActivity(snapshot.activity || [], 'activity-list', 5);
  renderActivity(snapshot.activity || [], 'full-activity-list', Infinity);
  updateLogCount(snapshot.activity || []);
}

function renderRequests(requests, trusted) {
  const panel = byId('request-panel');
  const container = byId('request-list');
  panel.hidden = !trusted || requests.length === 0;
  container.replaceChildren();
  for (const request of requests) {
    if (trusted && !notifiedRequests.has(request.id)) {
      notifiedRequests.add(request.id);
      backend().NotifyConnectionRequest(request).catch(() => {});
    }
    const card = document.createElement('div');
    card.className = 'request-card';
    const origin = document.createElement('strong');
    origin.textContent = request.origin;
    const expiry = document.createElement('small');
    expiry.textContent = `Expires ${new Date(request.expiresAt).toLocaleTimeString()}`;
    const actions = document.createElement('div');
    actions.className = 'request-actions';
    for (const [label, command, style] of [
      ['Allow Once', 'requests.allow-once', ''],
      ['Allow Permanently', 'requests.trust', 'primary'],
      ['Deny', 'requests.deny', 'quiet'],
    ]) {
      const button = document.createElement('button');
      button.className = style;
      button.textContent = label;
      button.onclick = async () => { await execute(command, {id: request.id}); await refresh(); };
      actions.append(button);
    }
    card.append(origin, expiry, actions);
    container.append(card);
  }
}

function renderSites(sites) {
  const container = byId('site-list');
  container.replaceChildren();
  if (!sites.length) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = 'No trusted websites configured.';
    container.append(empty);
    return;
  }
  for (const site of sites) {
    const row = document.createElement('div');
    row.className = 'site-row';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox'; checkbox.className = 'site-check'; checkbox.checked = site.enabled;
    checkbox.setAttribute('aria-label', `Enable ${site.origin}`);
    checkbox.onchange = async () => {
      try {
        const sites = await execute(`sites.${checkbox.checked ? 'enable' : 'disable'}`, {origin: site.origin});
        applyTrustedSites(sites);
        await refresh();
      } catch (_) { await refresh(); }
    };
    const origin = document.createElement('span'); origin.className = 'site-origin'; origin.textContent = site.origin; origin.title = site.origin;
    const badge = document.createElement('span'); badge.className = 'badge'; badge.textContent = site.builtIn ? 'Default' : 'Custom';
    row.append(checkbox, origin, badge);
    if (!site.builtIn) {
      const remove = document.createElement('button');
      remove.className = 'remove-site';
      remove.setAttribute('aria-label', `Remove ${site.origin}`);
      remove.title = 'Remove site';
      remove.innerHTML = '<svg class="icon"><use href="#icon-trash"/></svg>';
      remove.onclick = async () => {
        try {
          const sites = await execute('sites.remove', {origin: site.origin});
          applyTrustedSites(sites);
          await refresh();
        } catch (_) { await refresh(); }
      };
      row.append(remove);
    }
    container.append(row);
  }
}

function applyTrustedSites(sites) {
  if (!Array.isArray(sites)) return;
  if (state?.settings) state.settings.trustedSites = sites;
  renderSites(sites);
}

function renderActivity(entries, targetId = 'activity-list', limit = 5) {
  const container = byId(targetId);
  const shown = entries.slice(0, limit);
  const signature = shown.map(entry => entry.id || `${entry.time}:${entry.kind}:${entry.message}`).join('|');
  if (container.dataset.signature === signature) return;
  const scrollTop = container.scrollTop;
  container.replaceChildren();
  container.dataset.signature = signature;
  if (!shown.length) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = 'No activity yet.';
    container.append(empty);
    return;
  }
  const detailed = targetId === 'full-activity-list';
  for (const entry of shown) {
    const row = document.createElement('div');
    row.className = detailed ? 'activity-entry full-entry' : 'activity-entry';
    const date = new Date(entry.time);
    const time = document.createElement('span');
    time.className = 'activity-time';
    time.textContent = Number.isNaN(date.getTime()) ? String(entry.time || '') : (detailed ? date.toLocaleString() : date.toLocaleTimeString());
    const result = document.createElement('span');
    result.className = `activity-result ${entry.result || ''}`;
    result.textContent = entry.result || '';
    if (detailed) {
      const content = document.createElement('div');
      content.className = 'activity-content';
      const origin = document.createElement('strong');
      origin.className = 'activity-origin';
      origin.textContent = entry.origin || entry.kind || 'Host';
      const message = document.createElement('span');
      message.className = 'activity-message';
      message.textContent = entry.message || '';
      content.append(origin, message);
      row.append(time, content, result);
    } else {
      const message = document.createElement('span');
      message.textContent = entry.origin ? `${entry.origin} — ${entry.message}` : entry.message;
      row.append(time, message, result);
    }
    container.append(row);
  }
  container.scrollTop = scrollTop;
}

function updateLogCount(entries) {
  const count = entries.length;
  const label = count === 1 ? '1 activity entry' : `${count} activity entries`;
  byId('log-count').textContent = `${label} · newest first · up to 500 recent entries are retained`;
}

function escapeMarkdown(value) {
  return String(value ?? '').replace(/([\\`*_{}\[\]()#+.!|>~-])/g, '\\$1').replace(/\r?\n/g, ' ');
}

function formatActivityMarkdown(entries) {
  const exportedAt = new Date().toISOString().replace('T', ' ').replace(/\.\d{3}Z$/, ' UTC');
  const lines = [
    '# Specter Virtual Host — Activity Log',
    '',
    `Exported: ${exportedAt}`,
    `Entries: ${entries.length}`,
    '',
  ];
  if (!entries.length) lines.push('No activity recorded.', '');
  for (const entry of entries) {
    const date = new Date(entry.time);
    const timestamp = Number.isNaN(date.getTime()) ? String(entry.time || 'Unknown time') : date.toISOString();
    lines.push(
      '---',
      '',
      `## ${escapeMarkdown(timestamp)}`,
      '',
      `- **Type:** ${escapeMarkdown(entry.kind || 'activity')}`,
      ...(entry.origin ? [`- **Origin:** ${escapeMarkdown(entry.origin)}`] : []),
      `- **Event:** ${escapeMarkdown(entry.message || '')}`,
      ...(entry.result ? [`- **Result:** ${escapeMarkdown(entry.result)}`] : []),
      '',
    );
  }
  return lines.join('\n');
}

async function downloadActivityLog() {
  const result = await execute('log.show');
  const entries = Array.isArray(result) ? result : [];
  if (state) state.activity = entries;
  renderActivity(entries, 'full-activity-list', Infinity);
  updateLogCount(entries);
  const blob = new Blob([formatActivityMarkdown(entries)], {type: 'text/markdown;charset=utf-8'});
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = `specter-virtual-host-log-${new Date().toISOString().slice(0, 10)}.md`;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function showError(message) { const banner = byId('error-banner'); banner.textContent = message; banner.hidden = false; }
function hideError() { byId('error-banner').hidden = true; }
function showLogError(message) { const banner = byId('log-error'); banner.textContent = message; banner.hidden = false; }
function hideLogError() { byId('log-error').hidden = true; }

byId('open-repository').onclick = () => backend().OpenExternal('repository');
byId('bridge-toggle').onclick = async () => { await execute(state.bridge.running ? 'bridge.stop' : 'bridge.start'); await refresh(); };
byId('wallet-toggle').onclick = async () => { await execute(state.bridge.walletAllowed ? 'wallet.disconnect' : 'wallet.allow'); await refresh(); };
byId('open-simulator').onclick = () => execute('simulator.open');
byId('view-log').onclick = async () => {
  byId('app-shell').classList.add('log-mode');
  byId('log-view').hidden = false;
  byId('log-view').setAttribute('aria-hidden', 'false');
  try {
    const result = await execute('log.show');
    const entries = Array.isArray(result) ? result : [];
    if (state) state.activity = entries;
    renderActivity(entries, 'full-activity-list', Infinity);
    updateLogCount(entries);
    hideLogError();
  } catch (error) { showLogError(error?.message || String(error)); }
  byId('back-to-host').focus();
};
byId('back-to-host').onclick = () => {
  byId('log-view').hidden = true;
  byId('log-view').setAttribute('aria-hidden', 'true');
  byId('app-shell').classList.remove('log-mode');
  byId('view-log').focus();
};
byId('download-log').onclick = async () => {
  try { await downloadActivityLog(); hideLogError(); }
  catch (error) { showLogError(error?.message || String(error)); }
};
const clearActivityLog = async () => { await execute('log.clear'); await refresh(); };
byId('clear-log').onclick = clearActivityLog;
byId('clear-log-full').onclick = clearActivityLog;
byId('trusted-only').onchange = async event => {
  const enabled = event.currentTarget.checked;
  const panel = byId('trusted-panel');
  panel.hidden = !enabled;
  panel.setAttribute('aria-hidden', String(!enabled));
  try {
    await execute('settings.set', {key: 'origin-policy', value: enabled ? 'trusted' : 'open'});
    await refresh();
  } catch (_) {
    await refresh();
  }
  if (enabled && state?.settings?.originPolicy === 'trusted') {
    requestAnimationFrame(() => panel.scrollIntoView({behavior: 'smooth', block: 'nearest'}));
  }
};
byId('notify-sites').onchange = async event => { await execute('settings.set', {key: 'notify-new-site', value: String(event.target.checked)}); await refresh(); };
const addSiteDialog = byId('add-site-dialog');
const addSiteForm = byId('add-site-form');
const siteOriginInput = byId('site-origin-input');
const siteOriginError = byId('site-origin-error');

function setSiteOriginError(message) {
  siteOriginError.textContent = message;
  siteOriginError.hidden = !message;
  siteOriginInput.setAttribute('aria-invalid', String(Boolean(message)));
}

function normalizeEnteredOrigin(value) {
  const entered = value.trim();
  if (!entered) throw new Error('Enter a website address.');
  if (/^[a-z][a-z0-9+.-]*:/i.test(entered) && !/^https?:\/\//i.test(entered)) {
    throw new Error('Use a website address starting with http:// or https://.');
  }
  const address = /^https?:\/\//i.test(entered) ? entered : `https://${entered}`;
  const parsed = new URL(address);
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('Only http:// and https:// websites can be trusted.');
  }
  if (parsed.username || parsed.password || !parsed.hostname) {
    throw new Error('Enter a valid website address without account credentials.');
  }
  return parsed.origin;
}

byId('add-site').onclick = () => {
  addSiteForm.reset();
  setSiteOriginError('');
  addSiteDialog.showModal();
  requestAnimationFrame(() => siteOriginInput.focus());
};
byId('close-add-site').onclick = () => addSiteDialog.close();
byId('cancel-add-site').onclick = () => addSiteDialog.close();
addSiteDialog.addEventListener('close', () => {
  setSiteOriginError('');
  byId('submit-add-site').disabled = false;
});
addSiteForm.onsubmit = async event => {
  event.preventDefault();
  setSiteOriginError('');
  let origin;
  try { origin = normalizeEnteredOrigin(siteOriginInput.value); }
  catch (error) { setSiteOriginError(error.message || String(error)); siteOriginInput.focus(); return; }

  const submit = byId('submit-add-site');
  submit.disabled = true;
  try {
    const sites = await execute('sites.add', {origin});
    applyTrustedSites(sites);
    addSiteDialog.close();
    await refresh();
  } catch (error) {
    setSiteOriginError(error?.message || String(error));
    hideError();
    submit.disabled = false;
    siteOriginInput.focus();
  }
};

addEventListener('DOMContentLoaded', () => { refresh(); setInterval(refresh, 1000); });
