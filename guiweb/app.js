"use strict";

const $ = (selector) => document.querySelector(selector);

const elements = {
  connectionState: $("#connectionState"),
  refreshButton: $("#refreshButton"),
  startButton: $("#startButton"),
  stopButton: $("#stopButton"),
  saveButton: $("#saveButton"),
  saveDiscoveryButton: $("#saveDiscoveryButton"),
  fetchButton: $("#fetchButton"),
  discoverButton: $("#discoverButton"),
  reloadProxiesButton: $("#reloadProxiesButton"),
  clearLogsButton: $("#clearLogsButton"),
  settingsForm: $("#settingsForm"),
  discoveryForm: $("#discoveryForm"),
  listenInput: $("#listenInput"),
  strategyInput: $("#strategyInput"),
  checkInput: $("#checkInput"),
  checkIntervalInput: $("#checkIntervalInput"),
  fofaQueryInput: $("#fofaQueryInput"),
  discoveryConfigPathTooltip: $("#discoveryConfigPathTooltip"),
  fofaEndpointInput: $("#fofaEndpointInput"),
  fofaKeyInput: $("#fofaKeyInput"),
  toggleFofaKeyButton: $("#toggleFofaKeyButton"),
  fofaResultLimitInput: $("#fofaResultLimitInput"),
  hunterQueryInput: $("#hunterQueryInput"),
  hunterEndpointInput: $("#hunterEndpointInput"),
  hunterKeyInput: $("#hunterKeyInput"),
  toggleHunterKeyButton: $("#toggleHunterKeyButton"),
  hunterResultLimitInput: $("#hunterResultLimitInput"),
  providerTabs: [...document.querySelectorAll(".provider-tab")],
  providerFields: [...document.querySelectorAll(".provider-fields")],
  sourcesInput: $("#sourcesInput"),
  sourceTimeoutInput: $("#sourceTimeoutInput"),
  sourceConcurrencyInput: $("#sourceConcurrencyInput"),
  proxySearch: $("#proxySearch"),
  proxyPageSize: $("#proxyPageSize"),
  proxyPrevPage: $("#proxyPrevPage"),
  proxyNextPage: $("#proxyNextPage"),
  proxyPageInfo: $("#proxyPageInfo"),
  runningStatus: $("#runningStatus"),
  uptime: $("#uptime"),
  listenSummary: $("#listenSummary"),
  currentProxySummary: $("#currentProxySummary"),
  currentProxyBlacklistButton: $("#currentProxyBlacklistButton"),
  proxyCount: $("#proxyCount"),
  healthyCount: $("#healthyCount"),
  healthyRate: $("#healthyRate"),
  discoveryKeyState: $("#discoveryKeyState"),
  discoveryTargetCount: $("#discoveryTargetCount"),
  discoveryTargets: $("#discoveryTargets"),
  discoveredSourceCount: $("#discoveredSourceCount"),
  discoveredSources: $("#discoveredSources"),
  proxyTableBody: $("#proxyTableBody"),
  proxyTableMeta: $("#proxyTableMeta"),
  fetchResult: $("#fetchResult"),
  logOutput: $("#logOutput"),
  toastRegion: $("#toastRegion"),
  blacklistConfirmDialog: $("#blacklistConfirmDialog"),
  blacklistConfirmMessage: $("#blacklistConfirmMessage"),
  blacklistConfirmButton: $("#blacklistConfirmButton"),
  blacklistCancelButton: $("#blacklistCancelButton"),
};

const state = {
  status: {},
  proxies: [],
  discovery: {
    activeProvider: "fofa",
    providers: {},
    targets: [],
    targetCount: 0,
  },
  sourceStatuses: [],
  runtime: {
    current: "",
    forwarders: [],
  },
  proxyPage: 1,
  proxyPageSize: 10,
  proxySort: {
    key: "",
    direction: "",
  },
  settingsLoaded: false,
  logs: [],
  logsClearedAnchor: "",
  pendingBlacklist: "",
  pendingBlacklistButton: null,
  polling: null,
};

async function request(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(path, {
    ...options,
    headers,
    cache: "no-store",
  });

  const contentType = response.headers.get("content-type") || "";
  let payload;
  if (response.status === 204) {
    payload = {};
  } else if (contentType.includes("application/json")) {
    payload = await response.json();
  } else {
    payload = await response.text();
  }

  if (!response.ok) {
    const message = typeof payload === "string"
      ? payload
      : payload?.error || payload?.message || `请求失败（HTTP ${response.status}）`;
    const error = new Error(message);
    error.payload = payload;
    throw error;
  }

  return payload;
}

function setConnection(online, text) {
  elements.connectionState.classList.toggle("online", online);
  elements.connectionState.classList.toggle("offline", !online);
  elements.connectionState.querySelector("span").textContent = text;
}

function setButtonLoading(button, loading, loadingText) {
  if (!button.dataset.label) {
    button.dataset.label = button.querySelector("span")?.textContent || button.textContent;
  }
  button.disabled = loading;
  button.classList.toggle("loading", loading);
  button.setAttribute("aria-busy", loading ? "true" : "false");
  const label = button.querySelector("span");
  if (label) {
    label.textContent = loading ? loadingText : button.dataset.label;
  }
}

function showToast(message, type = "info", duration = 3800) {
  const toast = document.createElement("div");
  toast.className = `toast ${type}`;

  const dot = document.createElement("span");
  dot.className = "toast-dot";

  const text = document.createElement("p");
  text.textContent = message;

  const close = document.createElement("button");
  close.type = "button";
  close.setAttribute("aria-label", "关闭通知");
  close.textContent = "×";
  close.addEventListener("click", () => toast.remove());

  toast.append(dot, text, close);
  elements.toastRegion.append(toast);
  window.setTimeout(() => toast.remove(), duration);
}

function normaliseBoolean(value) {
  if (typeof value === "string") {
    return ["true", "ok", "healthy", "online", "running", "validated", "success"].includes(value.toLowerCase());
  }
  return Boolean(value);
}

function formatUptime(status) {
  const rawSeconds = Number(status.uptimeSeconds ?? status.uptime ?? 0);
  let seconds = Number.isFinite(rawSeconds) ? rawSeconds : 0;

  const startTime = status.startedAt ?? status.startTime;
  if (!seconds && startTime) {
    const startedAt = new Date(startTime).getTime();
    if (Number.isFinite(startedAt)) {
      seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000));
    }
  }

  if (!seconds) {
    return "刚刚启动";
  }

  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days) return `已运行 ${days} 天 ${hours} 小时`;
  if (hours) return `已运行 ${hours} 小时 ${minutes} 分`;
  return `已运行 ${minutes} 分钟`;
}

function updateStatusView(status) {
  state.status = status || {};
  state.runtime = status?.runtime && typeof status.runtime === "object"
    ? status.runtime
    : { current: "", forwarders: [] };
  if (Array.isArray(status?.sourceStatuses)) {
    state.sourceStatuses = status.sourceStatuses;
    renderDiscoveredSources(state.discovery.targets);
  }
  const running = normaliseBoolean(status?.running ?? status?.active ?? status?.status);
  const statusCard = elements.runningStatus.closest(".metric-card");

  elements.runningStatus.textContent = running ? "运行中" : "已停止";
  elements.uptime.textContent = running
    ? formatUptime(status)
    : status?.exitError || "服务当前未运行";
  statusCard.classList.toggle("running", running);
  statusCard.classList.toggle("stopped", !running);
  elements.startButton.disabled = running;
  elements.stopButton.disabled = !running;

  const listen = status?.listen || status?.listenAddress || elements.listenInput.value || "—";
  elements.listenSummary.textContent = listen;

  const currentProxy = String(state.runtime.current || "");
  const currentItem = currentProxyContext().item;
  elements.currentProxySummary.textContent = running && currentProxy ? currentProxy : "—";
  elements.currentProxySummary.classList.toggle("active", Boolean(running && currentProxy));
  elements.currentProxyBlacklistButton.disabled =
    !running || !currentProxy || normaliseBoolean(currentItem?.blacklisted);
  elements.currentProxyBlacklistButton.setAttribute(
    "aria-label",
    currentProxy ? `拉黑当前代理 ${currentProxy}` : "拉黑当前代理",
  );

  const total = Number(status?.proxyCount ?? status?.totalProxies ?? state.proxies.length ?? 0);
  const runtimeForwarders = Array.isArray(state.runtime.forwarders) ? state.runtime.forwarders : [];
  const healthy = running ? runtimeForwarders.filter((forwarder) => normaliseBoolean(forwarder.enabled)).length : 0;
  const safeTotal = Number.isFinite(total) ? total : 0;
  const safeHealthy = Number.isFinite(healthy) ? healthy : 0;

  elements.proxyCount.textContent = String(safeTotal);
  elements.healthyCount.textContent = String(safeHealthy);
  elements.healthyRate.textContent = running
    ? `glider 可用率 ${safeTotal ? Math.round((safeHealthy / safeTotal) * 100) : 0}%`
    : "glider 健康状态不可用";
  renderProxies();
}

function currentProxyContext() {
  const current = String(state.runtime.current || "");
  const runtimeByProxy = runtimeForwarderMap();
  const item = state.proxies.find((proxy) => {
    const runtime = runtimeByProxy.get(proxyRuntimeKey(proxy)) || runtimeByProxy.get(proxyAddress(proxy));
    return proxyMatchesCurrent(proxy, runtime, current);
  });
  return {
    current,
    item,
    identity: item ? proxyIdentity(item) : current,
  };
}

function getSettingsFromForm() {
  const protocol = document.querySelector('input[name="protocol"]:checked')?.value || "socks5";
  const sources = elements.sourcesInput.value
    .split(/\r?\n/)
    .map((source) => source.trim())
    .filter(Boolean);

  return {
    listen: elements.listenInput.value.trim(),
    strategy: elements.strategyInput.value,
    check: elements.checkInput.value.trim(),
    checkInterval: Number(elements.checkIntervalInput.value),
    fofaQuery: elements.fofaQueryInput.value.trim(),
    fofaResultLimit: Number(elements.fofaResultLimitInput.value),
    hunterQuery: elements.hunterQueryInput.value.trim(),
    hunterResultLimit: Number(elements.hunterResultLimitInput.value),
    sources,
    sourceTimeoutSeconds: Number(elements.sourceTimeoutInput.value),
    sourceConcurrency: Number(elements.sourceConcurrencyInput.value),
    protocol,
  };
}

function applySettings(settings) {
  if (!settings || typeof settings !== "object") return;
  elements.listenInput.value = settings.listen ?? settings.listenAddress ?? elements.listenInput.value;
  elements.strategyInput.value = settings.strategy ?? elements.strategyInput.value;
  elements.checkInput.value = settings.check ?? settings.checkURL ?? elements.checkInput.value;
  elements.checkIntervalInput.value = settings.checkInterval ?? settings.checkIntervalSeconds ?? elements.checkIntervalInput.value;
  elements.fofaQueryInput.value = settings.fofaQuery ?? elements.fofaQueryInput.value;
  elements.fofaResultLimitInput.value = settings.fofaResultLimit ?? elements.fofaResultLimitInput.value;
  elements.hunterQueryInput.value = settings.hunterQuery ?? elements.hunterQueryInput.value;
  elements.hunterResultLimitInput.value = settings.hunterResultLimit ?? elements.hunterResultLimitInput.value;
  elements.sourceTimeoutInput.value = settings.sourceTimeoutSeconds ?? elements.sourceTimeoutInput.value;
  elements.sourceConcurrencyInput.value = settings.sourceConcurrency ?? elements.sourceConcurrencyInput.value;

  const sources = settings.sources ?? settings.proxySources;
  if (Array.isArray(sources)) {
    elements.sourcesInput.value = sources.join("\n");
  } else if (typeof sources === "string") {
    elements.sourcesInput.value = sources;
  }

  const protocol = settings.protocol ?? settings.proxyType;
  const protocolInput = protocol && document.querySelector(`input[name="protocol"][value="${CSS.escape(protocol)}"]`);
  if (protocolInput) protocolInput.checked = true;
  state.settingsLoaded = true;
  renderDiscoveredSources(state.discovery.targets);
}

function proxyAddress(proxy) {
  if (proxy.address) return String(proxy.address).replace(/^[a-z0-9+.-]+:\/\//i, "");
  if (proxy.url) return proxy.url.replace(/^[a-z0-9+.-]+:\/\//i, "");
  if (proxy.ip && proxy.port !== undefined) return `${proxy.ip}:${proxy.port}`;
  return "—";
}

function proxyIdentity(proxy) {
  if (proxy.url && /^[a-z0-9+.-]+:\/\//i.test(proxy.url)) return String(proxy.url);
  if (proxy.address && /^[a-z0-9+.-]+:\/\//i.test(proxy.address)) return String(proxy.address);
  const protocol = String(proxy.protocol || proxy.type || "").toLowerCase();
  if (proxy.ip && proxy.port !== undefined) {
    const host = String(proxy.ip).includes(":") ? `[${proxy.ip}]` : proxy.ip;
    return protocol ? `${protocol}://${host}:${proxy.port}` : `${host}:${proxy.port}`;
  }
  const address = proxyAddress(proxy);
  return protocol && address !== "—" ? `${protocol}://${address}` : address;
}

function proxyRuntimeKey(proxy) {
  return proxyIdentity(proxy);
}

function proxyMatchesCurrent(proxy, runtime, current) {
  if (!current) return false;
  const protocol = String(proxy.protocol || proxy.type || runtime?.url?.split("://")[0] || "").toLowerCase();
  const canonicalCurrent = current.includes("://") || !protocol ? current : `${protocol}://${current}`;
  const candidates = [
    proxyIdentity(proxy),
    proxy.address,
    proxy.url,
    runtime?.url,
    runtime?.address,
  ].filter(Boolean).map(String);
  return candidates.includes(current) || candidates.includes(canonicalCurrent);
}

function runtimeForwarderMap() {
  const result = new Map();
  const forwarders = Array.isArray(state.runtime.forwarders) ? state.runtime.forwarders : [];
  for (const forwarder of forwarders) {
    if (forwarder?.url) result.set(String(forwarder.url), forwarder);
    if (forwarder?.address) result.set(String(forwarder.address), forwarder);
  }
  return result;
}

function proxyRuntimeStatus(runtime, running, current, blacklisted = false) {
  if (blacklisted) {
    return { label: "已拉黑", className: "blocked" };
  }
  if (!running) {
    return { label: "服务已停止", className: "pending" };
  }
  if (!runtime || !runtime.lastCheck) {
    return { label: "待首次检查", className: "pending" };
  }
  if (normaliseBoolean(runtime.enabled)) {
    return { label: current ? "可用 · 最近使用" : "可用", className: current ? "current" : "healthy" };
  }
  return { label: current ? "不可用 · 最近使用" : "不可用", className: "failed" };
}

const proxyCollator = new Intl.Collator("en", { numeric: true, sensitivity: "base" });

function proxySortValue(proxy, runtime, status) {
  switch (state.proxySort.key) {
    case "address":
      return proxyAddress(proxy);
    case "protocol":
      return proxy.protocol || proxy.type || "";
    case "source":
      return proxy.source || proxy.origin || "";
    case "status":
      return status.label;
    case "latency":
      return Number(runtime?.latencyMs ?? -1);
    case "failures":
      return Number(runtime?.failures ?? -1);
    case "lastCheck":
      return runtime?.lastCheck ? new Date(runtime.lastCheck).getTime() : 0;
    default:
      return "";
  }
}

function compareProxyRows(left, right) {
  const leftValue = left.sortValue;
  const rightValue = right.sortValue;
  let result;
  if (typeof leftValue === "number" && typeof rightValue === "number") {
    result = leftValue - rightValue;
  } else {
    result = proxyCollator.compare(String(leftValue), String(rightValue));
  }
  if (result === 0) result = left.index - right.index;
  return state.proxySort.direction === "desc" ? -result : result;
}

function updateSortHeaders() {
  for (const button of document.querySelectorAll(".sort-button")) {
    const active = button.dataset.sort === state.proxySort.key;
    const direction = active ? state.proxySort.direction : "";
    button.classList.toggle("active", active);
    button.querySelector("span").textContent = direction === "desc" ? "↓" : direction === "asc" ? "↑" : "";
    button.closest("th").setAttribute(
      "aria-sort",
      direction === "desc" ? "descending" : direction === "asc" ? "ascending" : "none",
    );
  }
}

function formatCheckTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleTimeString("zh-CN", { hour12: false });
}

function renderProxies() {
  const runtimeByProxy = runtimeForwarderMap();
  const running = normaliseBoolean(state.status?.running ?? state.status?.active ?? state.status?.status);
  const current = String(state.runtime.current || "");
  const query = elements.proxySearch.value.trim().toLowerCase();
  const rows = state.proxies
    .map((proxy, index) => {
      const runtime = runtimeByProxy.get(proxyRuntimeKey(proxy)) || runtimeByProxy.get(proxyAddress(proxy));
      const isCurrent = proxyMatchesCurrent(proxy, runtime, current);
      const status = proxyRuntimeStatus(runtime, running, isCurrent, normaliseBoolean(proxy.blacklisted));
      return { proxy, runtime, isCurrent, status, index };
    })
    .filter(({ proxy }) => {
      if (!query) return true;
      return [
        proxyAddress(proxy),
        proxy.protocol,
        proxy.type,
        proxy.source,
        proxy.origin,
        normaliseBoolean(proxy.blacklisted) ? "已拉黑" : "",
      ].some((value) => String(value || "").toLowerCase().includes(query));
    });
  if (state.proxySort.key && state.proxySort.direction) {
    for (const row of rows) {
      row.sortValue = proxySortValue(row.proxy, row.runtime, row.status);
    }
    rows.sort(compareProxyRows);
  }
  const totalPages = Math.max(1, Math.ceil(rows.length / state.proxyPageSize));
  state.proxyPage = Math.min(Math.max(1, state.proxyPage), totalPages);
  const start = (state.proxyPage - 1) * state.proxyPageSize;
  const pageRows = rows.slice(start, start + state.proxyPageSize);

  elements.proxyTableBody.replaceChildren();
  if (!rows.length) {
    const tr = document.createElement("tr");
    tr.className = "empty-row";
    const td = document.createElement("td");
    td.colSpan = 8;
    td.textContent = query ? "没有匹配的代理节点" : "代理池暂时为空";
    tr.append(td);
    elements.proxyTableBody.append(tr);
  } else {
    const fragment = document.createDocumentFragment();
    for (const row of pageRows) {
      const { proxy, runtime, isCurrent, status } = row;
      const tr = document.createElement("tr");
      tr.classList.toggle("current-proxy-row", isCurrent);
      tr.classList.toggle("blacklisted-proxy-row", normaliseBoolean(proxy.blacklisted));

      const address = document.createElement("td");
      const addressText = document.createElement("span");
      addressText.className = "proxy-address";
      addressText.textContent = proxyAddress(proxy);
      address.append(addressText);

      const protocol = document.createElement("td");
      const protocolBadge = document.createElement("span");
      protocolBadge.className = "protocol-badge";
      protocolBadge.textContent = proxy.protocol || proxy.type || "未知";
      protocol.append(protocolBadge);

      const source = document.createElement("td");
      source.textContent = proxy.source || proxy.origin || "手动配置";

      const runtimeState = document.createElement("td");
      const statusBadge = document.createElement("span");
      statusBadge.className = `status-badge ${status.className}`;
      statusBadge.textContent = status.label;
      statusBadge.title = runtime?.lastError || "";
      runtimeState.append(statusBadge);

      const latency = document.createElement("td");
      latency.textContent = runtime?.lastCheck && normaliseBoolean(runtime.enabled) && Number.isFinite(Number(runtime.latencyMs))
        ? `${Number(runtime.latencyMs)} ms`
        : "—";

      const failures = document.createElement("td");
      failures.textContent = runtime ? String(Number(runtime.failures || 0)) : "—";

      const lastCheck = document.createElement("td");
      lastCheck.textContent = formatCheckTime(runtime?.lastCheck);
      lastCheck.title = runtime?.lastError || "";

      const actions = document.createElement("td");
      const blacklistButton = document.createElement("button");
      blacklistButton.type = "button";
      blacklistButton.className = `text-button ${normaliseBoolean(proxy.blacklisted) ? "restore" : "blacklist"}`;
      blacklistButton.textContent = normaliseBoolean(proxy.blacklisted) ? "解除拉黑" : "拉黑";
      blacklistButton.addEventListener("click", () => setProxyBlacklisted(
        proxyRuntimeKey(proxy),
        !normaliseBoolean(proxy.blacklisted),
        blacklistButton,
      ));
      actions.append(blacklistButton);

      tr.append(address, protocol, source, runtimeState, latency, failures, lastCheck, actions);
      fragment.append(tr);
    }
    elements.proxyTableBody.append(fragment);
  }

  const firstVisible = rows.length ? start + 1 : 0;
  const lastVisible = Math.min(start + pageRows.length, rows.length);
  elements.proxyTableMeta.textContent = query
    ? `显示 ${firstVisible}-${lastVisible} / 筛选 ${rows.length} / 全部 ${state.proxies.length}`
    : `显示 ${firstVisible}-${lastVisible} / 共 ${state.proxies.length} 个节点`;
  elements.proxyPageInfo.textContent = `${state.proxyPage} / ${totalPages}`;
  elements.proxyPrevPage.disabled = state.proxyPage <= 1;
  elements.proxyNextPage.disabled = state.proxyPage >= totalPages;
  updateSortHeaders();
}

function normaliseLogs(payload) {
  let logs = payload?.logs ?? payload?.items ?? payload;
  if (typeof logs === "string") logs = logs.split(/\r?\n/).filter(Boolean);
  if (!Array.isArray(logs)) return [];

  return logs.map((entry) => {
    if (typeof entry === "string") {
      return { time: "", message: entry };
    }
    return {
      time: entry.time || entry.timestamp || "",
      message: entry.message || entry.msg || JSON.stringify(entry),
    };
  });
}

function targetURL(target) {
  if (target.url) return target.url;
  const host = target.host || target.ip;
  if (!host) return "—";
  const protocol = target.protocol || "http";
  return `${protocol}://${host}${target.port ? `:${target.port}` : ""}`;
}

function renderDiscoveredSources(targets) {
  const entries = new Map();
  const addSource = (source, origin) => {
    source = String(source || "").trim();
    if (!source || source === "—") return;
    const key = source.replace(/\/+$/, "");
    const entry = entries.get(key) || { source, origins: [] };
    if (!entry.origins.includes(origin)) entry.origins.push(origin);
    entries.set(key, entry);
  };
  for (const source of elements.sourcesInput.value.split(/\r?\n/)) {
    addSource(source, "手工");
  }
  for (const target of targets) {
    const providers = Array.isArray(target.providers) ? target.providers : [];
    if (!providers.length) {
      addSource(targetURL(target), "历史发现");
      continue;
    }
    for (const provider of providers) {
      addSource(targetURL(target), String(provider).toUpperCase());
    }
  }

  const statuses = new Map(
    state.sourceStatuses.map((status) => [
      String(status.source || "").trim().replace(/\/+$/, ""),
      status,
    ]),
  );
  elements.discoveredSourceCount.textContent = String(entries.size);
  elements.discoveredSources.replaceChildren();

  if (!entries.size) {
    const placeholder = document.createElement("div");
    placeholder.className = "synced-source-placeholder";
    placeholder.textContent = "填写手工来源或搜索目标后显示在这里";
    elements.discoveredSources.append(placeholder);
    return;
  }

  const fragment = document.createDocumentFragment();
  for (const [key, entry] of entries) {
    const status = statuses.get(key);
    const item = document.createElement("div");
    item.className = `synced-source-item ${status ? status.success ? "success" : "failed" : "pending"}`;
    item.title = status?.error || entry.source;

    const source = document.createElement("span");
    source.className = "synced-source-url";
    source.textContent = entry.source;

    const badges = document.createElement("span");
    badges.className = "synced-source-badges";
    for (const origin of entry.origins) {
      const originBadge = document.createElement("span");
      originBadge.className = "source-origin-badge";
      originBadge.textContent = origin;
      badges.append(originBadge);
    }
    const statusBadge = document.createElement("span");
    statusBadge.className = "source-result-badge";
    statusBadge.textContent = !status
      ? "未校验"
      : status.success
        ? `成功 · ${Number(status.proxyCount || 0)} 个`
        : "失败";
    badges.append(statusBadge);

    if (status && Number.isFinite(Number(status.durationMs))) {
      const duration = document.createElement("span");
      duration.className = "source-duration";
      duration.textContent = `${Number(status.durationMs)} ms`;
      badges.append(duration);
    }
    item.append(source, badges);
    fragment.append(item);
  }
  elements.discoveredSources.append(fragment);
}

function renderActiveDiscoveryProvider() {
  const name = state.discovery.activeProvider || "fofa";
  const provider = state.discovery.providers[name] || {};
  for (const tab of elements.providerTabs) {
    const active = tab.dataset.provider === name;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", String(active));
  }
  for (const fields of elements.providerFields) {
    const active = fields.dataset.providerFields === name;
    fields.classList.toggle("active", active);
    fields.hidden = !active;
    for (const control of fields.querySelectorAll("input, select, textarea, button")) {
      control.disabled = !active;
    }
  }

  const keyConfigured = normaliseBoolean(provider.keyConfigured);
  const configError = String(provider.configError || "");
  elements.discoveryKeyState.classList.toggle("configured", keyConfigured);
  elements.discoveryKeyState.classList.toggle("missing", !keyConfigured);
  elements.discoveryKeyState.classList.remove("pending");
  const keyLabel = configError
    ? "配置文件错误"
    : keyConfigured
      ? provider.keySource === "config" ? "本地 Key 已配置" : "环境 Key 已配置"
      : "Key 未配置";
  elements.discoveryKeyState.lastChild.textContent = keyLabel;
  elements.discoveryKeyState.title = configError;
  elements.discoveryConfigPathTooltip.textContent = provider.configPath || "配置路径不可用";
  elements.saveDiscoveryButton.textContent = `保存 ${name.toUpperCase()} 配置`;
}

function setActiveDiscoveryProvider(provider) {
  if (!["fofa", "hunter"].includes(provider)) return;
  state.discovery.activeProvider = provider;
  renderActiveDiscoveryProvider();
}

function renderDiscovery(discovery) {
  const targets = Array.isArray(discovery?.targets) ? discovery.targets : [];
  const targetCount = Number(discovery?.targetCount ?? targets.length);
  const providers = discovery?.providers && typeof discovery.providers === "object"
    ? discovery.providers
    : {
      fofa: {
        provider: "fofa",
        keyConfigured: discovery?.keyConfigured,
        keySource: discovery?.keySource,
        key: discovery?.key,
        endpoint: discovery?.endpoint,
        configPath: discovery?.configPath,
        configError: discovery?.configError,
      },
    };
  state.discovery = {
    activeProvider: state.discovery.activeProvider || "fofa",
    providers,
    targets,
    targetCount: Number.isFinite(targetCount) ? targetCount : targets.length,
  };

  const fofa = providers.fofa || {};
  const hunter = providers.hunter || {};
  elements.fofaEndpointInput.value = fofa.endpoint || elements.fofaEndpointInput.value;
  elements.fofaKeyInput.value = fofa.key || "";
  elements.fofaQueryInput.value = fofa.query || elements.fofaQueryInput.value;
  elements.fofaResultLimitInput.value = fofa.resultLimit || elements.fofaResultLimitInput.value;
  elements.hunterEndpointInput.value = hunter.endpoint || elements.hunterEndpointInput.value;
  elements.hunterKeyInput.value = hunter.key || "";
  elements.hunterQueryInput.value = hunter.query || elements.hunterQueryInput.value;
  elements.hunterResultLimitInput.value = hunter.resultLimit || elements.hunterResultLimitInput.value;
  renderActiveDiscoveryProvider();
  elements.discoveryTargetCount.textContent = String(state.discovery.targetCount);
  renderDiscoveredSources(targets);

  elements.discoveryTargets.replaceChildren();
  if (!targets.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "target-placeholder";
    placeholder.textContent = "尚未发现目标";
    elements.discoveryTargets.append(placeholder);
    return;
  }

  const fragment = document.createDocumentFragment();
  for (const target of targets) {
    const item = document.createElement("div");
    item.className = "target-item";

    const protocol = document.createElement("span");
    protocol.className = "target-protocol";
    protocol.textContent = target.protocol || "HTTP";

    const detail = document.createElement("div");
    detail.className = "target-detail";
    const url = document.createElement("span");
    url.className = "target-url";
    url.textContent = targetURL(target);
    url.title = targetURL(target);
    const meta = document.createElement("span");
    meta.className = "target-meta";
    const host = target.host || "未知主机";
    const ip = target.ip ? ` · ${target.ip}` : "";
    const port = target.port ? `:${target.port}` : "";
    meta.textContent = `${host}${port}${ip}`;

    detail.append(url, meta);
    item.append(protocol, detail);
    fragment.append(item);
  }
  elements.discoveryTargets.append(fragment);
}

function renderLogs(logs) {
  let visibleLogs = logs;
  if (state.logsClearedAnchor) {
    const anchor = logs.findLastIndex((log) => log.message === state.logsClearedAnchor);
    visibleLogs = anchor >= 0 ? logs.slice(anchor + 1) : logs;
  }

  elements.logOutput.replaceChildren();
  if (!visibleLogs.length) {
    const placeholder = document.createElement("div");
    placeholder.className = "log-placeholder";
    placeholder.textContent = "等待日志输出…";
    elements.logOutput.append(placeholder);
    return;
  }

  const fragment = document.createDocumentFragment();
  for (const log of visibleLogs.slice(-200)) {
    const line = document.createElement("div");
    line.className = "log-line";
    const time = document.createElement("span");
    time.className = "log-time";
    time.textContent = formatLogTime(log.time);
    const message = document.createElement("span");
    message.textContent = log.message;
    line.append(time, message);
    fragment.append(line);
  }
  elements.logOutput.append(fragment);
  elements.logOutput.scrollTop = elements.logOutput.scrollHeight;
}

function formatLogTime(value) {
  if (!value) return "·";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleTimeString("zh-CN", { hour12: false });
}

async function loadStatus({ quiet = false } = {}) {
  try {
    const status = await request("/api/status");
    updateStatusView(status);
    setConnection(true, "管理服务已连接");
  } catch (error) {
    setConnection(false, "管理服务未连接");
    if (!quiet) showToast(`状态读取失败：${error.message}`, "error");
    throw error;
  }
}

async function loadSettings({ quiet = false } = {}) {
  try {
    const settings = await request("/api/settings");
    applySettings(settings);
    updateStatusView(state.status);
  } catch (error) {
    if (!quiet) showToast(`配置读取失败：${error.message}`, "error");
    throw error;
  }
}

async function saveSettings({ quiet = false } = {}) {
  if (!elements.settingsForm.reportValidity()) {
    throw new Error("请先修正配置表单");
  }

  const settings = getSettingsFromForm();
  if (!settings.listen) {
    elements.listenInput.focus();
    throw new Error("监听地址不能为空");
  }

  const result = await request("/api/settings", {
    method: "PUT",
    body: JSON.stringify(settings),
  });
  applySettings(result?.settings || settings);
  if (!quiet) showToast("运行配置已保存", "success");
  return result;
}

async function loadProxies({ quiet = false } = {}) {
  try {
    const payload = await request("/api/proxies");
    const proxies = Array.isArray(payload) ? payload : payload?.proxies;
    state.proxies = Array.isArray(proxies) ? proxies : [];
    renderProxies();
    updateStatusView(state.status);
  } catch (error) {
    if (!quiet) showToast(`代理列表读取失败：${error.message}`, "error");
    throw error;
  }
}

async function loadLogs({ quiet = false } = {}) {
  try {
    const payload = await request("/api/logs");
    state.logs = normaliseLogs(payload);
    renderLogs(state.logs);
  } catch (error) {
    if (!quiet) showToast(`日志读取失败：${error.message}`, "error");
    throw error;
  }
}

async function loadDiscovery({ quiet = false } = {}) {
  try {
    const discovery = await request("/api/discovery");
    renderDiscovery(discovery);
  } catch (error) {
    if (!quiet) showToast(`发现状态读取失败：${error.message}`, "error");
    throw error;
  }
}

async function refreshAll({ quiet = false } = {}) {
  elements.refreshButton.classList.add("loading");
  elements.refreshButton.disabled = true;
  const tasks = [
    loadStatus({ quiet }),
    loadProxies({ quiet }),
    loadLogs({ quiet }),
    loadDiscovery({ quiet }),
  ];
  if (!state.settingsLoaded) tasks.push(loadSettings({ quiet }));

  const results = await Promise.allSettled(tasks);
  elements.refreshButton.classList.remove("loading");
  elements.refreshButton.disabled = false;

  const failures = results.filter((result) => result.status === "rejected");
  if (!quiet && failures.length === 0) {
    showToast("数据已刷新", "success");
  }
}

async function handleSave() {
  elements.saveButton.disabled = true;
  try {
    await saveSettings();
  } catch (error) {
    showToast(error.message, "error");
  } finally {
    elements.saveButton.disabled = false;
  }
}

async function handleStart() {
  setButtonLoading(elements.startButton, true, "正在启动");
  try {
    await saveSettings({ quiet: true });
    const result = await request("/api/start", { method: "POST" });
    showToast(result?.message || "glider 已启动", "success");
    await refreshAll({ quiet: true });
  } catch (error) {
    showToast(`启动失败：${error.message}`, "error");
  } finally {
    setButtonLoading(elements.startButton, false, "");
    updateStatusView(state.status);
  }
}

async function handleStop() {
  setButtonLoading(elements.stopButton, true, "正在停止");
  try {
    const result = await request("/api/stop", { method: "POST" });
    showToast(result?.message || "glider 已停止", "success");
    await refreshAll({ quiet: true });
  } catch (error) {
    showToast(`停止失败：${error.message}`, "error");
  } finally {
    setButtonLoading(elements.stopButton, false, "");
    updateStatusView(state.status);
  }
}

async function handleFetch() {
  const settings = getSettingsFromForm();
  if (!settings.sources.length && !state.discovery.targets.length) {
    elements.sourcesInput.focus();
    showToast("请先填写代理池地址，或通过 FOFA 保存发现目标", "warning");
    return;
  }

  setButtonLoading(elements.fetchButton, true, "正在拉取并验证");
  elements.fetchResult.hidden = true;
  try {
    await saveSettings({ quiet: true });
    const result = await request("/api/fetch", {
      method: "POST",
      body: JSON.stringify({
        sources: settings.sources,
        protocol: settings.protocol,
        apply: true,
      }),
    });
    state.sourceStatuses = Array.isArray(result?.sources) ? result.sources : [];
    renderDiscoveredSources(state.discovery.targets);

    const count = Number(result?.applied ?? result?.count ?? result?.proxies?.length ?? 0);
    const sourceCount = Number(result?.sourceCount ?? settings.sources.length + state.discovery.targetCount);
    const message = result?.message || `已从 ${sourceCount} 个来源拉取并应用 ${count} 个 ${settings.protocol.toUpperCase()} 代理`;
    elements.fetchResult.textContent = message;
    elements.fetchResult.hidden = false;
    showToast(message, "success", 5000);
    await Promise.allSettled([
      loadProxies({ quiet: true }),
      loadStatus({ quiet: true }),
      loadLogs({ quiet: true }),
    ]);
  } catch (error) {
    if (Array.isArray(error.payload?.sources)) {
      state.sourceStatuses = error.payload.sources;
      renderDiscoveredSources(state.discovery.targets);
    }
    showToast(`代理拉取失败：${error.message}`, "error", 6000);
  } finally {
    setButtonLoading(elements.fetchButton, false, "");
  }
}

function validateConfiguredDiscoveryProviders() {
  let configuredCount = 0;
  for (const provider of ["fofa", "hunter"]) {
    const stored = state.discovery.providers[provider] || {};
    const values = discoveryProviderValues(provider);
    const configured = Boolean(values.key) || normaliseBoolean(stored.keyConfigured);
    if (!configured) continue;
    configuredCount += 1;

    const queryInput = provider === "hunter" ? elements.hunterQueryInput : elements.fofaQueryInput;
    const limitInput = provider === "hunter" ? elements.hunterResultLimitInput : elements.fofaResultLimitInput;
    if (!queryInput.value.trim()) {
      setActiveDiscoveryProvider(provider);
      queryInput.focus();
      throw new Error(`${provider.toUpperCase()} 查询语句不能为空`);
    }
    if (!values.endpoint) {
      setActiveDiscoveryProvider(provider);
      (provider === "hunter" ? elements.hunterEndpointInput : elements.fofaEndpointInput).focus();
      throw new Error(`${provider.toUpperCase()} 查询接口不能为空`);
    }
    const limit = Number(limitInput.value);
    const min = Number(limitInput.min);
    const max = Number(limitInput.max);
    if (!Number.isInteger(limit) || limit < min || limit > max) {
      setActiveDiscoveryProvider(provider);
      limitInput.reportValidity();
      throw new Error(`${provider.toUpperCase()} 最大结果数无效`);
    }
  }
  if (!configuredCount) {
    throw new Error("请至少配置一个 FOFA 或 Hunter API Key");
  }
}

async function handleDiscover() {
  try {
    validateConfiguredDiscoveryProviders();
  } catch (error) {
    showToast(error.message, "warning");
    return;
  }

  setButtonLoading(elements.discoverButton, true, "正在搜索");
  try {
    await saveAllDiscoveryConfigurations();
    const result = await request("/api/discover", {
      method: "POST",
      body: JSON.stringify({}),
    });
    await loadDiscovery({ quiet: true });

    const count = state.discovery.targetCount;
    const failed = Array.isArray(result?.errors) ? result.errors.length : 0;
    const message = failed
      ? `已发现并保存 ${count} 个目标，${failed} 个接口失败`
      : count ? `已发现并保存 ${count} 个目标` : "搜索完成，未发现匹配目标";
    showToast(message, count ? "success" : "warning", 5000);
  } catch (error) {
    showToast(`资产发现失败：${error.message}`, "error", 6000);
  } finally {
    setButtonLoading(elements.discoverButton, false, "");
  }
}

function discoveryProviderValues(provider) {
  if (provider === "hunter") {
    return {
      provider,
      endpoint: elements.hunterEndpointInput.value.trim(),
      key: elements.hunterKeyInput.value.trim(),
    };
  }
  return {
    provider: "fofa",
    endpoint: elements.fofaEndpointInput.value.trim(),
    key: elements.fofaKeyInput.value.trim(),
  };
}

async function saveDiscoveryConfiguration({ quiet = false, provider = state.discovery.activeProvider } = {}) {
  if (!elements.discoveryForm.reportValidity()) {
    throw new Error("请先补全资产搜索配置");
  }
  await saveSettings({ quiet: true });
  await request("/api/discovery", {
    method: "PUT",
    body: JSON.stringify(discoveryProviderValues(provider)),
  });
  await loadDiscovery({ quiet: true });
  if (!quiet) showToast(`${provider.toUpperCase()} 配置已保存并生效`, "success");
}

async function saveAllDiscoveryConfigurations() {
  await saveSettings({ quiet: true });
  const requests = ["fofa", "hunter"].flatMap((provider) => {
    const values = discoveryProviderValues(provider);
    if (!values.endpoint && !values.key) return [];
    return [request("/api/discovery", {
      method: "PUT",
      body: JSON.stringify(values),
    })];
  });
  await Promise.all(requests);
}

async function handleSaveDiscovery() {
  elements.saveDiscoveryButton.disabled = true;
  try {
    await saveDiscoveryConfiguration();
  } catch (error) {
    showToast(`资产搜索配置保存失败：${error.message}`, "error");
  } finally {
    elements.saveDiscoveryButton.disabled = false;
  }
}

async function setProxyBlacklisted(proxy, blacklisted, button) {
  if (!proxy) return;
  button.disabled = true;
  let succeeded = false;
  try {
    await request("/api/proxies", {
      method: "PUT",
      body: JSON.stringify({ proxy, blacklisted }),
    });
    succeeded = true;
    showToast(blacklisted ? `已拉黑 ${proxy}` : `已解除拉黑 ${proxy}`, "success");
    try {
      await Promise.all([
        loadProxies({ quiet: true }),
        loadStatus({ quiet: true }),
      ]);
    } catch (error) {
      showToast(`操作已生效，但状态刷新失败：${error.message}`, "warning");
    }
  } catch (error) {
    showToast(`${blacklisted ? "拉黑" : "解除拉黑"}失败：${error.message}`, "error");
  } finally {
    if (!succeeded) button.disabled = false;
  }
}

function toggleSecret(input, button) {
  const visible = input.type === "text";
  input.type = visible ? "password" : "text";
  button.textContent = visible ? "显示" : "隐藏";
  button.setAttribute("aria-pressed", String(!visible));
}

function bindEvents() {
  elements.settingsForm.addEventListener("submit", (event) => {
    event.preventDefault();
    handleSave();
  });
  elements.saveButton.addEventListener("click", handleSave);
  elements.saveDiscoveryButton.addEventListener("click", handleSaveDiscovery);
  elements.startButton.addEventListener("click", handleStart);
  elements.stopButton.addEventListener("click", handleStop);
  elements.fetchButton.addEventListener("click", handleFetch);
  elements.discoverButton.addEventListener("click", handleDiscover);
  elements.toggleFofaKeyButton.addEventListener("click", () => toggleSecret(elements.fofaKeyInput, elements.toggleFofaKeyButton));
  elements.toggleHunterKeyButton.addEventListener("click", () => toggleSecret(elements.hunterKeyInput, elements.toggleHunterKeyButton));
  for (const tab of elements.providerTabs) {
    tab.addEventListener("click", () => setActiveDiscoveryProvider(tab.dataset.provider));
  }
  elements.currentProxyBlacklistButton.addEventListener("click", () => {
    const context = currentProxyContext();
    if (!context.identity || elements.currentProxyBlacklistButton.disabled) return;
    state.pendingBlacklist = context.identity;
    state.pendingBlacklistButton = elements.currentProxyBlacklistButton;
    elements.blacklistConfirmMessage.textContent =
      `确认拉黑当前代理 ${context.identity}？拉黑后将重载代理池。`;
    elements.blacklistConfirmDialog.showModal();
  });
  elements.blacklistCancelButton.addEventListener("click", () => {
    elements.blacklistConfirmDialog.close();
    state.pendingBlacklist = "";
    state.pendingBlacklistButton = null;
  });
  elements.blacklistConfirmDialog.addEventListener("close", () => {
    state.pendingBlacklist = "";
    state.pendingBlacklistButton = null;
  });
  elements.blacklistConfirmButton.addEventListener("click", (event) => {
    event.preventDefault();
    const proxy = state.pendingBlacklist;
    const button = state.pendingBlacklistButton;
    elements.blacklistConfirmDialog.close();
    state.pendingBlacklist = "";
    state.pendingBlacklistButton = null;
    if (proxy && button) setProxyBlacklisted(proxy, true, button);
  });
  for (const button of document.querySelectorAll(".sort-button")) {
    button.addEventListener("click", () => {
      const key = button.dataset.sort;
      if (state.proxySort.key !== key) {
        state.proxySort = { key, direction: "desc" };
      } else if (state.proxySort.direction === "desc") {
        state.proxySort.direction = "asc";
      } else if (state.proxySort.direction === "asc") {
        state.proxySort = { key: "", direction: "" };
      } else {
        state.proxySort = { key, direction: "desc" };
      }
      state.proxyPage = 1;
      renderProxies();
    });
  }
  elements.refreshButton.addEventListener("click", () => refreshAll());
  elements.reloadProxiesButton.addEventListener("click", () => loadProxies());
  elements.sourcesInput.addEventListener("input", () => renderDiscoveredSources(state.discovery.targets));
  elements.proxySearch.addEventListener("input", () => {
    state.proxyPage = 1;
    renderProxies();
  });
  elements.proxyPageSize.addEventListener("change", () => {
    state.proxyPageSize = Number(elements.proxyPageSize.value) || 10;
    state.proxyPage = 1;
    renderProxies();
  });
  elements.proxyPrevPage.addEventListener("click", () => {
    state.proxyPage = Math.max(1, state.proxyPage - 1);
    renderProxies();
  });
  elements.proxyNextPage.addEventListener("click", () => {
    state.proxyPage += 1;
    renderProxies();
  });
  elements.clearLogsButton.addEventListener("click", () => {
    state.logsClearedAnchor = state.logs.at(-1)?.message || "";
    renderLogs([]);
    showToast("已清空当前日志视图");
  });
}

function startPolling() {
  window.clearInterval(state.polling);
  state.polling = window.setInterval(async () => {
    await Promise.allSettled([
      loadStatus({ quiet: true }),
      loadProxies({ quiet: true }),
      loadLogs({ quiet: true }),
    ]);
  }, 5000);
}

async function initialise() {
  bindEvents();
  setActiveDiscoveryProvider("fofa");
  await refreshAll({ quiet: true });
  startPolling();
}

initialise();
