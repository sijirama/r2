# r2 — local Cloudflare R2 file manager

A single Go binary that serves a local web UI for managing your Cloudflare R2
buckets and files. Local-first: your credentials live in a `.env` in this repo
and never leave your machine. Control it from anywhere in your terminal with the
`r2` command.

```
r2            # start the server, prints the local URL
r2 die        # stop it
r2 status     # is it running?
r2 restart    # restart
r2 logs       # tail the server log
```

What you can do in the UI: list buckets, create/delete buckets, browse files by
folder, upload files (multi-select), create text files, delete files, download
locally, and copy a shareable link for any object.

## Setup

```bash
git clone <this-repo> r2 && cd r2
cp .env.example .env      # then fill it in (see below)
make install              # builds the binary + installs the global `r2` command
r2                        # open the printed http://127.0.0.1:8787
```

If `make install` warns that `~/.local/bin` isn't on your `PATH`, add this to
your `~/.zshrc` (or `~/.bashrc`) and restart your shell:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Re-run `make install` any time to rebuild and reinstall after pulling changes.

## Getting your `.env` values

All values come from the Cloudflare dashboard.

### `R2_ACCOUNT_ID`
Dashboard → **R2** → **Overview**. The **Account ID** is in the right-hand
sidebar.

### `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY`
Dashboard → **R2** → **Manage R2 API Tokens** → **Create API Token**.
- Permission: **Object Read & Write** (choose **Admin Read & Write** if you also
  want to create/delete buckets from the UI).
- Click **Create**. Cloudflare shows the **Access Key ID** and **Secret Access
  Key** exactly once — copy both now.

### `CF_API_TOKEN` (optional, but recommended)
This is what lets the app detect which buckets are **public** and hand you a
**permanent** link instead of an expiring one.

Dashboard → **My Profile** → **API Tokens** → **Create Token**.
- Use the **"Read all resources"** template (simplest), or a custom token with
  **Account → Workers R2 Storage → Read**.
- Create it and paste the token into `CF_API_TOKEN`.

Without this token the app still works — but every "copy link" produces a
**presigned URL that expires after 7 days**, because the S3 API alone can't tell
whether a bucket is public.

### `PORT` (optional)
Defaults to `8787`.

## How "copy link" decides what to give you

For each object the app asks Cloudflare (using `CF_API_TOKEN`) whether the bucket
has public access:

- **Custom domain connected** → `https://your-domain/<key>` (permanent)
- **r2.dev public access on** → `https://pub-xxxx.r2.dev/<key>` (permanent)
- **Private** (or no `CF_API_TOKEN`) → a **presigned URL**, valid 7 days

## Dev / Make targets

```
make help       # list targets
make run        # run in the foreground (dev), loads .env from this dir
make build      # compile to ./bin/r2-server
make install    # build + install the global `r2` command
make uninstall  # remove the global `r2` command
make tidy       # go mod tidy
make clean      # remove ./bin and ./.run
```

## Notes & limitations

- **Deleting a bucket requires it to be empty** (R2/S3 rule).
- The UI binds to `127.0.0.1` only — it is not exposed on your network.
- Tailwind is loaded from its Play CDN, so styling needs internet (the app needs
  internet anyway to reach R2). Everything else is embedded in the binary.
- Runtime files (PID + log) live in `./.run/` and are gitignored.
