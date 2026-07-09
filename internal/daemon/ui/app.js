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
  modelTierCount: document.getElementById("modelTierCount"),
  bounceClassCount: document.getElementById("bounceClassCount"),
  pipelineCount: document.getElementById("pipelineCount"),
  budgetTeamCount: document.getElementById("budgetTeamCount"),
  teamCount: document.getElementById("teamCount"),
  scheduleCount: document.getElementById("scheduleCount"),
  deploymentCount: document.getElementById("deploymentCount"),
  deadlineCount: document.getElementById("deadlineCount"),
  instancesBody: document.getElementById("instancesBody"),
  jobsBody: document.getElementById("jobsBody"),
  modelTierBody: document.getElementById("modelTierBody"),
  bounceClassBody: document.getElementById("bounceClassBody"),
  pipelinesBody: document.getElementById("pipelinesBody"),
  budgetsBody: document.getElementById("budgetsBody"),
  teamsBody: document.getElementById("teamsBody"),
  resourcesBody: document.getElementById("resourcesBody"),
  schedulesBody: document.getElementById("schedulesBody"),
  deadlinesBody: document.getElementById("deadlinesBody"),
  orgView: document.getElementById("orgView"),
  instanceUpdated: document.getElementById("instanceUpdated"),
  jobUpdated: document.getElementById("jobUpdated"),
  telemetryUpdated: document.getElementById("telemetryUpdated"),
  pipelineUpdated: document.getElementById("pipelineUpdated"),
  budgetUpdated: document.getElementById("budgetUpdated"),
  teamUpdated: document.getElementById("teamUpdated"),
  resourceUpdated: document.getElementById("resourceUpdated"),
  scheduleUpdated: document.getElementById("scheduleUpdated"),
  deadlineUpdated: document.getElementById("deadlineUpdated"),
  orgUpdated: document.getElementById("orgUpdated"),
  refreshState: document.getElementById("refreshState"),
};

const tokenKey = "agent-team.daemonToken";
const autoRefreshKey = "agent-team.autoRefresh";
const refreshIntervalMs = 5000;
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

function isResourceURI(value) {
  return typeof value === "string" && value.startsWith("agt://");
}

function addResourceURI(out, value) {
  if (isResourceURI(value)) {
    out.add(value);
  }
}

function collectResourceURIs(instances, jobs) {
  const out = new Set();
  const keys = [
    "uri",
    "URI",
    "deployment_uri",
    "DeploymentURI",
    "job_uri",
    "JobURI",
    "instance_uri",
    "InstanceURI",
    "workspace_uri",
    "WorkspaceURI",
    "state_uri",
    "StateURI",
    "log_uri",
    "LogURI",
  ];
  for (const row of [...instances, ...jobs]) {
    for (const key of keys) {
      addResourceURI(out, row && row[key]);
    }
  }
  return [...out].sort();
}

async function loadResource(uri) {
  try {
    return { uri, ok: true, data: await getJSON(`/v1/resources?uri=${encodeURIComponent(uri)}`) };
  } catch (error) {
    return { uri, ok: false, error };
  }
}

async function loadResources(instances, jobs) {
  const uris = collectResourceURIs(instances, jobs);
  if (!uris.length) {
    return { ok: true, data: new Map(), failures: [], requested: 0 };
  }
  const loaded = await Promise.all(uris.map(loadResource));
  const data = new Map();
  const failures = [];
  for (const result of loaded) {
    if (result.ok) {
      data.set(result.uri, result.data);
    } else {
      failures.push(result);
    }
  }
  return { ok: failures.length === 0, data, failures, requested: uris.length };
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

function formatDateTime(value) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return text(value);
  }
  return date.toLocaleString();
}

function shortURI(value) {
  const raw = text(value, "");
  if (!raw) {
    return "-";
  }
  const match = raw.match(/^agt:\/\/([^/]+)\/([^/]+)\/([^#]+)(#.+)?$/);
  if (!match) {
    return raw;
  }
  const id = decodeURIComponent(match[3]);
  return match[4] ? `${match[2]}/${id}${match[4]}` : `${match[2]}/${id}`;
}

function resourceEnvelope(resources, uri) {
  if (!resources || !resources.data || !isResourceURI(uri)) {
    return null;
  }
  return resources.data.get(uri) || null;
}

function resourceData(resources, uri) {
  const envelope = resourceEnvelope(resources, uri);
  return envelope && envelope.data && typeof envelope.data === "object" ? envelope.data : {};
}

function payloadSummary(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return text(value);
  }
  const entries = Object.entries(value);
  if (!entries.length) {
    return "-";
  }
  return entries
    .slice(0, 3)
    .map(([key, entry]) => `${key}=${Array.isArray(entry) ? entry.join("|") : text(entry)}`)
    .join(" ");
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
  if (["running", "working", "done", "connected", "active", "healthy"].includes(normalized)) {
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
  if (["idle", "exited", "stopped", "observed"].includes(normalized)) {
    return "neutral";
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

function firstField(sources, ...names) {
  for (const source of sources) {
    const value = field(source, ...names);
    if (value !== "") {
      return value;
    }
  }
  return "";
}

function tierForModel(model) {
  switch (text(model, "").toLowerCase()) {
    case "claude-fable-5":
      return "T0";
    case "claude-opus-4-8":
      return "T1";
    case "claude-sonnet-5":
      return "T2";
    case "claude-haiku-4-5":
      return "T3";
    default:
      return "";
  }
}

function tierForUnit(target, agent, runtime) {
  const haystack = `${text(target, "")} ${text(agent, "")}`.toLowerCase();
  if (haystack.includes("advisor") || haystack.includes("org-review")) {
    return "T0";
  }
  if (haystack.includes("reviewer") || haystack.includes("manager") || haystack.includes("harness-review")) {
    return "T1";
  }
  if (haystack.includes("verifier") || haystack.includes("ticket-manager") || haystack.includes("sentinel") || haystack.includes("product-verify")) {
    return "T3";
  }
  if (haystack.includes("worker") || haystack.includes("docs") || haystack.includes("comms") || haystack.includes("feedback") || haystack.includes("auditor")) {
    return "T2";
  }
  if (text(runtime, "").toLowerCase() === "codex") {
    return "T2";
  }
  return "";
}

function modelForRuntime(runtime) {
  return text(runtime, "");
}

function modelTierFromSources(sources, target, agent) {
  const runtime = firstField(sources, "effective_runtime", "EffectiveRuntime", "runtime", "Runtime");
  let model = firstField(sources, "model", "Model");
  let tier = firstField(sources, "tier", "Tier", "model_tier", "ModelTier");
  if (!tier) {
    tier = tierForModel(model);
  }
  if (!model) {
    model = modelForRuntime(runtime);
  }
  if (!tier) {
    tier = tierForUnit(target, agent, runtime);
  }
  return {
    runtime: text(runtime, ""),
    model: text(model, ""),
    tier: text(tier, ""),
  };
}

function modelTierLabel(telemetry, fallback = "not reported") {
  const model = text(telemetry && telemetry.model, "");
  const tier = text(telemetry && telemetry.tier, "");
  if (model && tier) {
    return `${model} / ${tier}`;
  }
  return model || tier || fallback;
}

function modelTierNode(telemetry) {
  const wrap = document.createElement("div");
  const label = modelTierLabel(telemetry);
  wrap.className = "telemetry-stack";
  wrap.appendChild(pill(label, label === "not reported" ? "neutral" : "info"));
  if (telemetry && telemetry.runtime && telemetry.runtime !== telemetry.model) {
    const runtime = document.createElement("span");
    runtime.textContent = `runtime ${telemetry.runtime}`;
    wrap.appendChild(runtime);
  }
  return wrap;
}

function declaredForInstance(instance, declaredInstances) {
  const declared = asArray(declaredInstances);
  const declaredNames = declared.map((item) => field(item, "name", "Name")).filter(Boolean);
  const name = declaredInstanceName(instance, declaredNames);
  return declared.find((item) => field(item, "name", "Name") === name) || null;
}

function instanceTelemetry(resources, instance, declaredInstances = []) {
  const data = resourceData(resources, field(instance, "uri", "URI", "instance_uri", "InstanceURI"));
  const declared = declaredForInstance(instance, declaredInstances);
  const agent = firstField([data, instance, declared], "agent", "Agent");
  const target = firstField([instance, declared], "instance", "Instance", "name", "Name") || agent;
  return modelTierFromSources([data, instance, declared], target, agent);
}

function jobResourceData(resources, job) {
  return resourceData(resources, field(job, "uri", "URI", "job_uri", "JobURI"));
}

function matchingJobInstance(job, step, instances) {
  const names = [
    field(step, "instance", "Instance"),
    field(job, "instance", "Instance"),
  ].filter(Boolean);
  for (const name of names) {
    const match = instances.find((instance) => field(instance, "instance", "Instance") === name);
    if (match) {
      return match;
    }
  }
  const jobID = field(job, "id", "ID");
  return instances.find((instance) => field(instance, "job", "Job") === jobID) || null;
}

function stepProgressed(step) {
  if (!step || typeof step !== "object") {
    return false;
  }
  if (field(step, "attempts", "Attempts") || field(step, "instance", "Instance")) {
    return true;
  }
  if (field(step, "running_at", "RunningAt") || field(step, "started_at", "StartedAt") || field(step, "finished_at", "FinishedAt")) {
    return true;
  }
  return ["running", "done", "failed"].includes(text(field(step, "status", "Status")).toLowerCase());
}

function primaryJobStep(job, data) {
  const steps = asArray(field(data, "steps", "Steps"));
  if (!steps.length) {
    return null;
  }
  const primary = text(field(job, "implementation_agent", "ImplementationAgent", "target", "Target"), "").toLowerCase();
  return (
    steps.find((step) => text(field(step, "id", "ID")).toLowerCase() === "implement") ||
    steps.find((step) => primary && text(field(step, "target", "Target", "agent", "Agent")).toLowerCase() === primary) ||
    steps.find(stepProgressed) ||
    steps[0]
  );
}

function jobTelemetry(job, resources, instances) {
  const data = jobResourceData(resources, job);
  const step = primaryJobStep(job, data);
  const instance = matchingJobInstance(job, step, instances) || {};
  const instanceData = resourceData(resources, field(instance, "uri", "URI", "instance_uri", "InstanceURI"));
  const target = firstField([step, job, data, instance], "target", "Target", "implementation_agent", "ImplementationAgent", "agent", "Agent");
  const agent = firstField([instanceData, instance, step, job, data], "agent", "Agent", "target", "Target");
  return modelTierFromSources([data, job, step, instanceData, instance], target, agent);
}

function addCount(map, key, count = 1) {
  key = text(key, "");
  if (!key || count <= 0) {
    return;
  }
  map.set(key, (map.get(key) || 0) + count);
}

function countMapFromValue(value) {
  const out = new Map();
  if (!value) {
    return out;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      if (typeof item === "string") {
        addCount(out, item);
      } else if (item && typeof item === "object") {
        for (const className of asArray(field(item, "classes", "Classes"))) {
          addCount(out, className);
        }
      }
    }
    return out;
  }
  if (typeof value === "object") {
    for (const [key, count] of Object.entries(value)) {
      addCount(out, key, Number(count) || 0);
    }
  }
  return out;
}

function explicitBounceClasses(lower) {
  const known = ["capability", "spec-ambiguity", "scope", "infra"];
  const out = [];
  for (const line of lower.split("\n")) {
    const clean = line.trim();
    if (!clean.includes("class")) {
      continue;
    }
    for (const className of known) {
      if (clean.includes(className) || (className === "spec-ambiguity" && clean.includes("spec ambiguity"))) {
        if (!out.includes(className)) {
          out.push(className);
        }
      }
    }
  }
  return out;
}

function classifyBounce(body) {
  const lower = text(body, "").toLowerCase();
  const explicit = explicitBounceClasses(lower);
  if (explicit.length) {
    return explicit;
  }
  const rules = [
    ["infra", ["infra", "flake", "flaky", "timeout", "rate limit", "credential", "auth", "network", "no space", "ci unavailable", "base drift", "runner", "environment"]],
    ["spec-ambiguity", ["spec ambiguity", "spec-ambiguity", "ambiguous", "ambiguity", "intent", "clarify", "not what was meant", "underspecified", "under-specified", "vague", "question"]],
    ["scope", ["scope", "sprawl", "drive-by", "unrelated", "oversized", "split the ticket", "split ticket", "multiple concerns", "too broad", "out of scope", "owned path"]],
    ["capability", ["capability", "logic error", "edge case", "misapplied", "missed", "missing test", "shallow test", "didn't understand", "did not understand", "incorrect", "wrong", "bug", "regression", "behavior", "requirement", "acceptance", "failed to", "doesn't", "does not"]],
  ];
  const classes = [];
  for (const [className, keys] of rules) {
    if (keys.some((key) => lower.includes(key))) {
      classes.push(className);
    }
  }
  return classes.length ? classes : ["unknown"];
}

function parseBounceClasses(kickoff) {
  const out = new Map();
  const raw = text(kickoff, "");
  if (!raw) {
    return out;
  }
  const matches = [...raw.matchAll(/^## Review findings \(bounce ([0-9]+)\)\s*$/gim)];
  for (let i = 0; i < matches.length; i++) {
    const start = matches[i].index + matches[i][0].length;
    const end = i + 1 < matches.length ? matches[i + 1].index : raw.length;
    for (const className of classifyBounce(raw.slice(start, end))) {
      addCount(out, className);
    }
  }
  return out;
}

function bounceClassesForJob(job, resources) {
  const data = jobResourceData(resources, job);
  const explicit = countMapFromValue(firstField([data, job], "bounce_classes", "BounceClasses", "bounceClasses"));
  if (explicit.size) {
    return explicit;
  }
  const bounces = countMapFromValue(firstField([data, job], "bounces", "Bounces"));
  if (bounces.size) {
    return bounces;
  }
  return parseBounceClasses(field(data, "kickoff", "Kickoff"));
}

function countMapTotal(map) {
  let total = 0;
  for (const count of map.values()) {
    total += count;
  }
  return total;
}

function countEntries(map) {
  return [...map.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function countChipList(map, empty = "none") {
  const wrap = document.createElement("div");
  wrap.className = "tag-list";
  const entries = countEntries(map);
  if (!entries.length) {
    wrap.textContent = empty;
    return wrap;
  }
  for (const [key, count] of entries) {
    const chip = document.createElement("span");
    chip.className = "tag-chip";
    chip.textContent = `${key} ${count}`;
    wrap.appendChild(chip);
  }
  return wrap;
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

function instanceIdentity(instance) {
  return field(instance, "uri", "URI", "instance_uri", "InstanceURI") || field(instance, "instance", "Instance");
}

function timestampValue(value) {
  if (!value) {
    return 0;
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 0 : date.getTime();
}

function instanceActivityTime(instance) {
  return Math.max(
    timestampValue(field(instance, "started_at", "StartedAt")),
    timestampValue(field(instance, "stopped_at", "StoppedAt")),
    timestampValue(field(instance, "exited_at", "ExitedAt"))
  );
}

function dedupeInstances(instances) {
  const byID = new Map();
  for (const instance of instances) {
    const id = instanceIdentity(instance);
    if (!id) {
      continue;
    }
    const existing = byID.get(id);
    if (!existing || instanceActivityTime(instance) >= instanceActivityTime(existing)) {
      byID.set(id, instance);
    }
  }
  return [...byID.values()];
}

function declaredInstanceName(runtime, declaredNames) {
  const name = field(runtime, "instance", "Instance");
  if (!name) {
    return "";
  }
  if (declaredNames.includes(name)) {
    return name;
  }
  let best = "";
  for (const declared of declaredNames) {
    if (name.startsWith(`${declared}-`) && declared.length > best.length) {
      best = declared;
    }
  }
  return best;
}

function stateStatus(resources, instance) {
  const data = resourceData(resources, field(instance, "state_uri", "StateURI"));
  return data && data.status && typeof data.status === "object" ? data.status : {};
}

function instancePhase(resources, instance) {
  return field(stateStatus(resources, instance), "phase", "Phase") || field(instance, "phase", "Phase");
}

function instanceDescription(resources, instance) {
  return field(stateStatus(resources, instance), "description", "Description", "last_action", "LastAction");
}

function instanceLifecycle(instance) {
  return text(field(instance, "status", "Status", "lifecycle", "Lifecycle"), "unknown").toLowerCase();
}

function activeRuntimeInstance(instance) {
  return instanceLifecycle(instance) === "running";
}

function runningPhase(phase) {
  const normalized = slug(phase || "");
  return Boolean(normalized && !["idle", "done"].includes(normalized));
}

function runningInstanceWorking(resources, instance) {
  if (instanceLifecycle(instance) !== "running") {
    return false;
  }
  const phase = instancePhase(resources, instance);
  return phase ? runningPhase(phase) : true;
}

function laneScheduleMatches(declared, schedule) {
  const triggers = asArray(field(declared, "triggers", "Triggers"));
  const scheduleName = field(schedule, "name", "Name");
  const scheduleKind = field(field(schedule, "payload", "Payload"), "kind", "Kind");
  return triggers.some((trigger) => {
    if (field(trigger, "event", "Event") !== "schedule") {
      return false;
    }
    const match = field(trigger, "match", "Match");
    return field(match, "name", "Name") === scheduleName || field(match, "kind", "Kind") === scheduleKind;
  });
}

function laneSchedules(declared, schedules) {
  return schedules.filter((schedule) => laneScheduleMatches(declared, schedule));
}

function laneCapacity(declared) {
  const replicas = field(declared, "replicas", "Replicas");
  const running = field(declared, "running", "Running");
  const queued = field(declared, "queued", "Queued");
  const parts = [];
  if (replicas) {
    parts.push(`${running || 0}/${replicas} running`);
  } else if (running) {
    parts.push(`${running} running`);
  }
  if (queued) {
    parts.push(`${queued} queued`);
  }
  return parts.join(" / ");
}

function laneScheduleSummary(schedules) {
  if (!schedules.length) {
    return "";
  }
  return schedules
    .slice(0, 2)
    .map((schedule) => `${field(schedule, "name", "Name")} ${field(schedule, "every", "Every") || "scheduled"}`)
    .join(" / ");
}

function createLane(name, agent, declared = null) {
  return {
    name,
    agent,
    declared,
    active: [],
    latest: null,
    schedules: [],
  };
}

function latestInstance(a, b) {
  if (!a) {
    return b;
  }
  if (!b) {
    return a;
  }
  return instanceActivityTime(b) > instanceActivityTime(a) ? b : a;
}

function orgLanes(instances, declaredInstances, schedules) {
  const declaredNames = declaredInstances.map((instance) => field(instance, "name", "Name")).filter(Boolean);
  const lanes = new Map();
  for (const declared of declaredInstances) {
    const name = field(declared, "name", "Name");
    if (!name) {
      continue;
    }
    const lane = createLane(name, field(declared, "agent", "Agent") || "unknown", declared);
    lane.schedules = laneSchedules(declared, schedules);
    lanes.set(name, lane);
  }

  for (const instance of dedupeInstances(instances)) {
    const name = field(instance, "instance", "Instance");
    const declaredName = declaredInstanceName(instance, declaredNames);
    const laneName = declaredName || name;
    if (!laneName) {
      continue;
    }
    let lane = lanes.get(laneName);
    if (!lane) {
      lane = createLane(laneName, field(instance, "agent", "Agent") || "unknown");
      lanes.set(laneName, lane);
    }
    lane.agent ||= field(instance, "agent", "Agent") || "unknown";
    if (activeRuntimeInstance(instance)) {
      lane.active.push(instance);
    } else {
      lane.latest = latestInstance(lane.latest, instance);
    }
  }

  return [...lanes.values()].sort((a, b) => a.agent.localeCompare(b.agent) || a.name.localeCompare(b.name));
}

function laneVisibleInstances(lane) {
  if (lane.active.length) {
    return lane.active.sort((a, b) => instanceActivityTime(b) - instanceActivityTime(a));
  }
  return lane.latest ? [lane.latest] : [];
}

function laneState(lane, resources) {
  const visible = laneVisibleInstances(lane);
  if (visible.some((instance) => instanceLifecycle(instance) === "crashed")) {
    return "crashed";
  }
  if (visible.some((instance) => instanceLifecycle(instance) === "running")) {
    return visible.some((instance) => runningInstanceWorking(resources, instance)) ? "working" : "idle";
  }
  const queued = Number(field(lane.declared, "queued", "Queued"));
  if (Number.isFinite(queued) && queued > 0) {
    return "queued";
  }
  return "idle";
}

function roleTitle(role) {
  const value = text(role, "unknown");
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function laneMeta(lane) {
  const parts = [];
  const capacity = laneCapacity(lane.declared);
  const schedule = laneScheduleSummary(lane.schedules);
  if (capacity) {
    parts.push(capacity);
  }
  if (schedule) {
    parts.push(schedule);
  }
  if (field(lane.declared, "ephemeral", "Ephemeral") === false) {
    parts.push("persistent");
  }
  return parts.join(" / ");
}

function instanceDetail(resources, instance) {
  const bits = [];
  const job = field(instance, "job", "Job");
  const ticket = field(instance, "ticket", "Ticket");
  const description = instanceDescription(resources, instance);
  if (job) {
    bits.push(job);
  }
  if (ticket && ticket !== job) {
    bits.push(ticket);
  }
  if (description) {
    bits.push(description);
  }
  return bits.join(" / ") || "no active job";
}

function renderOrgInstance(line, resources, instance, declared = null) {
  const name = field(instance, "instance", "Instance");
  const lifecycle = instanceLifecycle(instance);
  const phase = instancePhase(resources, instance);
  const telemetry = instanceTelemetry(resources, instance, declared ? [declared] : []);
  const row = document.createElement("div");
  const copy = document.createElement("div");
  const title = document.createElement("strong");
  const detail = document.createElement("span");
  const pills = document.createElement("div");
  row.className = `org-instance state-${slug(lifecycle)}`;
  copy.className = "org-instance-copy";
  title.textContent = name;
  detail.textContent = instanceDetail(resources, instance);
  copy.append(title, detail);
  pills.className = "org-instance-state";
  if (telemetry.model || telemetry.tier || telemetry.runtime) {
    pills.appendChild(pill(modelTierLabel(telemetry), "info"));
  }
  if (phase) {
    pills.appendChild(pill(phase));
  }
  pills.appendChild(statusPill(lifecycle));
  row.append(copy, pills);
  line.appendChild(row);
}

function renderOrgLane(group, lane, resources) {
  const row = document.createElement("div");
  const main = document.createElement("div");
  const title = document.createElement("div");
  const name = document.createElement("strong");
  const meta = document.createElement("span");
  const state = document.createElement("div");
  const details = document.createElement("div");
  const visible = laneVisibleInstances(lane);
  const stateName = laneState(lane, resources);
  row.className = `org-row state-${slug(stateName)}`;
  main.className = "org-row-main";
  title.className = "org-lane-title";
  name.textContent = lane.name;
  meta.textContent = laneMeta(lane) || "no declared activity";
  title.append(name, meta);
  state.className = "org-row-state";
  state.appendChild(pill(stateName));
  main.append(title, state);
  details.className = "org-row-details";
  if (visible.length) {
    for (const instance of visible.slice(0, 4)) {
      renderOrgInstance(details, resources, instance, lane.declared);
    }
    if (visible.length > 4) {
      const more = document.createElement("span");
      more.className = "org-more";
      more.textContent = `+${visible.length - 4} more`;
      details.appendChild(more);
    }
  } else {
    const idle = document.createElement("span");
    idle.className = "org-idle";
    idle.textContent = "idle";
    details.appendChild(idle);
  }
  row.append(main, details);
  group.appendChild(row);
}

function renderOrg(result, topologyResult, resources, instances, declaredInstances, schedules) {
  els.orgView.replaceChildren();
  if (!result.ok && !topologyResult.ok) {
    const empty = document.createElement("div");
    empty.className = "org-empty";
    empty.textContent = `Org view unavailable. ${humanError(result.error)} ${humanError(topologyResult.error)}`;
    els.orgView.appendChild(empty);
    return;
  }
  const lanes = orgLanes(instances, declaredInstances, schedules);
  if (!lanes.length) {
    const empty = document.createElement("div");
    empty.className = "org-empty";
    empty.textContent = "No declared or runtime instances reported.";
    els.orgView.appendChild(empty);
    return;
  }
  const byRole = new Map();
  for (const lane of lanes) {
    const role = lane.agent || "unknown";
    if (!byRole.has(role)) {
      byRole.set(role, []);
    }
    byRole.get(role).push(lane);
  }
  for (const [role, roleLanes] of byRole.entries()) {
    const section = document.createElement("section");
    const heading = document.createElement("div");
    const title = document.createElement("h3");
    const counts = document.createElement("div");
    const body = document.createElement("div");
    const states = roleLanes.reduce(
      (acc, lane) => {
        const state = laneState(lane, resources);
        acc[state] = (acc[state] || 0) + 1;
        return acc;
      },
      { working: 0, idle: 0, queued: 0, crashed: 0 }
    );
    section.className = "org-role";
    heading.className = "org-role-heading";
    title.textContent = roleTitle(role);
    counts.className = "org-role-counts";
    counts.append(
      pill(`${states.working || 0} working`, states.working ? "positive" : "neutral"),
      pill(`${states.idle || 0} idle`, "neutral")
    );
    if (states.queued) {
      counts.appendChild(pill(`${states.queued} queued`, "info"));
    }
    if (states.crashed) {
      counts.appendChild(pill(`${states.crashed} crashed`, "negative"));
    }
    heading.append(title, counts);
    body.className = "org-role-body";
    for (const lane of roleLanes) {
      renderOrgLane(body, lane, resources);
    }
    section.append(heading, body);
    els.orgView.appendChild(section);
  }
}

function renderInstances(result, instances, resources, declaredInstances) {
  els.instancesBody.replaceChildren();
  if (!result.ok) {
    els.instancesBody.appendChild(errorRow(5, "Instances unavailable", result.error));
    return;
  }
  if (!instances.length) {
    els.instancesBody.appendChild(emptyRow(5, "No instances reported", "Start an instance to populate daemon lifecycle rows."));
    return;
  }
  for (const instance of instances) {
    const telemetry = instanceTelemetry(resources, instance, declaredInstances);
    const tr = document.createElement("tr");
    tr.append(
      cell(field(instance, "instance", "Instance")),
      cell(field(instance, "agent", "Agent")),
      cell(statusPill(field(instance, "status", "Status", "lifecycle"))),
      cell(modelTierNode(telemetry)),
      cell(field(instance, "job", "Job"))
    );
    els.instancesBody.appendChild(tr);
  }
}

function renderJobs(result, jobs, resources, instances) {
  els.jobsBody.replaceChildren();
  if (!result.ok) {
    els.jobsBody.appendChild(errorRow(7, "Jobs unavailable", result.error));
    return;
  }
  if (!jobs.length) {
    els.jobsBody.appendChild(emptyRow(7, "No jobs recorded", "Dispatch a durable job to see ticket, pipeline, and instance state here."));
    return;
  }
  for (const job of jobs) {
    const telemetry = jobTelemetry(job, resources, instances);
    const bounces = bounceClassesForJob(job, resources);
    const tr = document.createElement("tr");
    tr.append(
      cell(field(job, "id", "ID")),
      cell(field(job, "ticket", "Ticket")),
      cell(statusPill(field(job, "status", "Status"))),
      cell(field(job, "pipeline", "Pipeline")),
      cell(modelTierNode(telemetry)),
      cell(countChipList(bounces, "none")),
      cell(field(job, "instance", "Instance"))
    );
    els.jobsBody.appendChild(tr);
  }
}

function recentJobs(jobs) {
  return [...jobs].sort((a, b) => {
    const bTime = timestampValue(field(b, "updated_at", "UpdatedAt", "created_at", "CreatedAt"));
    const aTime = timestampValue(field(a, "updated_at", "UpdatedAt", "created_at", "CreatedAt"));
    return bTime - aTime || text(field(a, "id", "ID")).localeCompare(text(field(b, "id", "ID")));
  });
}

function telemetryAggregates(jobs, resources, instances) {
  const byModelTier = new Map();
  const byClass = new Map();
  for (const job of recentJobs(jobs).slice(0, 24)) {
    const telemetry = jobTelemetry(job, resources, instances);
    const modelTier = modelTierLabel(telemetry, "not reported");
    const bounces = bounceClassesForJob(job, resources);
    const bounceTotal = countMapTotal(bounces);
    if (!byModelTier.has(modelTier)) {
      byModelTier.set(modelTier, { jobs: 0, active: 0, bounces: 0 });
    }
    const modelRow = byModelTier.get(modelTier);
    modelRow.jobs++;
    if (activeJob(job)) {
      modelRow.active++;
    }
    modelRow.bounces += bounceTotal;
    for (const [className, count] of bounces.entries()) {
      if (!byClass.has(className)) {
        byClass.set(className, { bounces: 0, jobs: 0 });
      }
      const classRow = byClass.get(className);
      classRow.bounces += count;
      classRow.jobs++;
    }
  }
  return { byModelTier, byClass };
}

function renderTelemetry(result, jobs, resources, instances) {
  els.modelTierBody.replaceChildren();
  els.bounceClassBody.replaceChildren();
  if (!result.ok) {
    els.modelTierBody.appendChild(errorRow(4, "Telemetry unavailable", result.error));
    els.bounceClassBody.appendChild(errorRow(3, "Bounce classes unavailable", result.error));
    return;
  }
  if (!jobs.length) {
    els.modelTierBody.appendChild(emptyRow(4, "No jobs recorded", "Model-tier telemetry appears once jobs are present."));
    els.bounceClassBody.appendChild(emptyRow(3, "No jobs recorded", "Bounce classes appear after review-bounce data is reported."));
    return;
  }
  const { byModelTier, byClass } = telemetryAggregates(jobs, resources, instances);
  const modelRows = [...byModelTier.entries()].sort((a, b) => b[1].jobs - a[1].jobs || a[0].localeCompare(b[0]));
  if (!modelRows.length) {
    els.modelTierBody.appendChild(emptyRow(4, "No model tiers reported", "Runtime and model fields are not present on the loaded jobs."));
  } else {
    for (const [label, row] of modelRows) {
      const tr = document.createElement("tr");
      tr.append(
        cell(pill(label, label === "not reported" ? "neutral" : "info")),
        cell(row.jobs),
        cell(row.active ? pill(`${row.active} active`, "positive") : pill("idle", "neutral")),
        cell(row.bounces || "-")
      );
      els.modelTierBody.appendChild(tr);
    }
  }
  const classRows = [...byClass.entries()].sort((a, b) => b[1].bounces - a[1].bounces || a[0].localeCompare(b[0]));
  if (!classRows.length) {
    els.bounceClassBody.appendChild(emptyRow(3, "No bounce classes reported", "Recent jobs have no classified review bounces."));
  } else {
    for (const [className, row] of classRows) {
      const tr = document.createElement("tr");
      tr.append(
        cell(pill(className, className === "unknown" ? "neutral" : "warning")),
        cell(row.bounces),
        cell(row.jobs)
      );
      els.bounceClassBody.appendChild(tr);
    }
  }
}

function deploymentID(uri) {
  const raw = text(uri, "");
  const match = raw.match(/^agt:\/\/([^/]+)\//);
  return match ? match[1] : raw;
}

function ensureDeployment(rows, uri) {
  const key = text(uri, "");
  if (!key) {
    return null;
  }
  if (!rows.has(key)) {
    rows.set(key, {
      uri: key,
      id: deploymentID(key),
      parent: "",
      instances: new Set(),
      jobs: new Set(),
      statuses: new Set(),
      charterURI: "",
      charterStatus: "",
      relationship: "",
      ready: false,
    });
  }
  return rows.get(key);
}

function absorbCharter(row, source) {
  if (!row || !source || typeof source !== "object") {
    return;
  }
  row.charterURI ||= field(source, "charter_uri", "CharterURI");
  row.charterStatus ||= field(source, "state", "State", "charter_status", "CharterStatus", "charter_state", "CharterState");
  row.relationship ||= field(source, "relationship", "Relationship");
}

function deploymentRows(instances, jobs, resources) {
  const rows = new Map();
  for (const instance of instances) {
    const uri = field(instance, "deployment_uri", "DeploymentURI");
    const row = ensureDeployment(rows, uri);
    if (!row) {
      continue;
    }
    row.parent ||= field(instance, "deployment_parent_uri", "DeploymentParentURI");
    row.instances.add(field(instance, "instance", "Instance"));
    row.statuses.add(field(instance, "status", "Status"));
    absorbCharter(row, instance);
    absorbCharter(row, resourceData(resources, field(instance, "uri", "URI")));
  }
  for (const job of jobs) {
    const uri = field(job, "deployment_uri", "DeploymentURI");
    const row = ensureDeployment(rows, uri);
    if (!row) {
      continue;
    }
    row.parent ||= field(job, "deployment_parent_uri", "DeploymentParentURI");
    row.jobs.add(field(job, "id", "ID"));
    row.statuses.add(field(job, "status", "Status"));
    absorbCharter(row, job);
    absorbCharter(row, resourceData(resources, field(job, "uri", "URI", "job_uri", "JobURI")));
  }
  if (resources && resources.data) {
    for (const envelope of resources.data.values()) {
      if (!envelope || envelope.kind !== "project") {
        continue;
      }
      const data = envelope.data || {};
      const row = ensureDeployment(rows, field(data, "uri", "URI", "deployment_uri", "DeploymentURI", "parent_uri", "ParentURI") || envelope.uri);
      if (!row) {
        continue;
      }
      row.id = field(data, "id", "ID") || row.id;
      row.parent ||= field(data, "parent_uri", "ParentURI");
      row.ready = Boolean(field(data, "ready", "Ready"));
      absorbCharter(row, data);
    }
  }
  return [...rows.values()].sort((a, b) => a.id.localeCompare(b.id));
}

function countParts(row) {
  const parts = [];
  if (row.instances.size) {
    parts.push(`${row.instances.size} ${row.instances.size === 1 ? "instance" : "instances"}`);
  }
  if (row.jobs.size) {
    parts.push(`${row.jobs.size} ${row.jobs.size === 1 ? "job" : "jobs"}`);
  }
  return parts.join(" / ") || "-";
}

function deploymentStatus(row) {
  if (row.charterStatus) {
    return row.charterStatus;
  }
  if ([...row.statuses].some((status) => text(status).toLowerCase() === "running")) {
    return "running";
  }
  if (row.ready) {
    return "ready";
  }
  return row.statuses.size ? [...row.statuses].filter(Boolean).join(", ") : "observed";
}

function renderResources(result, instances, jobs) {
  els.resourcesBody.replaceChildren();
  const rows = deploymentRows(instances, jobs, result);
  if (!rows.length && result && !result.ok) {
    els.resourcesBody.appendChild(errorRow(5, "Resources unavailable", result.failures.map((failure) => humanError(failure.error)).join(" | ")));
    return;
  }
  if (!rows.length) {
    els.resourcesBody.appendChild(emptyRow(5, "No deployment resources reported", "Resource URIs appear here once instances or jobs include deployment metadata."));
    return;
  }
  for (const row of rows) {
    const charter = row.charterURI ? `${shortURI(row.charterURI)}${row.charterStatus ? ` (${row.charterStatus})` : ""}` : "not reported";
    const tr = document.createElement("tr");
    tr.append(
      cell(shortURI(row.uri)),
      cell(row.parent ? shortURI(row.parent) : "root"),
      cell(countParts(row)),
      cell(charter),
      cell(statusPill(deploymentStatus(row)))
    );
    els.resourcesBody.appendChild(tr);
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
    els.budgetsBody.appendChild(errorRow(6, "Budgets unavailable", result.error));
    return;
  }
  if (!budgets.length) {
    els.budgetsBody.appendChild(emptyRow(6, "No budgets configured", "Declare team budgets to track tokens, job caps, and allocation mode."));
    return;
  }
  const byPipeline = pipelineTeamMap(teams);
  for (const budget of budgets) {
    const team = field(budget, "team", "Team");
    const active = jobs.filter((job) => activeJob(job) && byPipeline.get(field(job, "pipeline", "Pipeline")) === team).length;
    const capRaw = field(budget, "jobs_in_flight", "JobsInFlight");
    const cap = Number(capRaw);
    const available = Number.isFinite(cap) && cap > 0 ? Math.max(cap - active, 0) : "";
    const tr = document.createElement("tr");
    tr.append(
      cell(team),
      cell(compactNumber(field(budget, "tokens_per_day", "TokensPerDay"))),
      cell(capRaw || "unbounded"),
      cell(pill(active ? `${active} active` : "idle", active ? "positive" : "neutral")),
      cell(available === "" ? "-" : pill(`${available} open`, available > 0 ? "positive" : "warning")),
      cell(pill(field(budget, "allocation", "Allocation") || "unconfigured", "info"))
    );
    els.budgetsBody.appendChild(tr);
  }
}

function renderSchedules(result, schedules) {
  els.schedulesBody.replaceChildren();
  if (!result.ok) {
    els.schedulesBody.appendChild(errorRow(5, "Schedules unavailable", result.error));
    return;
  }
  if (!schedules.length) {
    els.schedulesBody.appendChild(emptyRow(5, "No schedules declared", "Topology schedules appear here once a loop is configured."));
    return;
  }
  for (const schedule of schedules) {
    const lastFired = field(schedule, "last_fired_at", "LastFiredAt");
    const cadence = field(schedule, "every", "Every");
    const tr = document.createElement("tr");
    tr.append(
      cell(field(schedule, "name", "Name")),
      cell(cadence),
      cell(lastFired ? formatDateTime(lastFired) : pill(field(schedule, "run_on_start", "RunOnStart") ? "on start" : "pending", "neutral")),
      cell(field(schedule, "team", "Team") || "-"),
      cell(payloadSummary(field(schedule, "payload", "Payload")))
    );
    els.schedulesBody.appendChild(tr);
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

function deadlineValue(source) {
  if (!source || typeof source !== "object") {
    return "";
  }
  const durable = field(source, "deadline", "Deadline");
  const runtime = field(source, "runtime_deadline", "RuntimeDeadline");
  return durable || runtime;
}

function deadlineIdentity(kind, source, fallbackURI, fallbackID) {
  const uri = field(source, "uri", "URI", "job_uri", "JobURI", "instance_uri", "InstanceURI") || fallbackURI;
  if (uri) {
    return uri;
  }
  const id = field(source, "id", "ID", "instance", "Instance", "name", "Name") || fallbackID;
  return id ? `${kind}:${id}` : "";
}

function rememberDeadlineResource(seen, envelope, fallbackURI) {
  if (fallbackURI) {
    seen.add(fallbackURI);
  }
  if (!envelope) {
    return;
  }
  if (envelope.uri) {
    seen.add(envelope.uri);
  }
  const data = envelope.data && typeof envelope.data === "object" ? envelope.data : {};
  const dataURI = field(data, "uri", "URI", "job_uri", "JobURI", "instance_uri", "InstanceURI");
  if (dataURI) {
    seen.add(dataURI);
  }
}

function deadlineResourceSeen(seen, envelope) {
  if (!envelope) {
    return false;
  }
  if (envelope.uri && seen.has(envelope.uri)) {
    return true;
  }
  const data = envelope.data && typeof envelope.data === "object" ? envelope.data : {};
  const dataURI = field(data, "uri", "URI", "job_uri", "JobURI", "instance_uri", "InstanceURI");
  return Boolean(dataURI && seen.has(dataURI));
}

function addDeadlineRow(rows, seen, identity, label, source, sourceLabel) {
  const value = deadlineValue(source);
  if (!value) {
    return;
  }
  const key = text(identity, "") || `${sourceLabel}:${text(label, "")}`;
  if (seen.has(key)) {
    return;
  }
  seen.add(key);
  const runtime = field(source, "runtime_deadline", "RuntimeDeadline");
  rows.push({
    label,
    deadline: value,
    state: field(source, "deadline_state", "DeadlineState") || (runtime ? "runtime" : "set"),
    source: field(source, "deadline_source", "DeadlineSource") || sourceLabel,
  });
}

function deadlineRows(instances, jobs, resources) {
  const rows = [];
  const seen = new Set();
  const representedResources = new Set();
  for (const job of jobs) {
    const label = field(job, "id", "ID");
    const uri = field(job, "uri", "URI", "job_uri", "JobURI");
    const envelope = resourceEnvelope(resources, uri);
    const data = envelope && envelope.data && typeof envelope.data === "object" ? envelope.data : {};
    rememberDeadlineResource(representedResources, envelope, uri);
    const source = deadlineValue(data) ? data : job;
    const sourceLabel = source === data ? "job resource" : "job";
    addDeadlineRow(rows, seen, deadlineIdentity("job", source, uri, label), label, source, sourceLabel);
  }
  for (const instance of instances) {
    const label = field(instance, "instance", "Instance");
    const uri = field(instance, "uri", "URI");
    const envelope = resourceEnvelope(resources, uri);
    const data = envelope && envelope.data && typeof envelope.data === "object" ? envelope.data : {};
    rememberDeadlineResource(representedResources, envelope, uri);
    const source = deadlineValue(data) ? data : instance;
    const sourceLabel = source === data ? "instance resource" : "runtime watchdog";
    addDeadlineRow(rows, seen, deadlineIdentity("instance", source, uri, label), label, source, sourceLabel);
  }
  if (resources && resources.data) {
    for (const envelope of resources.data.values()) {
      if (deadlineResourceSeen(representedResources, envelope)) {
        continue;
      }
      const data = envelope && envelope.data;
      const label = shortURI(envelope && envelope.uri);
      const kind = envelope && envelope.kind ? envelope.kind : "resource";
      addDeadlineRow(rows, seen, deadlineIdentity(kind, data, envelope && envelope.uri, envelope && envelope.id), label, data, `${kind} resource`);
    }
  }
  return rows.sort((a, b) => a.label.localeCompare(b.label));
}

function renderDeadlines(resources, instances, jobs) {
  els.deadlinesBody.replaceChildren();
  const rows = deadlineRows(instances, jobs, resources);
  if (!rows.length) {
    els.deadlinesBody.appendChild(emptyRow(4, "No durable deadlines reported", "Runtime watchdog deadlines and future delivery deadlines will appear here when present."));
    return;
  }
  for (const row of rows) {
    const tr = document.createElement("tr");
    tr.append(
      cell(row.label),
      cell(formatDateTime(row.deadline)),
      cell(statusPill(row.state)),
      cell(row.source)
    );
    els.deadlinesBody.appendChild(tr);
  }
}

function updateSummary(instances, jobs, topology, resources) {
  const pipelines = Array.isArray(topology.pipelines) ? topology.pipelines : [];
  const budgets = Array.isArray(topology.budgets) ? topology.budgets : [];
  const teams = Array.isArray(topology.teams) ? topology.teams : [];
  const schedules = Array.isArray(topology.schedules) ? topology.schedules : [];
  const telemetry = telemetryAggregates(jobs, resources, instances);
  els.instanceCount.textContent = instances.length;
  els.runningCount.textContent = instances.filter((row) => text(field(row, "status", "Status")).toLowerCase() === "running").length;
  els.jobCount.textContent = jobs.length;
  els.activeJobCount.textContent = jobs.filter(activeJob).length;
  els.modelTierCount.textContent = telemetry.byModelTier.size;
  els.bounceClassCount.textContent = telemetry.byClass.size;
  els.pipelineCount.textContent = pipelines.length;
  els.budgetTeamCount.textContent = budgets.length;
  els.teamCount.textContent = teams.length;
  els.scheduleCount.textContent = schedules.length;
  els.deploymentCount.textContent = deploymentRows(instances, jobs, resources).length;
  els.deadlineCount.textContent = deadlineRows(instances, jobs, resources).length;
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

function resourceFailureSummary(resources) {
  if (!resources || !resources.failures || !resources.failures.length) {
    return "";
  }
  return resources.failures.map((failure) => `${shortURI(failure.uri)}: ${humanError(failure.error)}`).join(" | ");
}

function updatePanelTimes(results, resources, stamp) {
  els.orgUpdated.textContent = results.instances.ok || results.topology.ok ? stamp : "unavailable";
  els.instanceUpdated.textContent = results.instances.ok ? stamp : "unavailable";
  els.jobUpdated.textContent = results.jobs.ok ? stamp : "unavailable";
  els.telemetryUpdated.textContent = results.jobs.ok && resources && (resources.ok || resources.requested > 0) ? stamp : "unavailable";
  els.pipelineUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.budgetUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.teamUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.resourceUpdated.textContent = resources && (resources.ok || resources.requested > 0) ? stamp : "unavailable";
  els.scheduleUpdated.textContent = results.topology.ok ? stamp : "unavailable";
  els.deadlineUpdated.textContent = resources && (resources.ok || resources.requested > 0) ? stamp : "unavailable";
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
    const safeSchedules = topologyArray(results.topology, "schedules");
    const safeDeclaredInstances = topologyArray(results.topology, "instances");
    const resources = await loadResources(safeInstances, safeJobs);

    renderOrg(results.instances, results.topology, resources, safeInstances, safeDeclaredInstances, safeSchedules);
    renderInstances(results.instances, safeInstances, resources, safeDeclaredInstances);
    renderJobs(results.jobs, safeJobs, resources, safeInstances);
    renderTelemetry(results.jobs, safeJobs, resources, safeInstances);
    renderResources(resources, safeInstances, safeJobs);
    renderPipelines(results.topology, safePipelines, safeJobs);
    renderBudgets(results.topology, safeBudgets, safeJobs, safeTeams);
    renderSchedules(results.topology, safeSchedules);
    renderDeadlines(resources, safeInstances, safeJobs);
    renderTeams(results.topology, safeTeams, safeJobs);
    updateSummary(safeInstances, safeJobs, safeTopology, resources);

    const stamp = new Date().toLocaleTimeString();
    const failures = loaded.filter((result) => !result.ok);
    const successes = loaded.length - failures.length;
    const resourceFailures = resourceFailureSummary(resources);
    updatePanelTimes(results, resources, stamp);
    if (!failures.length && !resourceFailures) {
      setConnection("connected", "Connected");
      setNotice(`Daemon data loaded at ${stamp}.`, "success");
    } else if (successes > 0 || (resources && resources.requested > resources.failures.length)) {
      setConnection("degraded", "Partial connection");
      const parts = [failureSummary(failures), resourceFailures && `resources: ${resourceFailures}`].filter(Boolean);
      setNotice(`Partial daemon data loaded at ${stamp}. ${parts.join(" | ")}`, "warning");
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
  els.refreshState.textContent = enabled ? "Every 5s" : "Manual";
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
