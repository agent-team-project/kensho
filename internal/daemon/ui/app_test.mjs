import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const elementIDs = [
  "tokenInput",
  "saveToken",
  "refresh",
  "autoRefresh",
  "notice",
  "connectionState",
  "connectionText",
  "instanceCount",
  "runningCount",
  "jobCount",
  "activeJobCount",
  "modelTierCount",
  "bounceClassCount",
  "pipelineCount",
  "budgetTeamCount",
  "teamCount",
  "scheduleCount",
  "deploymentCount",
  "deadlineCount",
  "instancesBody",
  "jobsBody",
  "modelTierBody",
  "bounceClassBody",
  "pipelinesBody",
  "budgetsBody",
  "teamsBody",
  "resourcesBody",
  "schedulesBody",
  "deadlinesBody",
  "orgView",
  "instanceUpdated",
  "jobUpdated",
  "telemetryUpdated",
  "pipelineUpdated",
  "budgetUpdated",
  "teamUpdated",
  "resourceUpdated",
  "scheduleUpdated",
  "deadlineUpdated",
  "orgUpdated",
  "refreshState",
];

class TestElement {
  constructor(tagName = "div", id = "") {
    this.tagName = tagName.toUpperCase();
    this.id = id;
    this.children = [];
    this.attributes = new Map();
    this._text = "";
    this.className = "";
    this.value = "";
    this.checked = false;
    this.disabled = false;
    this.colSpan = 0;
    this.classList = {
      add: (...names) => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
        for (const name of names) {
          classes.add(name);
        }
        this.className = [...classes].join(" ");
      },
    };
  }

  set textContent(value) {
    this._text = value === undefined || value === null ? "" : String(value);
    this.children = [];
  }

  get textContent() {
    return [this._text, ...this.children.map((child) => child.textContent)].join("");
  }

  appendChild(child) {
    this.children.push(child);
    return child;
  }

  append(...items) {
    for (const item of items) {
      if (item instanceof TestElement) {
        this.children.push(item);
      } else {
        const child = new TestElement("#text");
        child.textContent = item;
        this.children.push(child);
      }
    }
  }

  replaceChildren(...items) {
    this._text = "";
    this.children = [];
    this.append(...items);
  }

  addEventListener() {}

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === "class") {
      this.className = String(value);
    }
  }
}

function createDocument() {
  const byID = new Map(elementIDs.map((id) => [id, new TestElement("div", id)]));
  return {
    byID,
    createElement(tagName) {
      return new TestElement(tagName);
    },
    getElementById(id) {
      if (!byID.has(id)) {
        byID.set(id, new TestElement("div", id));
      }
      return byID.get(id);
    },
  };
}

function response(payload, ok = true, status = 200) {
  return {
    ok,
    status,
    statusText: ok ? "OK" : "not found",
    async json() {
      return payload;
    },
    async text() {
      return typeof payload === "string" ? payload : JSON.stringify(payload);
    },
  };
}

async function loadApp(fixtures) {
  const document = createDocument();
  const session = new Map([["agent-team.autoRefresh", "0"]]);
  const context = {
    console,
    document,
    Node: TestElement,
    sessionStorage: {
      getItem(key) {
        return session.has(key) ? session.get(key) : null;
      },
      setItem(key, value) {
        session.set(key, String(value));
      },
      removeItem(key) {
        session.delete(key);
      },
    },
    fetch: async (path) => {
      const raw = String(path);
      if (raw.startsWith("/v1/resources?uri=")) {
        const uri = decodeURIComponent(raw.slice("/v1/resources?uri=".length));
        const payload = fixtures.resources.get(uri);
        return payload ? response(payload) : response({ error: "not found" }, false, 404);
      }
      if (raw === "/v1/jobs") {
        return response(fixtures.jobs);
      }
      if (raw === "/v1/instances") {
        return response(fixtures.instances);
      }
      if (raw === "/v1/topology") {
        return response(fixtures.topology);
      }
      return response({ error: "unexpected path" }, false, 404);
    },
    setInterval() {
      return 0;
    },
    clearInterval() {},
    Date,
    Error,
    Intl,
    Map,
    Number,
    Object,
    Promise,
    RegExp,
    Set,
    String,
    TypeError,
    decodeURIComponent,
    encodeURIComponent,
  };
  context.window = context;
  vm.createContext(context);
  const source = await readFile(new URL("./app.js", import.meta.url), "utf8");
  vm.runInContext(source, context, { filename: "internal/daemon/ui/app.js" });
  await Promise.resolve();
  return { context, document };
}

test("job telemetry reports runtime-only jobs as unknown model/tier and reads outcome classes", async () => {
  const missingURI = "agt://dep/job/runtime-only";
  const reportedURI = "agt://dep/job/reported";
  const workerURI = "agt://dep/instance/platform-worker-runtime-only";
  const reviewerURI = "agt://dep/instance/platform-reviewer-reported";
  const fixtures = {
    jobs: [
      {
        id: "runtime-only",
        uri: missingURI,
        ticket: "UI-telemetry-org-polish",
        implementation_agent: "worker",
        instance: "platform-worker-runtime-only",
        status: "running",
        pipeline: "platform_ticket_to_pr",
        updated_at: "2026-07-09T14:00:00Z",
      },
      {
        id: "reported",
        uri: reportedURI,
        ticket: "UI-telemetry-org-polish",
        implementation_agent: "worker",
        instance: "platform-reviewer-reported",
        status: "done",
        pipeline: "platform_ticket_to_pr",
        updated_at: "2026-07-09T13:00:00Z",
      },
    ],
    instances: [
      {
        instance: "platform-worker-runtime-only",
        uri: workerURI,
        agent: "worker",
        status: "running",
        job: "runtime-only",
        runtime: "codex",
      },
      {
        instance: "platform-reviewer-reported",
        uri: reviewerURI,
        agent: "reviewer",
        status: "stopped",
        job: "reported",
        runtime: "claude",
      },
    ],
    topology: {
      instances: [
        { name: "platform-worker", agent: "worker", ephemeral: true },
        { name: "platform-reviewer", agent: "reviewer", ephemeral: true, runtime: "claude", model: "claude-opus-4-8" },
      ],
      pipelines: [],
      budgets: [],
      teams: [],
      schedules: [],
    },
    resources: new Map([
      [
        missingURI,
        {
          uri: missingURI,
          kind: "job",
          id: "runtime-only",
          data: {
            id: "runtime-only",
            steps: [{ id: "implement", target: "worker", instance: "platform-worker-runtime-only", status: "running" }],
            usage: { records: [{ agent: "worker", runtime: "codex" }] },
          },
        },
      ],
      [
        reportedURI,
        {
          uri: reportedURI,
          kind: "job",
          id: "reported",
          data: {
            id: "reported",
            outcome: {
              runtime: "claude",
              model: "claude-sonnet-5",
              tier: "T2",
              step_runs: [{ id: "implement", target: "worker", agent: "worker", runtime: "claude", model: "claude-sonnet-5", tier: "T2" }],
              bounce_classes: { capability: 2, scope: 1 },
            },
          },
        },
      ],
      [workerURI, { uri: workerURI, kind: "instance", id: "platform-worker-runtime-only", data: { agent: "worker", runtime: "codex" } }],
      [reviewerURI, { uri: reviewerURI, kind: "instance", id: "platform-reviewer-reported", data: { agent: "reviewer", runtime: "claude" } }],
    ]),
  };

  const { context, document } = await loadApp(fixtures);
  const resources = await context.loadResources(fixtures.instances, fixtures.jobs);
  const runtimeOnly = context.jobTelemetry(fixtures.jobs[0], resources, fixtures.instances);
  const reported = context.jobTelemetry(fixtures.jobs[1], resources, fixtures.instances);

  assert.equal(runtimeOnly.runtime, "codex");
  assert.equal(runtimeOnly.model, "");
  assert.equal(runtimeOnly.tier, "");
  assert.equal(context.modelTierLabel(runtimeOnly), "model/tier unknown");
  assert.equal(context.modelTierLabel(reported), "claude-sonnet-5 / T2");

  const bounces = context.bounceClassesForJob(fixtures.jobs[1], resources);
  assert.equal(bounces.get("capability"), 2);
  assert.equal(bounces.get("scope"), 1);

  context.renderJobs({ ok: true }, fixtures.jobs, resources, fixtures.instances);
  context.renderTelemetry({ ok: true }, fixtures.jobs, resources, fixtures.instances);

  const jobsText = document.byID.get("jobsBody").textContent;
  const telemetryText = document.byID.get("modelTierBody").textContent + document.byID.get("bounceClassBody").textContent;
  assert.match(jobsText, /model\/tier unknown/);
  assert.match(jobsText, /runtime codex/);
  assert.match(jobsText, /claude-sonnet-5 \/ T2/);
  assert.match(jobsText, /capability 2/);
  assert.doesNotMatch(jobsText + telemetryText, /codex \/ T2/);
  assert.match(telemetryText, /capability/);
  assert.match(telemetryText, /scope/);
});
