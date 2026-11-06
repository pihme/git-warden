# Configuration

Keys that mean the same thing in both guards. Each guard has its own configuration directory, usually on its own host: a `defaults.yaml` for the whole guard, and one folder per remote under `repos/<name>/` (`warden.yaml` for the Push Guard, `backup.yaml` for the Backup Guard). Unknown keys are errors in both. Every key one guard accepts is on its own page: [Push Guard](push-guard.md), [Backup Guard](backup-guard.md). Which credential to create: [Credentials and the rights they need](credentials.md).

## Shared settings

| Key | File | Meaning |
| --- | --- | --- |
| `remote` | repo file | The remote: `ssh://…` or `user@host:path` (SSH), `https://…` or `http://…` (HTTPS), or a local repository as an absolute path or `file://` URL. |
| `credential` | repo file | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Required for SSH and HTTPS, not allowed for a local remote. Push Guard: write access; Backup Guard: read access only ([rights per platform](credentials.md)). |
| `credential_username` | repo file | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | repo file | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `notify.command` | `defaults.yaml` | Optional. A list of program and arguments. When a warning is due, that program is started and the warning is written to its standard input as one JSON object. Unset means nothing is started; the log line is still written. The JSON is in each guard's Warnings section. |
| `state_dir` | `defaults.yaml` | Where the guard keeps its repositories, its JSON-lines log and its locks. Default `<config>/state`. |
| `timeout` | `defaults.yaml` | Push Guard: per push (default `60s`). Backup Guard: per repo and run (default `30m`). |
| `gitleaks.path` / `everref.path` | `defaults.yaml` | The external program: a bare name is looked up on `PATH` (the default), a path is relative to the configuration directory. |

Relative file paths are relative to the configuration directory. The credential never appears in a URL, on a command line or in a log: SSH runs as `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`; for HTTPS an inline credential helper reads the token file when git asks. The Backup Guard passes the same environment to git-everref, so everref's own fetches use the same key or token.
