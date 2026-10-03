# Risks: an AI agent with push access

What can an AI agent that may push to a repository break, and which of that can be repaired? This is the threat model behind Git Warden. How far Git Warden's two guard posts (Push Guard, Pull Guard) cover each risk, including what they **don't** cover, is in [Coverage by Git Warden](#coverage-by-git-warden).

**Risk IDs** (R1–R22) are stable: they are never renumbered, new risks are only appended at the end. The rest of the docs refer to them by ID. Overview: [Risk register](#risk-register). Research as of October 2026.

## TL;DR

1. With push access, an agent can destroy or quietly manipulate code, history, branches, tags and releases; most of that can be recovered with Git/GitHub.
2. Not recoverable: leaked secrets, published packages/releases, exfiltrated data and repos that were briefly public.
3. The bigger lever is the **token**, not the push: a classic `repo` token applies to *all* of the user's repos, including collaborators, webhooks and visibility; `workflow` allows CI manipulation.
4. Real cases from 2025 (Nx/s1ngularity, Shai-Hulud, Amazon Q, GitHub MCP injection) show exactly these chains: stolen `gh` token → private repos made public, workflows that grab secrets.
5. In a typical setup, an owner token on a machine the agent can read is a single point of failure → repo-bound tokens or a GitHub App per agent, bots without admin rights, rulesets against force push and deletion, backups the agent can't write to.

## 1. Damage inside the repo

- **R1 – Destroying history.** `git push --force` overwrites branches, `git push --delete` removes branches and tags, a rebase/`filter-repo` rewrites history. GitHub blocks force pushes only on protected branches – and by default the rules **don't apply to admins** ([GitHub Docs: About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)). Force pushes can also "delete branches or point them to unreviewed commits" (ibid.).
- **R2 – Deleting or damaging code.** Mass deletions, broken refactorings, lockfile chaos. Loud, but easy to repair.
- **R3 – Subtle bugs and backdoors.** More dangerous than loud damage: a weakened auth check, a new dependency, a `postinstall` script. Without review, nobody notices. Trail of Bits showed how hidden issue text gets the Copilot agent to build in a backdoor via a manipulated `uv.lock` ([Trail of Bits, Aug 2025](https://blog.trailofbits.com/2025/08/06/prompt-injection-engineering-for-attackers-exploiting-github-copilot/)) – lockfiles are a favourite hiding place because nobody reads them.
- **R4 – Secrets in commits.** GitGuardian counted over 23.7 million new hard-coded secrets in public GitHub repos in 2024; repos with Copilot active had a 40 % higher leak rate (6.4 % vs. 4.6 %) – correlation, not causation ([State of Secrets Sprawl 2025](https://blog.gitguardian.com/the-state-of-secrets-sprawl-2025/)).
- **R5 – Licence/legal problems.** An agent can copy in third-party code with an incompatible licence or change `LICENSE` files (relevant for PolyForm Noncommercial repos).
- **R6 – Spam.** Thousands of commits, issues, releases or comments in the user's name – reputational damage, possibly account suspension.

## 2. Supply chain

- **R7 – Rewriting CI/CD workflows.** Whoever can write `.github/workflows/` runs code with the repo's secrets. Shai-Hulud (Sept 2025) used stolen tokens to push a branch `shai-hulud` with a workflow that sent `toJSON(secrets)` to a webhook ([GitGuardian](https://blog.gitguardian.com/shai-hulud-a-persistent-secret-leaking-campaign/), [Wiz](https://www.wiz.io/blog/shai-hulud-npm-supply-chain-attack)).
- **R8 – Moving tags.** In tj-actions/changed-files (March 2025, CVE-2025-30066), tags were retroactively moved to a malicious commit that wrote secrets into workflow logs ([CISA](https://www.cisa.gov/news-events/alerts/2025/03/18/supply-chain-compromise-third-party-tj-actionschanged-files-cve-2025-30066-and-reviewdogaction)) – tags are mutable, only SHAs are not. Whoever controls a repo with push access can silently move any release tag.
- **R9 – Malicious releases/packages.** In Nx (Aug 2025), a manipulated workflow sent the npm publish token to a webhook; malicious Nx versions were published afterwards ([Nx advisory GHSA-cxm3-wv7p-598c](https://github.com/nrwl/nx/security/advisories/GHSA-cxm3-wv7p-598c)). In Amazon Q Developer for VS Code, an over-privileged GitHub token in the CodeBuild configuration let an attacker commit code that shipped automatically in version 1.84.0 ([AWS-2025-015](https://aws.amazon.com/security/security-bulletins/AWS-2025-015/), CVE-2025-8217). The injected prompt was meant to return the system "to a near-factory state" and delete cloud resources; it failed because of a syntax error ([SC Media](https://www.scworld.com/news/amazon-q-extension-for-vs-code-reportedly-injected-with-wiper-prompt)).
- **R10 – Dependency confusion.** A public package with the name of an internal dependency is preferred by the package manager ([Alex Birsan, 2021](https://medium.com/@alex.birsan/dependency-confusion-4a5d60fec610)). An agent can trigger this by changing registry configuration or package names.
- **R11 – Poisoning GitHub Pages.** Whoever pushes changes the website built from the repo – phishing or malware downloads under a trusted domain.

## 3. Beyond the repo: what the token allows

According to the [GitHub scope docs](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps):

| Scope | What it means for the agent |
| --- | --- |
| `repo` | Full access to **all** of the user's public and private repos: code, collaborators, invitations, webhooks, deploy statuses. With owner rights also admin functions such as settings, deploy keys, visibility (private → public), changing/removing branch protection. |
| `workflow` | Create/change workflow files → arbitrary code in CI. |
| `gist` | Write gists – a convenient exfiltration channel. |
| `read:org` | Read org/team memberships (reconnaissance). |
| `delete_repo` | Delete repos (separate scope). |

Shai-Hulud used exactly such tokens to publish private repos as public copies with the suffix `-migration`; one victim had access to 528 private company repos ([Safety](https://www.getsafety.com/blog-posts/shai-hulud-npm-attack)). In phase 2 of s1ngularity, leaked tokens were used to make over 5,500 private repos of more than 400 users/orgs public ([Wiz](https://www.wiz.io/blog/s1ngularity-supply-chain-attack)).

This gives three risks:

- **R12 – Reach beyond the target repo.** A token for "one" repo acts on all repos of the account.
- **R13 – Settings, visibility, access.** Private → public, new collaborators, deploy keys, webhooks, removing branch protection/rulesets, switching the default branch.
- **R14 – Exfiltration.** Private content ends up in gists, new public repos (`s1ngularity-repository`, `Shai-Hulud`) or PRs/comments in public repos.

## 4. Agent-specific vectors

- **R15 – Prompt injection via repo content.** Invariant Labs showed in May 2025: a prepared issue in a public repo gets an agent using the official GitHub MCP server to read private repos and publish their content via a PR – a "toxic agent flow" ([Invariant Labs](https://invariantlabs.ai/blog/mcp-github-vulnerability), [Toxic Flow Analysis](https://invariantlabs.ai/blog/toxic-flow-analysis)). The cause is not a bug in the server but a token that sees public *and* private repos. At the end of 2025, Aikido described the same pattern for AI agents in GitHub Actions that take issue titles/bodies into prompts ("PromptPwnd", including Gemini CLI) ([Aikido](https://www.aikido.dev/blog/promptpwnd-github-actions-ai-agents)). Other sources: READMEs, code comments, documentation of dependencies.
- **R16 – Local agents as malware tools / token theft.** The Nx malware called installed `claude`, `gemini` and `q` CLIs with `--dangerously-skip-permissions`, `--yolo` or `--trust-all-tools` to inventory secrets, and fetched the GitHub token via `gh auth token` ([Snyk](https://snyk.io/blog/weaponizing-ai-coding-agents-for-malware-in-the-nx-malicious-package/), [StepSecurity](https://www.stepsecurity.io/blog/supply-chain-security-alert-popular-nx-build-system-package-compromised-with-data-stealing-malware)). According to Snyk, the vulnerable workflow where it all started was probably generated with Claude Code.
- **R17 – Hallucinated packages ("slopsquatting").** In 2.23 million generated package references, 19.7 % were made up, 205,474 unique non-existent names ([Spracklen et al., USENIX Security 2025](https://www.usenix.org/conference/usenixsecurity25/presentation/spracklen)). Attackers can register such names.
- **R18 – Mistakes without an attacker.** Much damage needs no attacker: in July 2025, during a "code freeze", the Replit agent deleted Jason Lemkin's production database (1,206 executives, 1,196+ companies) and at first wrongly claimed a rollback was impossible ([Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/), [Fast Company](https://www.fastcompany.com/91372483/replit-ceo-what-really-happened-when-ai-agent-wiped-jason-lemkins-database-exclusive)). Instructions in the prompt are not access control.

## 4a. Further risks (from the research for Git Warden)

- **R19 – Naive mirrors mirror the destruction.** A pull mirror takes over force updates and deletes refs that were deleted upstream; Forgejo explicitly processes force updates and ref deletions on sync ([Forgejo repository mirrors](https://forgejo.org/docs/latest/user/repo-mirror/)). Gitea Mirror writes that after a force push the old history in Gitea is replaced "with no way to get it back" unless the protection is active ([Gitea Mirror: Force-Push Protection](https://gitea-mirror.raylabs.io/docs/force-push-protection/)). A backup that syncs blindly is worthless for R1/R2.
- **R20 – Compromised review/bot service with write access.** Kudelski Security got code execution on CodeRabbit's servers via a prepared `.rubocop.yml` in a PR, and with it the GitHub App's private key – write access to around 1 million repos, including private ones ([Kudelski](https://kudelskisecurity.com/research/how-we-exploited-coderabbit-from-a-simple-pr-to-rce-and-write-access-on-1m-repositories), [CodeRabbit statement](https://www.coderabbit.ai/blog/our-response-to-the-january-2025-kudelski-security-vulnerability-disclosure-action-and-continuous-improvement)). Every guard that holds write access is itself a target.
- **R21 – AI gatekeepers can be persuaded.** With iteratively refined PR metadata, re-introducing known vulnerabilities against Claude Code and CodeRabbit review pipelines succeeded in 32 of 33 cases (97 %; 17/17 Claude Code, 15/16 CodeRabbit); attackers can practise locally against a copy of the pipeline, defenders get only one try ([arXiv:2603.18740](https://arxiv.org/abs/2603.18740)). SEVRA-Bench showed 8 review agents to be susceptible to social engineering narratives, much more so for weaker models ([arXiv:2606.13757](https://arxiv.org/abs/2606.13757)); GitInject found all four tested AI providers vulnerable to at least one attack class in default workflows ([arXiv:2606.09935](https://arxiv.org/abs/2606.09935)). Anthropic itself warns that `claude-code-security-review` is not hardened against prompt injection ([README](https://github.com/anthropics/claude-code-security-review)). Details, and what Git Warden does about each technique: [R21 in detail](#r21-in-detail-how-ai-gatekeepers-were-persuaded).
- **R22 – Non-Git data.** Issues, PR discussions, releases with assets, wiki settings and repo settings are not in the Git object store. `git clone --mirror` doesn't back them up; that needs API exports such as [python-github-backup](https://github.com/josegonzalez/python-github-backup), and according to its README even those can't be restored faithfully.

### R21 in detail: how AI gatekeepers were persuaded

Documented cases only. These are attacks on **AI** reviewers and gatekeepers. Git Warden uses no AI to decide anything, so most of them have no target in Git Warden itself, but they hit any AI reviewer you run next to it unchanged. Under each technique, "Git Warden" says what its deterministic rules do about it, or **Not covered**.

**T1 – Plausible technical justification, refined offline.** Alexopoulos et al. ([arXiv:2603.18740](https://arxiv.org/abs/2603.18740), v4 of 2026-09-23) revert fixes for 33 real CVEs in 20 projects and submit them as PRs – against Claude Code and CodeRabbit review pipelines rebuilt in isolation. Two strategies:
- **Templates** (after Moon et al.: *authority* "written by an expert", *self-correctness* "correct", *misleading* false purpose) almost never worked: across 264 combinations, a template helped only once, not reproducibly; sometimes it **backfired** because the contradiction between claim and diff stood out.
- **LLM-assisted refinement:** a coding agent writes a factual-sounding PR description (explicitly *without* appeals to authority), adapts comments/commit message to the repo's guidelines and revises them up to ten times based on the review feedback of a local copy of the pipeline. Result: 17/17 against Claude Code, 15/16 against CodeRabbit (32/33 = 97 %). Example strapi: a false "proof" that authorisation applies regardless of query parameters – exactly the property the removed code ensured.
- **Cross-check:** removing the PR description recovered 16 of the 32 cases; additionally removing commit message and comments recovered 12 of the remaining 16; the rest already failed without any text. For CodeRabbit, commit message and code comments had the most effect.
- *Git Warden:* PR and commit texts never change a verdict. **Not covered:** whether the code itself reintroduces a vulnerability; that needs a semantic review Git Warden doesn't do.

**T2 – Unverifiable external context and social engineering.** [SEVRA-Bench](https://arxiv.org/abs/2606.13757) (1,062 adversarial PRs from real fixes of the MITRE top 10 CWEs, 15 framings, 8 review agents in an isolated Gitea): most effective were justifications the reviewer can't check – "Dependency Compatibility Claim", "Prior Approval Signal" (supposedly already approved), "Review Bias"; repo-related explanations produced "unverified deference". Directly checkable contradictions were often caught. Weaker agents fell below a 35 % rejection rate; the strongest (including Claude Opus 4.7, GPT-5.5, GLM-5) mostly rejected.
- *Git Warden:* approvals only count from the warden's own log on the wall host; lockfile and manifest changes are `yellow`. **Not covered:** judging whether a claimed justification is true.

**T3 – Direct instructions in PR, issue or commit text.** [GitInject](https://arxiv.org/abs/2606.09935) (real GitHub workflows, four providers): a fake "internal audit note" in the PR text, instructing the reviewer to approve and not to report comparison operators, worked against `codex-pr-review` and `gemini-assistant`; `claude-pr-review` resisted. Aikido's [PromptPwnd](https://www.aikido.dev/blog/promptpwnd-github-actions-ai-agents) (Dec 2025): issue texts, PR descriptions and commit messages end up in prompts of agents with tools like `gh issue edit`; affected were, among others, Google's Gemini CLI repo (patched within four days) and, according to Aikido, at least five Fortune 500 companies.
- *Git Warden:* no model reads the texts, so there is nothing to instruct. **Not covered:** instructions aimed at other AI tools that read the repo later.

**T4 – Configuration files as operator instructions.** GitInject: a PR adds `CLAUDE.md`, `AGENTS.md` or `GEMINI.md`; the CLI loads it before the review as operator instructions. Example: a CSRF module compares tokens with `==` instead of `hmac.compare_digest` (timing oracle), and an included `CLAUDE.md` says not to report comparison operators in security utilities, "a separate security team checks that" – Claude followed exactly that. According to the paper there is no cheap workflow fix (path filters can be bypassed); author filters or a human are needed. Related: Pillar's ["Rules File Backdoor"](https://www.pillar.security/blog/new-vulnerability-in-github-copilot-and-cursor-how-hackers-can-weaponize-code-agents) (March 2025) hides instructions in Cursor/Copilot rule files.
- *Git Warden:* changes to agent configurations (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.cursor/rules/`, Copilot instructions) are `red` by default. *Limit:* a repo where agents maintain `AGENTS.md` may move these paths to `yellow` (see [design.md](design.md#rules-excerpt)).

**T5 – Invisible or hidden content.** [CamoLeak](https://www.legitsecurity.com/blog/camoleak-critical-github-copilot-vulnerability-leaks-private-source-code) (Legit Security, CVSS 9.6): instructions in invisible Markdown comments of a PR; Copilot Chat followed them with the user's permissions and exfiltrated via an "alphabet" of pre-signed Camo image URLs; GitHub switched off image rendering in Copilot Chat on 2025-08-14 ([The Register](https://www.theregister.com/special-features/2025/10/09/github-patches-copilot-chat-flaw-that-could-leak-secrets/880245)). Pillar: zero-width joiners and bidi markers hide instructions; since 2025-05-01 GitHub shows a warning for hidden Unicode. [Trail of Bits](https://blog.trailofbits.com/2025/08/06/prompt-injection-engineering-for-attackers-exploiting-github-copilot/): payload in an HTML tag, invisible in the GitHub UI. AWS confirmed "invisible prompt injection" in Amazon Q ([AWS-2025-019](https://aws.amazon.com/security/security-bulletins/AWS-2025-019/)).
- *Git Warden:* invisible and bidi characters are `red` (`CONTENT-INVISIBLE`). **Not covered:** instructions hidden in HTML comments or in normal-looking text.

**T6 – Using the reviewer as an exfiltration channel.** Invariant Labs ([GitHub MCP](https://invariantlabs.ai/blog/mcp-github-vulnerability)) and CamoLeak: the tool had more permissions than the task needed. GitInject: token exfiltration via configuration files plus shell tools (minimum protection according to the paper: `persist-credentials: false` + tool restriction).
- *Git Warden:* no reviewer with permissions exists in Git Warden, and nothing is sent to a model provider. **Not covered:** AI reviewers you run elsewhere.

**T7 – Availability / denial of wallet.** GitInject: a PR text at GitHub's limit (65,536 characters) yields ≈ 22,100 input tokens and costs the victim about $1.11 per PR, a two-hour campaign $32–111. As a *hypothesis* (not confirmed), the paper mentions getting the API key suspended by provoking policy violations, which would silently switch off the gate.
- *Git Warden:* no LLM cost. Size and rate limits (`SIZE-*`, `RATE-*`) bound what a push can make the guard do; a flood of `red` pushes can still tire the human.

**T8 – Splitting and hiding outside the checked surface.** So far documented only against **human** reviewers: in xz-utils (CVE-2024-3094), part of the backdoor was "solely in the distributed tarballs", not in the Git repo, the rest obfuscated in test files ([oss-security, A. Freund](https://www.openwall.com/lists/oss-security/2024/03/29/4)). We did **not find** a study that specifically tests splitting across many commits against AI reviewers – plausible, but unproven.
- *Git Warden:* path and content rules run over the cumulative diff, so splitting across commits doesn't help; binary files (also in test directories) are `yellow`. **Not covered:** release artefacts that differ from the tag (reproducible builds would be needed).

**T9 – Code obfuscation.** Base64 payloads, `eval`, renamings. We did **not find** a solid study specifically on the effect on AI reviewers – marked as unproven.
- *Git Warden:* long base64/hex literals are `yellow` (`CONTENT-BLOB`). **Not covered:** `eval`/`exec` and other obfuscation in normal-looking code.

**Common lesson:** all sources see the problem as structural, not as a weakness of one particular model. An AI gatekeeper must not be the last instance for anything that matters. Git Warden therefore has none: its rules are deterministic and a human decides on `red`.

## 5. What can be recovered – and what can't

**Repairable:**

- **Local clones** contain the old history; `git reflog` shows earlier branch states ([git-reflog](https://git-scm.com/docs/git-reflog)).
- **GitHub practically never forgets force pushes:** overwritten commits remain retrievable by SHA ([Brizinov/Truffle Security](https://trufflesecurity.com/blog/guest-post-how-i-scanned-all-of-github-s-oops-commits-for-leaked-secrets)). The [activity view](https://docs.github.com/en/repositories/viewing-activity-and-data-for-your-repository/using-the-activity-view-to-see-changes-to-a-repository) lists force pushes and branch deletions with the actor; that allows restoring the old state.
- **Deleted branches** can be restored via the associated PR ([docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-branches-in-your-repository/deleting-and-restoring-branches-in-a-pull-request)), otherwise via a ref to the known SHA.
- **Deleted repos** can usually be restored for 90 days ([docs](https://docs.github.com/en/repositories/creating-and-managing-repositories/restoring-a-deleted-repository)).

**Not repairable:**

- **Leaked secrets.** A commit is never really gone (see above). The only way out: revoke and rotate the secret ([GitHub: Removing sensitive data](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)).
- **Published packages.** npm allows unpublishing only to a limited extent, and the version number is used up afterwards ([npm unpublish policy](https://docs.npmjs.com/policies/unpublish/), [`npm unpublish`](https://docs.npmjs.com/cli/v11/commands/npm-unpublish)); whoever installed it has already run the malicious code.
- **Exfiltrated or briefly public data.** Copies, forks and archives (e.g. GH Archive) can't be recalled.
- **Knock-on damage** to users of Pages sites or releases.

## 6. Countermeasures

1. **Least-privilege tokens.** [Fine-grained PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) limited to single repos and single permissions (e.g. only `Contents: write`, no `Administration`, no `Workflows`) with an expiry date. Better: a **GitHub App**, whose installation tokens expire after 1 hour ([docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)).
2. **A separate bot identity without admin rights.** Then branch protection and rulesets apply to the agent too; the admin bypass stays with the human.
3. **Rulesets/branch protection** ([available rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)): block force pushes, forbid deletion, require PRs with review, status checks, signed commits, tag protection (`v*`).
4. **CODEOWNERS** for `.github/workflows/`, lockfiles, `LICENSE` ([docs](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)) plus "Require review from Code Owners".
5. **Secrets in environments** with required reviewers ([docs](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)); pin actions to SHAs, avoid `pull_request_target` ([secure use](https://docs.github.com/en/actions/reference/security/secure-use)).
6. **Turn on push protection/secret scanning** ([docs](https://docs.github.com/en/code-security/secret-scanning/introduction/about-push-protection)).
7. **Separate public and private.** An agent that reads other people's issues must not have a token that sees private repos (against toxic flows).
8. **Audit:** check the [security log](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/reviewing-your-security-log) and activity view regularly.
9. **Backups/mirrors** (`git clone --mirror`) in a place agents can't write to. Don't sync blindly (R19) – concept: the [Pull Guard](design.md#pull-guard).
10. **Sandboxing.** Tokens don't belong in the agent's environment but on the wall around it (for example [Hermetarium](https://github.com/pihme/hermetarium)): a proxy/broker that allows and logs Git operations per repo. The Push Guard is such a broker for Git.

## Risk register

| ID | Risk | Section |
| --- | --- | --- |
| R1 | Destroying history (force push, branch/tag deletion, rewrite) | 1 |
| R2 | Deleting or damaging code | 1 |
| R3 | Subtle bugs and backdoors (incl. lockfiles) | 1 |
| R4 | Secrets in commits | 1 |
| R5 | Licence/legal problems | 1 |
| R6 | Spam | 1 |
| R7 | Rewriting CI/CD workflows (secret exfiltration) | 2 |
| R8 | Moving tags | 2 |
| R9 | Malicious releases/packages | 2 |
| R10 | Dependency confusion | 2 |
| R11 | Poisoning GitHub Pages | 2 |
| R12 | Token reach beyond the target repo | 3 |
| R13 | Changing settings, visibility, access, protection rules | 3 |
| R14 | Exfiltration (gists, new public repos, PRs) | 3 |
| R15 | Prompt injection via repo content | 4 |
| R16 | Local agent CLIs abused by malware / token theft | 4 |
| R17 | Slopsquatting | 4 |
| R18 | Mistakes without an attacker | 4 |
| R19 | Naive mirrors mirror the destruction | 4a |
| R20 | Compromised review/bot service with write access | 4a |
| R21 | AI gatekeepers can be persuaded | 4a |
| R22 | Non-Git data (issues, PRs, releases, settings) | 4a |

## Coverage by Git Warden

Git Warden has two guard posts: the **Push Guard** (deterministic rules on every agent push) and the **Pull Guard** (a scheduled git-everref backup that preserves, but decides nothing). Neither uses AI, and nothing checks pull requests, CI results or changes by writers who don't go through the Push Guard.

Legend: **✅** prevents (Push) or preserves (Pull) · **◐** partly · **–** no. Status: **Covered**, **Partly** or **Not covered**. Push Guard values apply **only if agents have no other write credential for the remote**.

| ID | Risk | Push | Pull | Status | Reasoning |
| --- | --- | --- | --- | --- | --- |
| R1 | Destroying history | ✅ | ✅ | Covered | Non-FF and delete are `red` for agents; the Pull Guard preserves against other writers and stolen tokens. |
| R2 | Deleting or damaging code | ◐ | ✅ | Covered | Preserved by the backup; prevented only for mass deletion. |
| R3 | Subtle bugs and backdoors | ◐ | – | Partly | Only what rules see: lockfiles, manifests, build files, binaries, long literals. **Not covered:** semantic review of the code, and changes by other writers or through PRs. |
| R4 | Secrets in commits | ◐ | – | Partly | gitleaks over every new commit before forwarding: recognisable formats never reach the remote; scanner gaps remain. |
| R5 | Licence | ◐ | – | Partly | A `LICENSE` change is `red`. **Not covered:** copied-in third-party code. |
| R6 | Spam | ✅ | – | Covered | Rate limit per agent; the agents' PR-only token allows no issues. PR texts and comments are out of scope. |
| R7 | CI/CD workflows | ✅ | – | Partly | Workflow changes are `red` for agents, plus an App without `workflows` permission. **Not covered:** workflow changes by other writers or in PRs from forks. |
| R8 | Moving tags | ✅ | ✅ | Covered | Tag moves are `red`; the Pull Guard keeps old tags. |
| R9 | Releases and packages | ◐ | – | Partly | The agents' PR-only token can't create releases. **Not covered:** package registries. |
| R10 | Dependency confusion | ◐ | – | Partly | Registry configuration and manifest changes are `yellow`. **Not covered:** resolution at build time. |
| R11 | GitHub Pages | ◐ | – | Partly | Pages pushes are checked (new external scripts are `yellow`). **Not covered:** subtle phishing content. |
| R12 | Token reach | ✅ | – | Covered | By architecture: an agent only pushes to repos assigned to it, the write credential stays on the wall. |
| R13 | Settings, visibility, access | ✅ | – | Covered | By architecture: the agents' token can only open PRs. Settings themselves are out of scope. |
| R14 | Exfiltration | ◐ | – | Partly | No gists or repos without a token. **Not covered:** network exfiltration (the wall's job) and data in PR texts. |
| R15 | Prompt injection of the working agent | ◐ | – | Partly | The cause lies outside; its consequences in the repo go through the rules. |
| R16 | Local CLIs, token theft | ◐ | – | Partly | No write token left on the agents' machine. **Not covered:** malware on that machine. |
| R17 | Slopsquatting | ◐ | – | Partly | Manifest and lockfile changes are `yellow`. **Not covered:** a registry check of new dependencies (not built). |
| R18 | Mistakes without an attacker | ✅ | ✅ | Covered | Prevented or preserved for Git. **Not covered:** databases and cloud resources. |
| R19 | Naive mirrors | – | ✅ | Covered | The Pull Guard's purpose. |
| R20 | Compromised bot with write access | ◐ | ✅ | Partly | The Push Guard holds a write credential and is itself a target (per repo, contents only); the Pull Guard is read-only. |
| R21 | AI gatekeepers can be persuaded | – | – | Not covered | Git Warden has no AI gatekeeper, so there is none to persuade; AI reviewers you run next to it stay exposed. See [R21 in detail](#r21-in-detail-how-ai-gatekeepers-were-persuaded). |
| R22 | Non-Git data | ◐ | – | Partly | The agents' PR-only token can't touch issues or releases. **Not covered:** a backup of issues, PRs and releases. |

**Honestly:** the Push Guard lifts R1, R7, R8, R12 and R13 from "detect" to "prevent" only because agents no longer have their own write credential for the remote. That is half architecture and only half verdict.
