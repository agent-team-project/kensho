const els = {
  tokenInput: document.getElementById("tokenInput"),
  saveToken: document.getElementById("saveToken"),
  refresh: document.getElementById("refresh"),
  autoRefresh: document.getElementById("autoRefresh"),
  notice: document.getElementById("notice"),
  connectionState: document.getElementById("connectionState"),
  connectionText: document.getElementById("connectionText"),
  instanceCount: document.getElementById("instanceCount"),
  runningCount: document.getElementById("runningCount"),
  jobCount: document.getElementById("jobCount"),
  activeJobCount: document.getElementById("activeJobCount"),
  pipelineCount: document.getElementById("pipelineCount"),
  budgetTeamCount: document.getElementById("budgetTeamCount"),
  teamCount: document.getElementById("teamCount"),
  instancesBody: document.getElementById("instancesBody"),
  jobsBody: document.getElementById("jobsBody"),
  pipelinesBody: document.getElementById("pipelinesBody"),
  budgetsBody: document.getElementById("budgetsBody"),
  teamsBody: document.getElementById("teamsBody"),
  instanceUpdated: document.getElementById("instanceUpdated"),
  jobUpdated: document.getElementById("jobUpdated"),
  pipelineUpdated: document.getElementById("pipelineUpdated"),
  budgetUpdated: document.getElementById("budgetUpdated"),
  teamUpdated: document.getElementById("teamUpdated"),
  refreshState: document.getElementById("refreshState"),
};

const tokenKey = "agent-team.daemonToken";
const autoRefreshKey = "agent-team.autoRefresh";
const refreshIntervalMs = 15000;
const endpoints = {
  instances: { path: "/v1/instances", label: "instances" },
  jobs: { path: "/v1/jobs", label: "jobs" },
  topology: { path: "/v1/topology", label: "topology" },
};

let refreshTimer = null;
let refreshInFlight = false;

class HTTPError extends Error {
  constructor(path, status, body) {
    const detail = body || "request failed";
    super(`${path} ${status}: ${detail}`);
    this.name = "HTTPError";
    this.path = path;
    this.status = status;
    this.body = body;
  }
}

function authHeaders() {
  const token = sessionStorage.getItem(tokenKey) || "";
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function getJSON(path) {
  const response = await fetch(path, {
    headers: authHeaders(),
    cache: "no-store",
  });
  if (!response.ok) {
    const body = (await response.text()).trim();
    throw new HTTPError(path, response.status, body || response.statusText);
  }
  return response.json();
}

async function loadEndpoint(key) {
  const endpoint = endpoints[key];
  try {
    return { key, ok: true, data: await getJSON(endpoint.path) };
  } catch (error) {
    return { key, ok: false, error };
  }
}

function field(row, ...names) {
  for (const name of names) {
    if (row && row[name] !== undefined && row[name] !== null && row[name] !== "") {
      return row[name];
    }
  }
  return "";
}

function text(value, fallback = "-") {
  if (value === undefined || value === null) {
    return fallback;
  }
  const rendered = String(value).trim();
  return rendered || fallback;
}

function slug(value) {
  return text(value, "unknown").toLowerCase().replace(/[^a-z0-9_-]+/g, "-");
}

function asArray(value) {
  return Array.isArray(value) ? value : [];
}

function resultArray(result) {
  return result && result.ok && Array.isArray(result.data) ? result.data : [];
}

function resultObject(result) {
  return result && result.ok && result.data && typeof result.data === "object" ? result.data : {};
}

function topologyArray(result, name) {
  const topology = resultObject(result);
  return Array.isArray(topology[name]) ? topology[name] : [];
}

function countLabel(value, singular, plural = `${singular}s`) {
  const count = asArray(value).length;
  return `${count} ${count === 1 ? singular : plural}`;
}

function compactNumber(value) {
  if (value === undefined || value === null || value === "") {
    return "-";
  }
  const number = Number(value);
  if (!Number.isFinite(number)) {
    return text(value);
  }
  return new Intl.NumberFormat(undefined, {
    notation: Math.abs(number) >= 100000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(number);
}

function readableErrorBody(body) {
  const raw = text(body, "");
  if (!raw) {
    return "";
  }
  try {
    const parsed = JSON.parse(raw);
    return text(parsed.error || parsed.message, raw);
  } catch (_) {
    return raw;
  }
}

function activeJob(job) {
  const status = text(field(job, "status", "Status")).toLowerCase();
  return status === "queued" || status === "running" || status === "blocked";
}

function toneForStatus(value) {
  const normalized = slug(value);
  if (["running", "done", "connected", "active", "healthy"].includes(normalized)) {
    return "positive";
  }
  if (["queued", "planning", "implementing", "awaiting_review", "checking"].includes(normalized)) {
    return "info";
  }
  if (["blocked", "failed", "crashed", "disconnected", "error"].includes(normalized)) {
    return "negative";
  }
  if (["degraded", "warning", "warn", "stale"].includes(normalized)) {
    return "warning";
  }
  return "neutral";
}

function pill(value, tone = toneForStatus(value)) {
  const label = text(value, "unknown");
  const span = document.createElement("span");
  span.className = `pill ${tone} ${slug(label)}`;
  span.textContent = label;
  return span;
}

function statusPill(value) {
  return pill(text(value, "unknown"));
}

function cell(value) {
  const td = document.createElement("td");
  if (value instanceof Node) {
    td.appendChild(value);
  } else {
    td.textContent = text(value);
  }
  return td;
}

function emptyRow(columns, title, detail) {
  const tr = document.createElement("tr");
  const td = document.createElement("td");
  const wrap = document.createElement("div");
  const headline = document.createElement("strong");
  td.colSpan = columns;
  td.className = "empty";
  wrap.className = "empty-state";
  headline.textContent = title;
  wrap.appendChild(headline);
  if (detail) {
    const copy = document.createElement("span");
    copy.textContent = detail;
    wrap.appendChild(copy);
  }
  td.appendChild(wrap);
  tr.appendChild(td);
  return tr;
}

function errorRow(columns, title, error) {
  return emptyRow(columns, title, humanError(error));
}

function triggerSummary(trigger) {
  if (!trigger) {
    return "-";
  }
  const event = text(field(trigger, "event", "Event"));
  const match = field(trigger, "match", "Match");
  if (!match || typeof match !== "object" || !Object.keys(match).length) {
    return event;
  }
  const parts = Object.entries(match).map(([key, value]) => `${key}=${Array.isArray(value) ? value.join("|") : value}`);
  return `${event} ${parts.join(" ")}`;
}

function stepBudget(step) {
  const tokens = field(step, "token_budget", "TokenBudget");
  const time = field(step, "time_budget", "TimeBudget", "timeout", "Timeout");
  const parts = [];
  if (tokens) {
    parts.push(compactNumber(tokens));
  }
  if (time) {
    parts.push(text(time));
  }
  return parts.join(" / ");
}

function stepList(steps) {
  const wrap = document.createElement("div");
  wrap.className = "step-list";
  if (!Array.isArray(steps) || !steps.length) {
    wrap.textContent = "-";
    return wrap;
  }
  for (const step of steps) {
    const item = document.createElement("span");
    item.className = "step-chip";
    const name = text(field(step, "label", "Label", "id", "ID"));
    const target = text(field(step, "target", "Target"));
    const budget = stepBudget(step);
    item.textContent = budget ? `${name} -> ${target} (${budget})` : `${name} -> ${target}`;
    wrap.appendChild(item);
  }
  return wrap;
}

function renderInstances(result, instances) {
  els.instancesBody.replaceChildren();
  if (!result.ok) {
    els.instancesBody.appendChild(errorRow(4, "Instances unavailable", result.error));
    return;
  }
  if (!instances.length) {
    els.instancesBody.appendChild(emptyRow(4, "No instances reported", "Start an instance to populate daemon lifecycle rows."));
    return;
  }
  for (const instance of instances) {
    const tr = document.createElement("tr");
    tr.append(
      cell(field(instance, "instance", "Instance")),
      cell(field(instance, "agent", "Agent")),
      cell(statusPill(field(instance, "status", "Status", "lifecycle"))),
      cell(field(instance, "job", "Job"))
    );
    els.instancesBody.appendChild(tr);
  }
}

function renderJobs(result, jobs) {
  els.jobsBody.replaceChildren();
  if (!result.ok) {
    els.jobsBody.appendChild(errorRow(5, "Jobs unavailable", result.error));
    return;
  }
  if (!jobs.length) {
    els.jobsBody.appendChild(emptyRow(5, "No jobs recorded", "Dispatch a durable job to see ticket, pipeline, and instance state here."));
    return;
  }
  for (const job of jobs) {
    const tr = document.createElement("tr");
    tr.append(
      cell(field(job, "id", "ID")),
      cell(field(job, "ticket", "Ticket")),
      cell(statusPill(field(job, "status", "Status"))),
      cell(field(job, "pipeline", "Pipeline")),
      cell(field(job, "instance", "Instance"))
    );
    els.jobsBody.appendChild(tr);
  }
}

function pipelineActiveCount(pipeline, jobs) {
  const name = field(pipeline, "name", "Name");
  return jobs.filter((job) => field(job, "pipeline", "Pipeline") === name && activeJob(job)).length;
}

function pipelineTeamMap(teams) {
  const out = new Map();
  for (const team of teams) {
    const name = field(team, "name", "Name");
    const pipelines = field(team, "pipelines", "Pipelines");
    if (!name || !Array.isArray(pipelines)) {
      continue;
    }
    for (const pipeline of pipelines) {
      out.set(pipeline, name);
    }
  }
  return out;
}

function renderPipelines(result, pipelines, jobs) {
  els.pipelinesBody.replaceChildren();
  if (!result.ok) {
    els.pipelinesBody.appendChild(errorRow(4, "Pipelines unavailable", result.error));
    return;
  }
  if (!pipelines.length) {
    els.pipelinesBody.appendChild(emptyRow(4, "No pipelines declared", "Add topology pipelines to show trigger, step, and active-job state."));
    return;
  }
  for (const pipeline of pipelines) {
    const active = pipelineActiveCount(pipeline, jobs);
    const tr = document.createElement("tr");
    tr.append(
      cell(field(pipeline, "name", "Name")),
      cell(triggerSummary(field(pipeline, "trigger", "Trigger"))),
      cell(stepList(field(pipeline, "steps", "Steps"))),
      cell(pill(active ? `${active} active` : "idle", active ? "positive" : "neutral"))
    );
    els.pipelinesBody.appendChild(tr);
  }
}

function renderBudgets(result, budgets, jobs, teams) {
  els.budgetsBody.replaceChildren();
  if (!result.ok) {
    els.budgetsBody.appendChild(errorRow(5, "Budgets unavailable", result.error));
    return;
  }
  if (!budgets.length) {
    els.budgetsBody.appendChild(emptyRow(5, "No budgets configured", "Declare team budgets to track tokens, job caps, and allocation mode."));
    return;
  }
  const byPipeline = pipelineTeamMap(teams);
  for (const budget of budgets) {
    const team = field(budget, "team", "Team");
    const active = jobs.filter((job) => activeJob(job) && byPipeline.get(field(job, "pipeline", "Pipeline")) === team).length;
    const tr = document.createElement("tr");
    tr.append(
      cell(team),
      cell(compactNumber(field(budget, "tokens_per_day", "TokensPerDay"))),
      cell(field(budget, "jobs_in_flight", "JobsInFlight")),
      cell(pill(active ? `${active} active` : "idle", active ? "positive" : "neutral")),
      cell(pill(field(budget, "allocation", "Allocation") || "unconfigured", "info"))
    );
    els.budgetsBody.appendChild(tr);
  }
}

function teamActiveJobs(team, jobs) {
  const pipelines = asArray(field(team, "pipelines", "Pipelines"));
  return jobs.filter((job) => pipelines.includes(field(job, "pipeline", "Pipeline")) && activeJob(job)).length;
}

function renderTeams(result, teams, jobs) {
  els.teamsBody.replaceChildren();
  if (!result.ok) {
    els.teamsBody.appendChild(errorRow(5, "Teams unavailable", result.error));
    return;
  }
  if (!teams.length) {
    els.teamsBody.appendChild(emptyRow(5, "No teams declared", "Topology teams will appear here once instances or pipelines are grouped."));
    return;
  }
  for (const team of teams) {
    const active = teamActiveJobs(team, jobs);
    const tr = document.createElement("tr");
    tr.append(
      cell(field(team, "name", "Name")),
      cell(countLabel(field(team, "instances", "Instances"), "instance")),
      cell(countLabel(field(team, "pipelines", "Pipelines"), "pipeline")),
      cell(countLabel(field(team, "channels", "Channels"), "channel")),
      cell(pill(active ? `${active} active` : "idle", active ? "positive" : "neutral"))
    );
    els.teamsBody.appendChild(tr);
  }
}

function updateSummary(instances, jobs, topology) {
  const pipelines = Array.isArray(topology.pipelines) ? topology.pipelines : [];
  const budgets = Array.isArray(topology.budgets) ? topology.budgets : [];
  const teams = Array.isArray(topology.teams) ? topology.teams : [];
  els.instanceCount.textContent = instances.length;
  els.runningCount.textContent = instances.filter((row) => text(field(row, "status", "Status")).toLowerCase() === "running").length;
  els.jobCount.textContent = jobs.length;
  els.activeJobCount.textContent = jobs.filter(activeJob).length;
  els.pipelineCount.textContent = pipelines.length;
  els.budgetTeamCount.textContent = budgets.length;
  els.teamCount.textContent = teams.length;
}

function setNotice(message, tone = "info") {
  els.notice.textContent = message;
  els.notice.className = "notice";
  if (tone) {
    els.notice.classList.add(tone);
  }
}

function setConnection(state, message) {
  els.connectionState.className = `connection ${state}`;
  els.connectionText.textContent = message;
}

function humanError(error) {
  if (error instanceof HTTPError) {
    const body = readableErrorBody(error.body);
    if (error.status === 401) {
      return "Unauthorized. Enter a bearer token and connect.";
    }
    if (error.status === 403) {
      return "Forbidden. This token does not have access to the requested daemon resource.";
    }
    if (error.status === 503) {
      return text(body, "Service unavailable.");
    }
    return `${error.status}: ${text(body, "request failed")}`;
  }
  if (error instanceof TypeError) {
    return "Network request failed. Check that the daemon is running and reachable.";
  }
  return text(error && error.message, "Unknown error");
}

function failureSummary(failures) {
  return failures.map((failure) => `${endpoints[failure.key].label}: ${humanError(failure.error)}`).join(" | ");
}

function updatePanelTimes(results, stamp) {
  els.instanceUpdated.textContent = results.instances.ok ? stamp : "unavailable";
  els.jobUpdated.textContent = results.jobs.ok ? stamp : "unavailable";
  els.pipelineUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.budgetUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.teamUpdated.textContent = results.topology.ok ? stamp : "unavailable";
}

async function refresh() {
  if (refreshInFlight) {
    return;
  }
  refreshInFlight = true;
  els.refresh.disabled = true;
  setConnection("checking", "Checking connection");
  setNotice("Loading daemon data...");
  try {
    const loaded = await Promise.all(Object.keys(endpoints).map(loadEndpoint));
    const results = Object.fromEntries(loaded.map((result) => [result.key, result]));
    const safeInstances = resultArray(results.instances);
    const safeJobs = resultArray(results.jobs);
    const safeTopology = resultObject(results.topology);
    const safePipelines = topologyArray(results.topology, "pipelines");
    const safeBudgets = topologyArray(results.topology, "budgets");
    const safeTeams = topologyArray(results.topology, "teams");

    renderInstances(results.instances, safeInstances);
    renderJobs(results.jobs, safeJobs);
    renderPipelines(results.topology, safePipelines, safeJobs);
    renderBudgets(results.topology, safeBudgets, safeJobs, safeTeams);
    renderTeams(results.topology, safeTeams, safeJobs);
    updateSummary(safeInstances, safeJobs, safeTopology);

    const stamp = new Date().toLocaleTimeString();
    const failures = loaded.filter((result) => !result.ok);
    const successes = loaded.length - failures.length;
    updatePanelTimes(results, stamp);
    if (!failures.length) {
      setConnection("connected", "Connected");
      setNotice(`Daemon data loaded at ${stamp}.`, "success");
    } else if (successes > 0) {
      setConnection("degraded", "Partial connection");
      setNotice(`Partial daemon data loaded at ${stamp}. ${failureSummary(failures)}`, "warning");
    } else {
      setConnection("disconnected", "Disconnected");
      setNotice(`Unable to load daemon data. ${failureSummary(failures)}`, "error");
    }
  } catch (error) {
    setConnection("disconnected", "Render error");
    setNotice(`Dashboard render failed. ${humanError(error)}`, "error");
  } finally {
    els.refresh.disabled = false;
    refreshInFlight = false;
  }
}

function setAutoRefresh(enabled) {
  sessionStorage.setItem(autoRefreshKey, enabled ? "1" : "0");
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
  if (enabled) {
    refreshTimer = setInterval(refresh, refreshIntervalMs);
  }
  els.refreshState.textContent = enabled ? "Every 15s" : "Manual";
}

els.saveToken.addEventListener("click", () => {
  const token = els.tokenInput.value.trim();
  if (token) {
    sessionStorage.setItem(tokenKey, token);
  } else {
    sessionStorage.removeItem(tokenKey);
  }
  refresh();
});

els.refresh.addEventListener("click", refresh);

els.autoRefresh.addEventListener("change", () => {
  setAutoRefresh(els.autoRefresh.checked);
});

els.tokenInput.value = sessionStorage.getItem(tokenKey) || "";
els.autoRefresh.checked = sessionStorage.getItem(autoRefreshKey) !== "0";
setAutoRefresh(els.autoRefresh.checked);
refresh();
