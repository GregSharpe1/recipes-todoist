---
name: deploy-recipes-todoist
description: Deploy the latest recipes-todoist image built successfully after a merge to the application default branch by discovering and updating its live configuration in GregSharpe1/k8s, then opening a deployment PR containing the application commit diff, environment-variable impact, validation, and rollback notes. Use when asked to deploy, promote, or release recipes-todoist; the normal invocation requires no image, tag, commit, or Kubernetes manifest path.
---

# Deploy Recipes Todoist

Automatically promote the newest verified image from the application default branch.
Create a small PR in `GregSharpe1/k8s`; never apply directly to the cluster.

## Resolve the latest image automatically

Do not ask the user for an image, tag, commit, or source branch in the normal flow.

1. Start in a `recipes-todoist` checkout. Inspect `git status --short` and preserve
   unrelated changes.
2. Discover the source slug, URL, and default branch with `gh repo view`; do not
   infer them from the local directory name.
3. Fetch the source remote and freeze the fetched default-branch tip as the target
   SHA. Inspect the container workflow at that revision to determine the registry,
   image repository, immutable tag format, and workflow responsible for publishing
   it.
4. Query GitHub Actions for the run of that workflow whose `headSha` exactly equals
   the frozen target SHA. If that build has not appeared yet, poll briefly; if it is
   queued or running, wait when practical. If it fails, stop and report the failed
   build rather than silently deploying an older image as “latest.”
5. Require the target SHA to remain contained in the remote default branch and the
   exact run to succeed. Verify that run published its immutable GHCR tag or digest. The
   current workflow yields `ghcr.io/gregsharpe1/recipes-todoist:sha-<short-commit>`,
   but always re-derive this because the workflow may change.

Pin the run SHA, immutable image reference, build URL, and source URL for the whole
operation. Do not deploy `latest`, accept a user-supplied mutable image, rebuild
locally as a substitute, or silently retarget if the branch moves mid-run.

## Prepare the k8s checkout

1. Check for an existing open k8s PR that promotes the pinned image. Reuse and
   report it instead of creating a duplicate.
2. Reuse a clean local `GregSharpe1/k8s` checkout only when it is explicitly in
   scope; otherwise clone it into a directory created with `mktemp -d`.
3. Read its root `AGENTS.md` and any narrower instructions before editing.
4. Fetch its discovered default branch and create a branch named like
   `deploy/recipes-todoist-sha-<short-commit>`.

Never encode a deployment pathname. Discover the current location on every run:

```bash
python3 <application-checkout>/.agents/skills/deploy-recipes-todoist/scripts/discover_image_references.py \
  --repo-root <k8s-checkout> \
  --image <automatically-derived-image-repository>
```

Review every result and the repository's Argo CD, Kustomize, or Helm wiring. Select
live GitOps configuration by Kubernetes object identity, container name, and active
entrypoint inclusion—not merely by a familiar filename. Ignore documentation,
migration samples, generated output, and historical copies. If multiple live
environments deploy the image, update only the environment clearly established by
repository context; ask only when that scope is genuinely ambiguous.

The helper returns complete image values and context around split Helm/Kustomize
references. If it finds no exact repository reference, search by workload and
container identity and inspect values, Kustomize images, Jsonnet, or other
indirection. Never fall back to a remembered path.

## Determine the deployed baseline

Read the current image from the selected live configuration. Resolve a
`sha-<abbrev>` tag to the full application commit and confirm it is an ancestor of
the pinned target. For a mutable or non-commit-derived reference, inspect the k8s
file history to recover the last deployed application SHA.

Do not fabricate a baseline. If it cannot be recovered, label the comparison
incomplete and explain why. If the live configuration already uses the pinned
image, report that deployment is current and create no empty PR. Identify a
non-ancestor target as a rollback or non-linear promotion before editing.

## Review application and configuration impact

Compare the deployed application commit with the target:

```bash
git -C <application-checkout> log --reverse --oneline <deployed>..<target>
git -C <application-checkout> diff --stat <deployed>..<target>
git -C <application-checkout> diff <deployed>..<target>
```

Review, at minimum:

- Runtime environment-variable names, requiredness, defaults, and semantics.
- Secrets and ExternalSecret keys, without exposing secret values.
- Ports, probes, command/arguments, permissions, and filesystem paths.
- Database/schema migrations, persistent storage, compatibility, and rollback risk.
- Operationally significant behavior or dependency changes.

Update Kubernetes configuration only when the new application contract requires it.
Follow the target repository's existing managed-secret pattern. Never expose a
secret value in a manifest, commit, log, or PR body.

## Edit and validate

Change the discovered controlling image value to the pinned immutable image.
Preserve its existing representation, whether workload image, Helm value, or
Kustomize override. Make the smallest correct edit and avoid unrelated formatting.

Run the target repository's required checks and the narrowest rendering or YAML
validation that covers the changed entrypoint. Never run `kubectl apply` or mutate
the cluster. Inspect `git diff --check`, `git diff`, and `git status --short` in the
k8s checkout. Confirm that only the intended live reference and contract-driven
configuration changed.

## Build the PR description

Generate the body before committing while the k8s diff is in the worktree:

```bash
python3 <application-checkout>/.agents/skills/deploy-recipes-todoist/scripts/build_pr_body.py \
  --app-repo <application-checkout> \
  --base <deployed-full-sha> \
  --target <automatically-selected-full-sha> \
  --old-image <discovered-old-image> \
  --new-image <automatically-selected-immutable-image> \
  --deployment-repo <k8s-checkout> \
  --build-url <successful-build-run-url> \
  --environment-note "<reviewed env/config impact>" \
  --note "<migration, storage, compatibility, or rollback finding>" \
  --verification "<check and result>" \
  --output <temporary-pr-body-file>
```

Supply multiple note arguments as needed, then inspect the generated body. It must
contain:

- Old/new immutable images and source commits.
- Successful build evidence and a full source compare link.
- Application commit list, diffstat, and embedded unified commit diff.
- Explicit environment/configuration assessment, including “no changes” when true.
- The exact Kubernetes worktree diff.
- Migration, storage, compatibility, rollout/rollback, and validation notes.

The helper bounds oversized embedded patches and links the complete compare diff.
Its runtime-env scan supplements but does not replace manual review. Remove any
sensitive material found during inspection before publishing.

## Publish

1. Commit only the intended k8s changes with a message such as
   `Deploy recipes-todoist sha-<short-commit>`.
2. Push without force.
3. Create a non-draft PR against the discovered k8s default branch using
   `gh pr create --body-file <temporary-pr-body-file>`.
4. Read the PR back and verify its base, head, files, image, and description.

Report the PR URL, source comparison, automatically selected image,
environment/configuration impact, checks, and rollout caveats. Stop at PR creation;
do not merge it or operate Argo CD unless separately requested.
