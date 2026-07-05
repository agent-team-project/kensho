#!/usr/bin/env python3
"""Mirror open Linear (SQU) tickets to GitHub issues + the agent-team project board.

Idempotent: skips any ticket whose backlink marker already exists in an issue body.
Linear stays the source of truth during the parallel-run window; this mirrors, it
does not close or mutate Linear. Requires: LINEAR_USER_API_KEY env, gh authed with
repo + project scopes.

Usage: python3 scripts/migrate/linear_to_github.py [--repo owner/name] [--project-owner user] [--project-number N] [--dry-run]
"""
import argparse, json, os, subprocess, sys, urllib.request

def linear(query, variables=None):
    req = urllib.request.Request(
        "https://api.linear.app/graphql",
        data=json.dumps({"query": query, "variables": variables or {}}).encode(),
        headers={"Authorization": os.environ["LINEAR_USER_API_KEY"], "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        out = json.load(r)
    if out.get("errors"):
        raise SystemExit(f"linear error: {out['errors']}")
    return out["data"]

def gh(*args, input=None):
    r = subprocess.run(["gh", *args], capture_output=True, text=True, input=input)
    if r.returncode != 0:
        raise SystemExit(f"gh {' '.join(args[:3])} failed: {r.stderr.strip()}")
    return r.stdout.strip()

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default="agent-team-project/agent-team")
    ap.add_argument("--project-owner", default="agent-team-project")
    ap.add_argument("--project-number", type=int, default=1)
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args()

    tickets = linear("""query { issues(filter: {team: {key: {eq: "SQU"}},
        state: {type: {nin: ["completed","canceled"]}}}, first: 50) {
        nodes { identifier title description url state { name } labels { nodes { name } } } } }
    """)["issues"]["nodes"]
    existing = gh("issue", "list", "--repo", a.repo, "--state", "all", "--limit", "200",
                  "--json", "number,body")
    seen = {n["number"]: n.get("body") or "" for n in json.loads(existing)}
    proj_args = ["--owner", a.project_owner]
    created = skipped = 0
    for t in tickets:
        marker = f"Mirrored-From: {t['url']}"
        if any(marker in b for b in seen.values()):
            skipped += 1
            print(f"skip  {t['identifier']} (already mirrored)")
            continue
        title = f"[{t['identifier']}] {t['title']}"
        labels = [l["name"] for l in t["labels"]["nodes"]]
        body = (t.get("description") or "(no description)") + \
            f"\n\n---\n{marker}\nLinear-State: {t['state']['name']}\n" + \
            "Parallel-run window: Linear remains the source of truth; this mirror validates the GitHub Projects control plane."
        if a.dry_run:
            created += 1
            print(f"would create: {title} labels={labels}")
            continue
        args = ["issue", "create", "--repo", a.repo, "--title", title, "--body", body]
        for l in labels:
            args += ["--label", l]
        url = gh(*args)
        gh("project", "item-add", str(a.project_number), *proj_args, "--url", url)
        created += 1
        print(f"created {t['identifier']} -> {url}")
    print(f"\ndone: {created} created, {skipped} skipped, {len(tickets)} total open")

if __name__ == "__main__":
    main()
