# Find stale branches to clean up

Your goal is to identify branches that are fully merged into the default
branch or otherwise abandoned, and open one GitHub issue for a human to
review. Do not delete, force-push, or rewrite any branch in this workflow.

## Inspect branches

Use authenticated `git` and `gh` commands to list all remote branches, their
last commit date, merge status into the default branch, and any associated
open or closed pull request. Exclude the default branch and any branch with
an open pull request.

Classify each remaining branch as:

- **merged**: every commit on the branch is reachable from the default
  branch (fully merged via a merge commit, squash, or rebase), and it has no
  open pull request — safe to delete;
- **stale**: not merged, no commits in 60+ days, no open pull request, and no
  recent discussion — likely abandoned but still contains unmerged work;
- **active**: recent commits or discussion — leave alone.

For a squash-merged branch, confirm the merge by checking the linked, closed
pull request's merge status rather than relying on `git merge-base` alone,
since squash merges do not make the branch's tip reachable from the default
branch through ancestry.

## Create the ticket

If there are no merged or stale branches, make no changes and finish with a
short summary confirming the repository is clean.

Otherwise, create or update one GitHub issue listing merged and stale
branches, grouped by classification, with the branch name, author, last
commit date, and the reasoning behind the classification. Recommend deletion
for merged branches and a keep-or-delete decision for stale ones. Apply an
existing maintenance or chore label when the repository has one, but do not
create new labels. Do not add `factory:ready-for-spec` or
`factory:ready-to-implement` — a human reviews the findings and decides
whether to move the ticket into the pipeline.
