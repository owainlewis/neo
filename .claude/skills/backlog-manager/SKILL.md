---
name: backlog-manager
description: "Manage an engineering backlog for humans and AI agents: review the whole loop, classify issues, improve issue quality, sync pull-request state, and label safe tickets so AI agents know what work they can pick up. Use when GitHub Issues, GitHub Projects, Linear, or an explicit local backlog path is the source of truth."
user-invocable: true
argument-hint: "<dry-run|apply> backlog for <GitHub repo|GitHub Project URL|Linear board|local path>"
---

# Backlog Manager

Manage an engineering backlog for humans and AI agents.

Think of this skill as a lightweight product manager for the backlog. The goal is to keep engineering
work clear, sequenced, classified, and safe to route to either humans or AI agents. A core job is to
label safe, well-scoped tickets so AI agents know which work they are allowed to pick up, while
marking ambiguous, risky, or judgement-heavy work for humans. This is backlog review and project-state
hygiene, not implementation. It labels issues, improves issue quality, identifies missing follow-up
tickets, creates evidence-backed maintenance tickets when allowed, and syncs issue state with linked
pull requests.

Default to `dry-run`. Only mutate GitHub, Linear, or another tracker when the user explicitly asks
for `apply`.

When running unattended (scheduled or non-interactive), never wait for user input. Record any
blocker or missing setup in the final report and exit.

When a tracker has workflow/status fields, keep status in sync with the issue/PR lifecycle. Labels
answer "who may pick this up"; statuses answer "where is it in the workflow." Do not use labels as a
substitute for an available project/status field.

## Jobs To Be Done

This is one umbrella skill: **Engineering Backlog Manager**. Keep the recurring loop in one skill
because the jobs share the same source of truth, labels, state model, and report. Split into separate
skills only when a job needs a different toolchain or safety policy.

Run the whole workflow by default rather than asking the user to orchestrate several tiny modes. The
workflow has three simple phases:

1. **Triage the backlog** — inspect open issues, current labels, stale state, linked PRs, and recent tracker changes.
2. **Prepare the queue** — classify issues by risk/type, mark safe work as `agent:ready`, route judgement-heavy work to `needs:human`, and add/update Agent Assessments.
3. **Maintain and report** — sync clearly completed issues from PR evidence, propose or create evidence-backed maintenance tickets, report branch cleanup candidates, verify the result, and summarize the next human decision.

The skill is not a coding agent. Its main output is a clean backlog and a safe queue that a separate execution loop can consume.

## Backlog Source Contract

Use exactly one backlog source of truth per run. Do not merge competing local and remote backlogs.

Supported sources:
- **GitHub Issues** for a named repo, using an installed and authenticated `gh` CLI.
- **GitHub Projects** when the user provides the project URL/path and `gh` can access it.
- **Linear** when the user provides the team/project/board path and a Linear connector is available.
- **Explicit local backlog path** only when the user provides that path and says it is the source of truth.

Default to GitHub Issues when the current repository has a GitHub remote and `gh` is installed and
authenticated. If `gh` is missing or unauthenticated, stop and report the prerequisite instead of
falling back to local planning files.

If the user does not name a tracker and no GitHub Issues source can be inferred, ask for the backlog
path/URL. Examples: a Linear board, a GitHub Project, a GitHub repo, or an explicit local backlog file.

Local roadmap files, ticket files, planning docs, and README sections are context only unless the user
explicitly says they are the backlog source. Treat divergence between those files and the tracker as
quality drift to report or fix, not as a second backlog to reconcile by default.

## Labels

Use this small fixed label set. Do not invent extra labels unless the user asks.

The skill owns only the labels listed below. Existing tracker labels such as `bug`, `enhancement`,
`documentation`, `exploration`, `good first issue`, or team-specific labels should be left alone
unless the user explicitly asks to normalize or remove legacy labels.

Managed labels are additive by default. Trackers do not record who added a label, so treat every
existing managed label as if a human set it deliberately. Never remove or change an existing
`risk:*`, `type:*`, `agent:ready`, or `needs:human` label during classification; only add managed
labels that are missing. The single exception is PR-evidence sync (Step 5), where `agent:ready` is
removed because the work is demonstrably in progress or finished. If a run disagrees with an
existing label, keep the label and raise the disagreement in the run report instead.

### Risk

- `risk:low` - Safe for agent execution when the issue is also `agent:ready`.
- `risk:medium` - Possibly agent-suitable later, but do not execute unattended by default.
- `risk:high` - Human-led. Agents may investigate or plan, but should not execute unattended.

### Type

- `type:bug` - Incorrect behavior or regression.
- `type:feature` - New user-facing or developer-facing capability.
- `type:docs` - Documentation, examples, README, comments, broken links, or written guidance.
- `type:test` - Test additions, test fixes, coverage, fixtures, or test reliability.
- `type:refactor` - Internal restructuring without intended behavior change.
- `type:chore` - Maintenance such as dependency upgrades, build scripts, CI config, formatting,
  package metadata, repo cleanup, or tool configuration.

### Routing

These labels are the machine-readable handoff contract. Keep routing labels deliberately small:

- `agent:ready` - Permission for an AI agent execution loop to pick up the issue.
- `needs:human` - A human decision, clarification, or judgement call is required.

Do not add extra routing labels by default. Use GitHub issue/PR state for completion and review state
instead of labels like `agent:complete` or `agent:blocked`. If an unlabeled issue cannot be safely
progressed, add `needs:human` with a specific question. If the issue already carries `agent:ready`,
leave the label in place and raise the concern in the run report instead of removing it.

If a repository already has a completion/routing convention such as `agent:complete` or
`agent:blocked`, respect it only when the user asks this skill to sync that convention or when the
repo docs clearly define it. Do not create those labels from this skill unless explicitly requested.

## Risk And Routing Rules

Use a simple contract. The execution loop should be able to query `agent:ready` and trust that the
issue is safe to attempt without re-litigating product risk.

### `agent:ready`

Only add `agent:ready` when all are true:
- risk is `risk:low`
- scope is clear
- the work is small enough for one pull request
- expected output is clear
- likely verification is known
- no product, UX, architecture, security, data, billing, auth, or deployment judgement is required
- the issue is not already linked to active work

Good low-risk examples:
- docs updates
- broken links
- stale README commands
- simple test additions
- lint or formatting fixes
- small repo chores
- simple CI command/config drift
- patch dependency upgrades with passing tests

### `needs:human`

Add `needs:human` when any are true:
- requirements are ambiguous
- expected behavior is unclear
- a reproduction is missing for a real bug
- the issue is too large for one pull request
- the issue needs product, UX, architecture, security, data, billing, auth, or deployment judgement
- the agent cannot classify the issue with confidence
- a previous agent attempt failed and the next step is unclear

### Risk levels

Use `risk:low` for small, bounded changes with clear verification and low blast radius.

Use `risk:medium` when the change may be agent-suitable later, but needs more confidence, stronger
tests, or close human review. Do not mark medium-risk issues `agent:ready` unless the user explicitly
asks this workflow to include medium-risk work.

Use `risk:high` when unattended execution could create meaningful product, security, operational, or
data risk. Add `needs:human` to high-risk issues unless they are already clearly human-owned.

## Agent Assessment

Put reasoning in the issue body or a comment instead of creating more labels.

Only write or rewrite the assessment when the classification, reasoning, or plan has actually
changed. Rewriting identical assessments on every run spams notifications.

Add or update this block:

```md
## Agent Assessment

Risk: low | medium | high
Type: bug | feature | docs | test | refactor | chore
Agent-ready: yes | no

Reason:
<1-3 sentences explaining the classification.>

Suggested plan:
1. <small first step>
2. <small second step>
3. <verification step>
```

If human input is needed, include:

```md
Human needed:
<specific question or decision required before an agent can execute.>
```

## Workflow

Think of the backlog manager as a repeatable product-management operating loop, not a one-shot labelling tool.

Each run should review the whole backlog loop against the selected source of truth: load context, resolve the backlog source, check labels, classify open issues for human/agent routing, sync clearly completed issues from pull-request evidence, sweep for evidence-backed quality drift, create or propose missing tickets according to the run mode, report branch cleanup candidates, verify tracker state, and report. Clearly state which steps were dry-run versus applied.

### Step 1 — Load Context

Read repository or workspace instructions first:
- `AGENTS.md`
- `CLAUDE.md`
- README files
- contribution/development docs
- issue templates
- local roadmap/backlog docs only as context, unless the user explicitly provides one as the backlog source

Use this context to classify risk and write issue assessments. Do not make up project policy.

If repo docs disagree with the selected backlog source, treat that as quality drift. Do not let local roadmap or ticket files override GitHub Issues, GitHub Projects, or Linear unless the user explicitly made the local path authoritative.

### Step 2 — Resolve Backlog Source

Use the backlog source the user names.

Otherwise:
- Use GitHub Issues only when `gh` is installed, authenticated, and the current directory has a GitHub remote.
- Use GitHub Projects only when the user provides a project URL/path and `gh` can access it.
- Use Linear only when the user provides a Linear team/project/board path and a Linear connector/tool is available.
- Use a local backlog file only when the user explicitly provides the file path and says it is the source of truth.
- If no backlog source can be resolved, stop and ask for the backlog path/URL or the missing setup.

Do not infer a Linear board, GitHub Project, or local backlog from vague references. Divergent branches
or planning docs are context for the product-manager review, not independent backlog sources.

When the user names a GitHub Project or confirms one during the run, load its fields and record the
status option IDs before mutating anything. Common mappings are:

- `Todo` for open unstarted issues.
- `In Progress` for issues currently owned by a worker or active branch.
- `In Review` for issues with an open PR ready for review.
- `Done` for issues closed by a merged PR.

Use existing option names even when they differ slightly. Do not create project fields or statuses
unless the user explicitly asks.

### Step 3 — Ensure Labels Exist

In `dry-run`, report missing labels.

In `apply`, create missing labels where the tracker supports it.

Recommended GitHub colors:
- `risk:low` - `0E8A16`
- `risk:medium` - `FBCA04`
- `risk:high` - `B60205`
- `type:*` - `5319E7`
- `agent:*` - `1D76DB`
- `needs:human` - `D93F0B`

### Step 4 — Classify Open Issues

Fetch open issues with title, body, labels, comments, status/project fields when available, and linked pull requests when the tracker exposes them.

Classification fills gaps; it never overrides existing managed labels.

For each open issue:
1. If it has no managed `risk:*` label, assign exactly one.
2. If it has no managed `type:*` label, assign exactly one.
3. If it has no routing label, decide whether to add `agent:ready`, `needs:human`, or neither.
4. Never remove or change managed labels that are already present. If the classification disagrees
   with an existing label, keep the label and note the disagreement in the run report.
5. Add or update the Agent Assessment only if it changed.
6. Avoid marking an issue `agent:ready` when confidence is low. Use `needs:human` and explain why.

Do not mark medium-risk or high-risk issues `agent:ready` unless the user explicitly asks for that policy change. Agents should be able to use `agent:ready` as their default pickup queue without re-litigating product risk.

### Step 5 — Sync Issue State With Pull Requests

Keep issue state aligned with linked PRs.

If a linked PR is open:
- move the issue to the tracker review state when status/project fields are available
- remove `agent:ready` if the work is already being attempted
- leave or add a short issue comment only when it adds useful state, such as a missing PR link or
  verification summary

If a linked PR is merged:
- remove `agent:ready`
- close the issue when the PR clearly resolves it
- move the issue/project item to the done state when status/project fields are available
- preserve audit labels such as `risk:*`, `type:*`, and any repo-approved completion label

If a linked PR is closed without merge:
- remove `agent:ready`
- add `needs:human` when the next step is unclear
- comment with the known reason when available
- move the issue/project item back to an appropriate open state only when the tracker policy is clear;
  otherwise leave status unchanged and report the mismatch

Do not close an issue unless the linked PR clearly resolves it. Use GitHub issue/PR state for
completion instead of adding a separate completion label.

If a PR exists but merge readiness is unclear, do not infer completion from the PR title or branch
name. Check live PR state: draft flag, mergeability, checks, review submissions, and unresolved
review threads. Open PR plus unresolved actionable feedback means review/in-progress, not done.

When the backlog source has both issues and project items, verify the two agree after sync. For
example, a closed issue should not remain `In Review`, and an open issue without an active PR should
not remain `In Review` unless there is a human reason recorded.

### Step 6 — Sweep The Repo For Quality Drift

Run this step on every full backlog review. Keep it evidence-driven and proportional: the goal is to catch product/project drift that should become a ticket or a report item, not to perform an unbounded code audit.

The sweep is evidence-driven. Look for concrete problems, not speculative improvements:
- stale docs referencing closed/open issue state incorrectly
- local backlog or roadmap docs that contradict tracker state
- docs saying something is not implemented when code exists, or saying it exists when code is missing
- broken local Markdown links
- README/setup commands that do not exist in `justfile`, package scripts, CLI help, Makefile, or docs
- generated docs drift when the repo has a documented check command
- TODO/FIXME/HACK comments that describe clear, bounded work
- skipped tests or disabled checks that look accidental
- recent failed CI/check runs on the default branch
- simple build/lint/config drift with a clear verification command

Do not create issues for:
- vague improvement ideas
- speculative refactors
- architecture rewrites
- product ideas
- anything requiring business, UX, security, data, billing, auth, or deployment judgement

Deduplicate against existing open and recently closed issues before proposing or creating anything.

### Step 7 — Create Candidate Issues

In `dry-run`, do not create issues. Output candidates in this shape:

```md
Candidate issue: <title>
Evidence:
- <file, command, PR, issue, or code reference>
Why it matters:
<short explanation>
Suggested fix:
<small reviewable fix>
Risk: low | medium | high
Type: bug | feature | docs | test | refactor | chore
Agent-ready: yes | no
Confidence: high | medium | low
Create issue: yes | no
```

In `apply`, create a new issue only when there is concrete evidence of a real problem and confidence is high. If the concern is plausible but not proven, mention it in the run report instead of creating a ticket.

Every agent-created issue must include:
- evidence, including file paths, commands, config keys, PRs, issue links, or code references
- why the problem matters
- a small suggested fix
- an Agent Assessment

### Step 8 — Report Unneeded Branches

Treat branch cleanup as backlog hygiene, but do not delete branches from this skill.

Inspect remote branches and PR state. Good cleanup candidates are branches that are:
- already merged into the default branch
- linked to closed issues or merged/closed PRs with no remaining work
- stale automation branches whose PR was closed without merge and has no active follow-up

Never delete branches in this workflow. Report cleanup candidates with evidence and leave deletion to a
separate explicit branch-cleanup workflow or a human.

### Step 9 — Verify Apply Runs

After an `apply` run, verify the tracker state before reporting:
- Every remaining open issue has a managed `risk:*` label and a managed `type:*` label.
- Any issue where `agent:ready` appears without `risk:low`, or alongside `needs:human`, is flagged
  in the report, not auto-corrected. A human may have set those labels deliberately.
- Every `risk:high` issue has `needs:human` unless there is a clear reason not to.
- Every classified open issue has an `## Agent Assessment` block in the issue body or an equivalent comment.
- Any stale completed issue closed during sync still keeps its final risk/type labels and assessment for auditability.
- Any issues created by the sweep are deduplicated and include evidence.
- Issues with open linked PRs are in the review state when the tracker has one.
- Issues closed because a linked PR merged are in the done state when the tracker has one.
- No issue is both closed and left in an active status such as `In Progress` or `In Review`, unless
  the report calls out a tracker limitation or failed API update.

For GitHub, a small verification script using `gh issue list --json number,title,labels,body` is safer than eyeballing the web UI.

### Step 10 — Report

End with a compact summary:
- tracker used
- mode used
- steps run: classify, sync, sweep, create candidates/issues, branch cleanup report, verify
- number of issues inspected
- labels created or missing
- issues changed
- issues marked `agent:ready`
- issues marked `needs:human`
- issues closed or synced from PR state
- sweep candidates found or created
- branch cleanup candidates found and reported
- verification result
- blockers and recommended next action

## Scheduled Runs / Cron

For scheduled backlog management, run the full engineering-backlog loop every time so the repo stays in a healthy state: review backlog, label tickets, find repo drift, create/propose missing issues, sync ticket state, close stale/completed tickets, report safe branch-cleanup candidates, verify, and report.

Keep scheduled mutation policy explicit. A cron may run in `dry-run` mode, or in conservative `apply` mode once the user has approved exactly which mutations are allowed for the repo.

Cron prompts must be self-contained. Include:
- repo/tracker name
- source-of-truth rule
- allowed mutation policy
- whether to create issues or only propose candidates
- verification requirements
- delivery target

Default scheduled behaviour should not merge PRs, publish releases, change secrets, spend money, delete branches, or make high-risk changes.

## GitHub Adapter

Use `gh` when available.

Useful commands:

```bash
gh repo view --json nameWithOwner,url
gh label list --limit 200
gh label create "risk:low" --color "0E8A16" --description "Low-risk issue suitable for agent execution when agent-ready"
gh issue list --state open --limit 100 --json number,title,body,labels,url,createdAt,updatedAt,comments
gh issue edit <number> --add-label "risk:low,type:docs,agent:ready"
gh issue comment <number> --body-file <file>
gh issue close <number> --comment "Closed because linked PR <url> was merged."
gh project list --owner <owner>
gh project field-list <project-number> --owner <owner> --format json
gh project item-list <project-number> --owner <owner> --limit 200 --format json
gh project item-add <project-number> --owner <owner> --url <issue-or-pr-url> --format json
gh project item-edit --id <item-id> --project-id <project-id> --field-id <status-field-id> --single-select-option-id <option-id>
```

For linked PRs, use GraphQL or `gh pr list`/`gh pr view` as needed. Prefer exact linked PR data over
guessing from branch names or text search.

Project mutations can be flaky through the GitHub API. When a project write times out, query the item
before retrying so you do not duplicate comments or create duplicate project items. Retry serially and
verify the final status.

## Linear Adapter

Use Linear when the user asks for Linear and the connector/tool is available.

Map labels directly:
- `risk:*`
- `type:*`
- `agent:ready`
- `needs:human`

Map issue status to the workspace's existing workflow states. Do not create new status states unless
the user asks.

When Linear and GitHub are connected, use linked PR state to update Linear issue status and labels.

## Example Invocations

```text
$backlog-manager dry-run GitHub backlog for this repo
$backlog-manager apply full backlog loop for GitHub repo owainlewis/neo
$backlog-manager dry-run backlog for GitHub Project https://github.com/orgs/acme/projects/7
$backlog-manager dry-run Linear backlog for team ENG project Agentic Engineer
$backlog-manager dry-run backlog from ./BACKLOG.md as the source of truth
```

## Safety Rules

- Default to `dry-run`.
- Do not mutate trackers unless the user asks for `apply`.
- Never remove or downgrade existing managed labels during classification; only fill gaps.
  PR-evidence sync (Step 5) is the only step allowed to remove `agent:ready`.
- Do not add `agent:ready` to high-risk issues.
- Do not auto-close issues without clear linked merged PR evidence.
- Do not mark a ticket done while linked PR checks are failing, pending, or unresolved review threads
  remain actionable.
- Do not create speculative work.
- Do not use the backlog manager to implement code.
- Do not delete branches from this skill; only report cleanup candidates.
- When unsure, classify conservatively and route the question through `needs:human` instead of
  waiting for a reply.

## Quality Bar

- [ ] Uses the small fixed label set.
- [ ] Existing managed labels were respected; classification only filled gaps.
- [ ] Each classified issue has one managed risk label and one managed type label.
- [ ] `agent:ready` only appears on low-risk, clear, verifiable work.
- [ ] Human decisions are routed through `needs:human`, not extra labels.
- [ ] Issue reasoning lives in Agent Assessment, not label sprawl.
- [ ] Merged PR cleanup only happens with clear evidence.
- [ ] Branch cleanup is report-only.
- [ ] Final report is concise and actionable.
