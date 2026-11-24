# Omakase

The scripts and systemd units Git Warden's handbook points at, for the wall host. "Omakase" is the set of pieces Git Warden serves next to its two guard posts: shared host configuration, not a third guard.

| File | What it is |
| --- | --- |
| `digest.sh` | Mails a digest of the requests a Hermetarium habitat was denied since the last run. |
| `mail.sh` | The wall's one mail program. The digest and the Push Guard's warning mail both send through it. |
| `hermetarium-digest.service`, `hermetarium-digest.timer` | The systemd user unit and timer that run `digest.sh`. |

The digest used to live in [Hermetarium](https://github.com/pihme/hermetarium) (`omakase/`). It is here now because the mail it sends is the same mail the Push Guard's warnings use, and the Push Guard is Git Warden's. The scripts are unchanged by the move: same mail semantics, same `digest.state`, the same two `dns.jsonl` files.

# Denied requests

A host script and a systemd user timer. Hermetarium does not send mail. Squid only writes the log. The script asks `hermetarium logs` for an up-to-date I/O log, takes the denials since the last run, and sends one mail for that run. A run with nothing new sends nothing. Many denials between two runs are still one mail. The mail names the habitat, the time span, and each denied destination with its count. A connection the client closed during the TLS handshake is listed too, with the host name from the log. The mail has no key, no request body, and no DNS answer.

Gate DNS keeps the current lookup log and one older file, `dns.jsonl` and `dns.jsonl.1`, each capped at 1 MiB. `hermetarium logs` copies only those two. A denied lookup that has already rotated out of `dns.jsonl.1` before this timer runs is not mailed. An HTTP denial stays in Squid's access log and is not dropped that way.

The same mail settings are the wall's one mail configuration. The Push Guard's mail for a red push uses this file and `omakase/mail.sh`. The Push Guard itself does not send mail: you set `notify.command` in its `defaults.yaml` to a program, and the guard hands that program one JSON object on standard input ([Warnings](https://github.com/pihme/git-warden/blob/main/docs/push-guard.md#warnings)). A program that sends the warning as mail, or as anything else you will see, is yours to write; `mail.sh` is what the digest uses there. A password stays in a file that the mail program reads. It is not written in `mail.env`.

The settings file is `~/.config/hermetarium/mail.env` because Hermetarium's `digest.env` points at it: the timer reads that file, and `digest.sh` hands its path to `mail.sh`. A Push Guard warning program that should use the same address reads the same file.

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
| `HERMETARIUM_ROOT` | Data directory. Optional. Same rule as the `hermetarium` command. |
| `HERMETARIUM` | `hermetarium` binary. Optional. Otherwise `PATH`, then `$HERMETARIUM_ROOT/bin/hermetarium`. |
| `HERMETARIUM_MAIL_ENV` | Mail settings file. Optional. Otherwise `~/.config/hermetarium/mail.env`. |
| `MAIL_TO` | Address the mail is sent to. Required. |
| `MAIL_FROM` | Address in the From header. Required. |
| `MAIL_COMMAND` | Absolute path of a program that reads the finished message on its standard input. Optional. Otherwise `/usr/sbin/sendmail -oi -t`. |

`chmod 600` both env files.

Where the script stopped is `var/<id>/digest.state`, next to the I/O log. Deleting that file makes the next run mail the denials still in the log again.

## Timer

The timer runs every 15 minutes. To run it less often, change `OnCalendar` in the timer file before enabling it.

```bash
mkdir -p ~/.config/systemd/user ~/.config/hermetarium
cp omakase/hermetarium-digest.service omakase/hermetarium-digest.timer ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now hermetarium-digest.timer
```