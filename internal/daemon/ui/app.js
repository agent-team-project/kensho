const els = {
  tokenInput: document.getElementById("tokenInput"),
  saveToken: document.getElementById("saveToken"),
  refresh: document.getElementById("refresh"),
  autoRefresh: document.getElementById("autoRefresh"),
  notice: document.getElementById("notice"),
  instanceCount: document.getElementById("instanceCount"),
  runningCount: document.getElementById("runningCount"),
  jobCount: document.getElementById("jobCount"),
  activeJobCount: document.getElementById("activeJobCount"),
  pipelineCount: document.getElementById("pipelineCount"),
  budgetTeamCount: document.getElementById("budgetTeamCount"),
  instancesBody: document.getElementById("instancesBody"),
  jobsBody: document.getElementById("jobsBody"),
  pipelinesBody: document.getElementById("pipelinesBody"),
  budgetsBody: document.getElementById("budgetsBody"),
  instanceUpdated: document.getElementById("instanceUpdated"),
  jobUpdated: document.getElementById("jobUpdated"),
  pipelineUpdated: document.getElementById("pipelineUpdated"),
  budgetUpdated: document.getElementById("budgetUpdated"),
  refreshState: document.getElementById("refreshState"),
};

const tokenKey = "agent-team.daemonToken";
const autoRefreshKey = "agent-team.autoRefresh";
const refreshIntervalMs = 15000;
let refreshTimer = null;
let refreshInFlight = false;

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
    const body = await response.text();
    throw new Error(`${path} ${response.status}: ${body || response.statusText}`);
  }
  return response.json();
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

function activeJob(job) {
  const status = text(field(job, "status", "Status")).toLowerCase();
  return status === "queued" || status === "running" || status === "blocked";
}

function statusPill(value) {
  const label = text(value, "unknown");
  const span = document.createElement("span");
  span.className = `pill ${label.toLowerCase().replace(/[^a-z0-9_-]+/g, "-")}`;
  span.textContent = label;
  return span;
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

function emptyRow(columns, label) {
  const tr = document.createElement("tr");
  const td = document.createElement("td");
  td.colSpan = columns;
  td.className = "empty";
  td.textContent = label;
  tr.appendChild(td);
  return tr;
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

function renderInstances(instances) {
  els.instancesBody.replaceChildren();
  if (!instances.length) {
    els.instancesBody.appendChild(emptyRow(4, "No instances"));
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

function renderJobs(jobs) {
  els.jobsBody.replaceChildren();
  if (!jobs.length) {
    els.jobsBody.appendChild(emptyRow(5, "No jobs"));
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

function renderPipelines(pipelines, jobs) {
  els.pipelinesBody.replaceChildren();
  if (!pipelines.length) {
    els.pipelinesBody.appendChild(emptyRow(4, "No pipelines"));
    return;
  }
  for (const pipeline of pipelines) {
    const name = field(pipeline, "name", "Name");
    const active = jobs.filter((job) => field(job, "pipeline", "Pipeline") === name && activeJob(job)).length;
    const tr = document.createElement("tr");
    tr.append(
      cell(name),
      cell(triggerSummary(field(pipeline, "trigger", "Trigger"))),
      cell(stepList(field(pipeline, "steps", "Steps"))),
      cell(active)
    );
    els.pipelinesBody.appendChild(tr);
  }
}

function renderBudgets(budgets, jobs, teams) {
  els.budgetsBody.replaceChildren();
  if (!budgets.length) {
    els.budgetsBody.appendChild(emptyRow(5, "No budgets"));
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
      cell(active),
      cell(field(budget, "allocation", "Allocation"))
    );
    els.budgetsBody.appendChild(tr);
  }
}

function updateSummary(instances, jobs, topology) {
  const pipelines = Array.isArray(topology.pipelines) ? topology.pipelines : [];
  const budgets = Array.isArray(topology.budgets) ? topology.budgets : [];
  els.instanceCount.textContent = instances.length;
  els.runningCount.textContent = instances.filter((row) => text(field(row, "status", "Status")).toLowerCase() === "running").length;
  els.jobCount.textContent = jobs.length;
  els.activeJobCount.textContent = jobs.filter(activeJob).length;
  els.pipelineCount.textContent = pipelines.length;
  els.budgetTeamCount.textContent = budgets.length;
}

function setNotice(message, error = false) {
  els.notice.textContent = message;
  els.notice.classList.toggle("error", error);
}

async function refresh() {
  if (refreshInFlight) {
    return;
  }
  refreshInFlight = true;
  els.refresh.disabled = true;
  setNotice("Loading daemon data...");
  try {
    const [instances, jobs, topology] = await Promise.all([
      getJSON("/v1/instances"),
      getJSON("/v1/jobs"),
      getJSON("/v1/topology"),
    ]);
    const safeInstances = Array.isArray(instances) ? instances : [];
    const safeJobs = Array.isArray(jobs) ? jobs : [];
    const safeTopology = topology && typeof topology === "object" ? topology : {};
    const safePipelines = Array.isArray(safeTopology.pipelines) ? safeTopology.pipelines : [];
    const safeBudgets = Array.isArray(safeTopology.budgets) ? safeTopology.budgets : [];
    const safeTeams = Array.isArray(safeTopology.teams) ? safeTopology.teams : [];
    renderInstances(safeInstances);
    renderJobs(safeJobs);
    renderPipelines(safePipelines, safeJobs);
    renderBudgets(safeBudgets, safeJobs, safeTeams);
    updateSummary(safeInstances, safeJobs, safeTopology);
    const stamp = new Date().toLocaleTimeString();
    els.instanceUpdated.textContent = stamp;
    els.jobUpdated.textContent = stamp;
    els.pipelineUpdated.textContent = stamp;
    els.budgetUpdated.textContent = stamp;
    setNotice(`Daemon data loaded at ${stamp}.`);
  } catch (err) {
    setNotice(err.message, true);
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
