# Credentials and the rights they need

The Push Guard writes to the remote, the Backup Guard only reads. Give each exactly that, and never reuse the Push Guard's credential for the Backup Guard. Save the key or token as a file. Its path is the `credential` setting in that remote's `warden.yaml` or `backup.yaml` ([the other settings](configuration.md)).

| Credential | Push Guard (`warden.yaml`): read and write | Backup Guard (`backup.yaml`): read only |
| --- | --- | --- |
| GitHub deploy key (SSH) | Deploy key with **Allow write access** | Deploy key without it (deploy keys are read-only by default) |
| GitHub fine-grained personal access token (HTTPS) | Repository permission **Contents: Read and write**; **Metadata: Read-only** is always included | **Contents: Read-only**; **Metadata: Read-only** |
| GitHub App installation token (HTTPS, user name `x-access-token`) | Repository permission **Contents: Read and write** | **Contents: Read-only** |
| GitLab deploy key (SSH) | Deploy key with **Grant write permissions to this key** | Deploy key without it (read-only) |
| GitLab project access token (HTTPS, any non-blank user name) | Scope **`write_repository`**, role **Developer** (pushes to unprotected branches) or **Maintainer** (protected branches) | Scope **`read_repository`**, with a role that may read the code (e.g. **Reporter**) |
| GitLab deploy token (HTTPS) | Not possible: deploy tokens have no scope for pushing to a repository | Scope **`read_repository`**; set `credential_username` to the token's user name (default format `gitlab+deploy-token-<n>`) |

Notes:

- With a GitHub personal access token, GitHub's documentation uses the account's user name as the Git user name; set `credential_username` to it.
- Changes to `.github/workflows` additionally need the **Workflows** permission on GitHub. Leaving it out of the Push Guard's token means GitHub itself refuses workflow changes, even if a rule missed one.
- The remote's own protections (protected branches, rulesets) still apply to the guard's credential.

Sources: GitHub [deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys), [personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens), [GitHub App permissions for Git access](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app); GitLab [deploy keys](https://docs.gitlab.com/user/project/deploy_keys/), [project access tokens](https://docs.gitlab.com/user/project/settings/project_access_tokens/), [token scopes](https://docs.gitlab.com/security/tokens/access_token_scopes/), [deploy tokens](https://docs.gitlab.com/user/project/deploy_tokens/), [roles and permissions](https://docs.gitlab.com/user/permissions/).
