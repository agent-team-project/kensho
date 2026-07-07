const els = {
  tokenInput: document.getElementById("tokenInput"),
  saveToken: document.getElementById("saveToken"),
  refresh: document.getElementById("refresh"),
  notice: document.getElementById("notice"),
  instanceCount: document.getElementById("instanceCount"),
  runningCount: document.getElementById("runningCount"),
  jobCount: document.getElementById("jobCount"),
  activeJobCount: document.getElementById("activeJobCount"),
  instancesBody: document.getElementById("instancesBody"),
  jobsBody: document.getElementById("jobsBody"),
  instanceUpdated: document.getElementById("instanceUpdated"),
  jobUpdated: document.getElementById("jobUpdated"),
};

const tokenKey = "agent-team.daemonToken";

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
  const rendered = String(value || "").trim();
  return rendered || fallback;
}

function statusPill(value) {
  const label = text(value, "unknown");
  const span = document.createElement("span");
  span.className = `pill ${label.toLowerCase()}`;
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
    els.jobsBody.appendChild(emptyRow(4, "No jobs"));
    return;
  }
  for (const job of jobs) {
    const tr = document.createElement("tr");
    tr.append(
      cell(field(job, "id", "ID")),
      cell(field(job, "ticket", "Ticket")),
      cell(statusPill(field(job, "status", "Status"))),
      cell(field(job, "instance", "Instance"))
    );
    els.jobsBody.appendChild(tr);
  }
}

function updateSummary(instances, jobs) {
  els.instanceCount.textContent = instances.length;
  els.runningCount.textContent = instances.filter((row) => text(field(row, "status", "Status")).toLowerCase() === "running").length;
  els.jobCount.textContent = jobs.length;
  els.activeJobCount.textContent = jobs.filter((row) => {
    const status = text(field(row, "status", "Status")).toLowerCase();
    return status === "queued" || status === "running" || status === "blocked";
  }).length;
}

function setNotice(message, error = false) {
  els.notice.textContent = message;
  els.notice.classList.toggle("error", error);
}

async function refresh() {
  els.refresh.disabled = true;
  setNotice("Loading daemon data...");
  try {
    const [instances, jobs] = await Promise.all([
      getJSON("/v1/instances"),
      getJSON("/v1/jobs"),
    ]);
    const safeInstances = Array.isArray(instances) ? instances : [];
    const safeJobs = Array.isArray(jobs) ? jobs : [];
    renderInstances(safeInstances);
    renderJobs(safeJobs);
    updateSummary(safeInstances, safeJobs);
    const stamp = new Date().toLocaleTimeString();
    els.instanceUpdated.textContent = stamp;
    els.jobUpdated.textContent = stamp;
    setNotice("Daemon data loaded.");
  } catch (err) {
    setNotice(err.message, true);
  } finally {
    els.refresh.disabled = false;
  }
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

els.tokenInput.value = sessionStorage.getItem(tokenKey) || "";
refresh();
