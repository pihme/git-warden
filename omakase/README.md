# Omakase

Host scripts that run on the wall next to the guard posts. They are not a guard post, they are not built into `push-guard` or `backup-guard`, and they are not part of a release. Use them from a checkout of this repository.

- [Denied requests](#denied-requests): one mail for the requests a [Hermetarium](https://github.com/pihme/hermetarium) habitat was denied, and `mail.sh`, the wall's one mail configuration.

These scripts were in Hermetarium's `omakase/` until [hermetarium#209](https://github.com/pihme/hermetarium/issues/209). [Moving from Hermetarium](#moving-from-hermetarium) says what to change on a host that already runs them.

# Denied requests

A host script and a systemd user timer. Hermetarium does not send mail. Squid only writes the log. The script asks `hermetarium logs` for an up-to-date I/O log, takes the denials since the last run, and sends one mail for that run. A run with nothing new sends nothing. Many denials between two runs are still one mail. The mail names the habitat, the time span, and each denied destination with its count. A connection the client closed during the TLS handshake is listed too, with the host name from the log. The mail has no key, no request body, and no DNS answer.

Gate DNS keeps the current lookup log and one older file, `dns.jsonl` and `dns.jsonl.1`, each capped at 1 MiB. `hermetarium logs` copies only those two. A denied lookup that has already rotated out of `dns.jsonl.1` before this timer runs is not mailed. An HTTP denial stays in Squid's access log and is not dropped that way.

The same mail settings are the wall's one mail configuration. The Push Guard's mail for a red push uses this file and `omakase/mail.sh`: set `notify.command` to the absolute path of `omakase/mail.sh` ([A warning when a push needs you](../docs/push-guard.md#a-warning-when-a-push-needs-you)). `mail.sh` sends what it reads on its standard input as the message body. A password stays in a file that the mail program reads. It is not written in `mail.env`.

The script needs a Hermetarium habitat and the `hermetarium` command. This repository does not install Hermetarium.

## Settings

`~/.config/hermetarium/digest.env` is the file the timer reads.

```
HERMETARIUM_DIGEST_SCRIPT=/path/to/git-warden/omakase/digest.sh
PATH=/usr/local/bin:/usr/bin:/bin
HERMETARIUM_ID=opencode
```

`~/.config/hermetarium/mail.env` is the file `omakase/mail.sh` reads. It is assignments, not a shell script.

```
MAIL_TO=you@example.com
MAIL_FROM=hermetarium@wall
```

| Setting | Meaning |
| --- | --- |
| `HERMETARIUM_DIGEST_SCRIPT` | Absolute path of `digest.sh`. Required. The timer runs this and nothing else. |
| `PATH` | Command search path for the timer. Required. The user manager does not use a login PATH. It must contain `hermetarium` unless `HERMETARIUM` is set, and `awk`. |
| `HERMETARIUM_ID` | Habitat id. Required. |
| `HERMETARIUM_ROOT` | Hermetarium's data directory. Optional. Same rule as the `hermetarium` command. |
| `HERMETARIUM` | `hermetarium` binary. Optional. Otherwise `PATH`, then `$HERMETARIUM_ROOT/bin/hermetarium`. |
| `HERMETARIUM_MAIL_ENV` | Mail settings file. Optional. Otherwise `~/.config/hermetarium/mail.env`. |
| `MAIL_TO` | Address the mail is sent to. Required. |
| `MAIL_FROM` | Address in the From header. Required. |
| `MAIL_COMMAND` | Absolute path of a program that reads the finished message on its standard input. Optional. Otherwise `/usr/sbin/sendmail -oi -t`. |

`chmod 600` both env files.

Where the script stopped is `var/<id>/digest.state` in Hermetarium's data directory, next to the I/O log. Deleting that file makes the next run mail the denials still in the log again.

## Timer

The timer runs every 15 minutes. To run it less often, change `OnCalendar` in the timer file before enabling it. Run these in the checkout of this repository.

```bash
mkdir -p ~/.config/systemd/user ~/.config/hermetarium
cp omakase/hermetarium-digest.service omakase/hermetarium-digest.timer ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now hermetarium-digest.timer
```

`loginctl enable-linger` keeps the timer running when you are logged out.

## Moving from Hermetarium

The scripts, the unit files, the settings files and `digest.state` are the same as in Hermetarium's `omakase/`. On a host that already runs the timer:

1. In `~/.config/hermetarium/digest.env`, set `HERMETARIUM_DIGEST_SCRIPT` to `omakase/digest.sh` in this checkout.
2. If `notify.command` of the Push Guard names Hermetarium's `omakase/mail.sh`, set it to `omakase/mail.sh` in this checkout.

The service reads `digest.env` on every run, so the next run uses the new path. The installed unit files do not change. `mail.env` and `digest.state` stay where they are, so the next run continues after the last mailed denial.
