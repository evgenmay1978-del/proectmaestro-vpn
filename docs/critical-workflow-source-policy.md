# Critical workflow source policy

This guard closes the bootstrap gap in self-validating GitHub Actions. A pull
request can edit or skip checks inside its candidate workflows, so
'.github/workflows/critical-workflow-source-policy.yml' runs on
'pull_request_target'. GitHub therefore executes the version from the protected
base branch, not the pull-request version.

The guard has only 'contents: read'. It checks out nothing, executes no
candidate code, and does not interpolate pull-request fields into a shell. Its
base-owned inline Python reads the exact head repository and 40-character head
SHA from 'GITHUB_EVENT_PATH'. It resolves that immutable commit through the Git
Commits and Git Trees APIs, rejects truncated trees, and requires every critical
path to be exactly one non-executable regular blob with mode '100644'. It then
downloads only those three paths through the Contents API, requires each
response blob SHA to match the reviewed tree, enforces a 256 KiB limit and
strict UTF-8, LF normalizes the text, and accepts only reviewed SHA-256 values.

## Mandatory repository ruleset

The code is not the complete trust boundary until the GitHub repository
ruleset is enabled on every production/default branch:

1. Require pull requests and at least one code-owner approval.
2. Dismiss stale approvals and require approval after the latest push.
3. Require the workflow
   '.github/workflows/critical-workflow-source-policy.yml' by workflow identity.
   If the repository plan exposes only status contexts, require the stable
   'Critical workflow source policy / critical-workflow-source-policy' check.
4. Require conversation resolution and block force pushes and branch deletion.
5. Disallow direct pushes and disable bypass, including administrator bypass,
   for '.github/CODEOWNERS' and all four workflow paths named there.

Do not add a 'paths' filter to the guard: a required path-filtered workflow can
remain pending on unrelated pull requests. Do not replace
'pull_request_target' with 'pull_request', checkout the candidate, download a
candidate script, or execute any candidate content.

## First activation

The guard does not exist in the protected base branch until this bootstrap
change is merged. Merge it only with explicit owner review. Open one harmless
follow-up pull request so GitHub emits the stable check, configure the mandatory
ruleset above, and confirm that the check is required before treating the guard
as production-enforced.

## Reviewed critical workflow hashes

- 'ha-control-plane.yml':
  'db5da2254acdadcd11095748fb07848627aa492a5d0094638e62e1602444cfe7'
- 'ha-dr-restore-drill.yml':
  '0982b58c98ad8dc56cd6a68fbb19554e4ec3c8a9f237a3860770e225adcfdef9'
- 'yandex-cdn-release.yml':
  '54b906858b483dac81cc6e50e0c3c2657d5de5b3d51bd676716bf100dfd24d33'

These hashes cover LF-normalized full source text. A metadata-only bypass,
extra job or step, altered 'run' body, removed APK path, or changed permission
therefore fails before candidate workflow code runs.

## Two-phase workflow update

Changing a protected workflow is intentionally two-phase:

1. In PR A, change only the base-owned guard allowlist to accept both the
   current hash and one explicitly reviewed next hash. Merge PR A after owner
   review.
2. In PR B, change the critical workflow to the reviewed source and narrow the
   candidate guard allowlist to the new hash. The PR A base guard accepts the
   exact new source; after merge, the old source is no longer accepted.

If PR B is abandoned, immediately revert PR A. Never accept a wildcard,
branch name, prefix hash, mutable URL, or candidate-provided digest.

## Verification

Run the local contract before push:

    python -m unittest scripts.tests.test_critical_workflow_source_policy

On GitHub, the required check must report each of the three paths as verified
without printing file content. Any API error, malformed event, commit/tree SHA
mismatch, truncated tree, symlink or non-'100644' entry, non-file response,
oversized response, invalid base64/UTF-8, blob mismatch, or source hash mismatch
is a hard failure.
