<div align="center">
  <img src="/docs/src/assets/lilbox.svg" height="200"/>
</div>

<h1 align="center" style="margin-top: -10px;"> HomeBox </h1>
<p align="center" style="width: 100%;">
   <a href="https://homebox.software/en/">Docs</a>
   |
   <a href="https://demo.homebox.software">Demo</a>
   |
   <a href="https://discord.gg/aY4DCkpNA9">Discord</a>
</p>
<p align="center" style="width: 100%;">
    <img src="https://img.shields.io/github/check-runs/sysadminsmedia/homebox/main" alt="Github Checks"/>
    <img src="https://img.shields.io/github/license/sysadminsmedia/homebox"/>
    <img src="https://img.shields.io/github/v/release/sysadminsmedia/homebox?sort=semver&display_name=release"/>
    <img src="https://img.shields.io/weblate/progress/homebox?server=https%3A%2F%2Ftranslate.sysadminsmedia.com"/>
</p>
<p align="center" style="width: 100%;">
    <img src="https://img.shields.io/reddit/subreddit-subscribers/homebox"/>
    <img src="https://img.shields.io/mastodon/follow/110749314839831923?domain=infosec.exchange"/>
    <img src="https://img.shields.io/lemmy/homebox%40lemmy.world?label=lemmy"/>
</p>
<p align="center" style="width: 100%;">
  <a href="https://www.pikapods.com/pods?run=homebox"><img src="https://www.pikapods.com/static/run-button.svg"/></a>
</p>

## What is HomeBox

HomeBox is the inventory and organization system built for the Home User! With a focus on simplicity and ease of use, Homebox is the perfect solution for your home inventory, organization, and management needs. While developing this project, We've tried to keep the following principles in mind:

- 🧘 _Simple but Expandable_ - Homebox is designed to be simple and easy to use. No complicated setup or configuration required. But expandable to whatever level of infrastructure you want to put into it.
- 🚀 _Blazingly Fast_ - Homebox is written in Go, which makes it extremely fast and requires minimal resources to deploy. In general, idle memory usage is less than 50MB for the whole container.
- 📦 _Portable_ - Homebox is designed to be portable and run on anywhere. We use SQLite and an embedded Web UI to make it easy to deploy, use, and backup.

### Key Features
- 📇 Rich Organization - Organize your items into categories, locations, and tags. You can also create custom fields to store additional information about your items.
- 🔍 Powerful Search - Quickly find items in your inventory using the powerful search feature.
- 📸 Image Upload - Upload images of your items to make it easy to identify them.
- 📄 Document and Warranty Tracking - Keep track of important documents and warranties for your items.
- 💰 Purchase & Maintenance Tracking - Track purchase dates, prices, and maintenance schedules for your items.
- 📱 Responsive Design - Homebox is designed to work on any device, including desktops, tablets, and smartphones.

## Fork roadmap

This is the `kashif-khan/homebox` fork. Its working branch is `custom`, rebased on upstream `sysadminsmedia/homebox` and released as `*-kk.N` tags. The roadmap below comes from the most requested upstream [Ideas](https://github.com/sysadminsmedia/homebox/discussions/categories/ideas) and open issues. Where a feature is not started it says so; dates are intentionally absent.

**Shipped on `custom`**

| Feature | Upstream request |
| --- | --- |
| Expiry dates for consumables, with alert rules (offsets before/after, per-collection time zone) | [#714](https://github.com/sysadminsmedia/homebox/discussions/714), [#1135](https://github.com/sysadminsmedia/homebox/discussions/1135) |
| Scoped API keys, with an OAuth 2.1 server | [#539](https://github.com/sysadminsmedia/homebox/discussions/539), [#979](https://github.com/sysadminsmedia/homebox/discussions/979) |
| MCP server for AI assistants (opt-in, permission-capped per collection) | [#1603](https://github.com/sysadminsmedia/homebox/discussions/1603) |
| Signed outbound webhooks with retries | none |
| Typed custom fields | none |
| Scheduled, versioned, remote backups | [#1767](https://github.com/sysadminsmedia/homebox/discussions/1767) |

**Planned, not started.** Order is a proposal and may change.

| Wave | Work | Upstream requests |
| --- | --- | --- |
| 0 | Reliability: CSV export/import round-trip, import timeouts, wipe and backup-download bugs, mobile camera | [#1776](https://github.com/sysadminsmedia/homebox/issues/1776), [#793](https://github.com/sysadminsmedia/homebox/issues/793), [#1781](https://github.com/sysadminsmedia/homebox/issues/1781), [#1752](https://github.com/sysadminsmedia/homebox/issues/1752), [#531](https://github.com/sysadminsmedia/homebox/issues/531), [#1290](https://github.com/sysadminsmedia/homebox/issues/1290) |
| 1 | Recurring maintenance plans, with a calendar feed | [#172](https://github.com/sysadminsmedia/homebox/issues/172), [#10](https://github.com/sysadminsmedia/homebox/discussions/10), [#722](https://github.com/sysadminsmedia/homebox/discussions/722) |
| 1 | Audit log and item history | [#1139](https://github.com/sysadminsmedia/homebox/discussions/1139) |
| 2 | Lending and checkout ledger | [#713](https://github.com/sysadminsmedia/homebox/discussions/713), [#761](https://github.com/sysadminsmedia/homebox/discussions/761), [#74](https://github.com/sysadminsmedia/homebox/issues/74) |
| 2 | Label and QR overhaul, public "found me" page | [#53](https://github.com/sysadminsmedia/homebox/discussions/53), [#1202](https://github.com/sysadminsmedia/homebox/discussions/1202), [#13](https://github.com/sysadminsmedia/homebox/issues/13) |
| 3 | Barcode lookup and batch scan capture | [#477](https://github.com/sysadminsmedia/homebox/discussions/477), [#724](https://github.com/sysadminsmedia/homebox/discussions/724), [#997](https://github.com/sysadminsmedia/homebox/discussions/997) |
| 3 | Document hub: Paperless-ngx, Immich, Nextcloud | [#544](https://github.com/sysadminsmedia/homebox/discussions/544), [#266](https://github.com/sysadminsmedia/homebox/discussions/266) |
| 4 | User management and OIDC fixes | [#439](https://github.com/sysadminsmedia/homebox/issues/439), [#1259](https://github.com/sysadminsmedia/homebox/issues/1259) |
| 4 | Sub-location rollups, floor plans, finance and insurance pack, table view | [#1201](https://github.com/sysadminsmedia/homebox/discussions/1201), [#767](https://github.com/sysadminsmedia/homebox/discussions/767), [#743](https://github.com/sysadminsmedia/homebox/discussions/743) |

Everything outward-facing (lookup providers, AI features, public pages, integrations) will be off by default. Useful pieces will be offered upstream as small PRs.

## Screenshots
![Login Screen](.github/screenshots/1.png)
![Dashboard](.github/screenshots/2.png)
![Item View](.github/screenshots/3.png)
![Create Item](.github/screenshots/9.png)
![Search](.github/screenshots/8.png)

You can also try the demo instances of Homebox:
- [Demo](https://demo.homebox.software)
- [Nightly](https://nightly.homebox.software)

## Quick Start

[Configuration & Docker Compose](https://homebox.software/en/quick-start/)

```bash
# Save pepper to local file
openssl rand -base64 48 > hbox.pepper
chmod 400 hbox.pepper
# If using the rootless or hardened image, ensure data 
# folder has correct permissions
mkdir -p /path/to/data/folder
chown 65532:65532 -R /path/to/data/folder
docker run -d \
  --name homebox \
  --restart unless-stopped \
  --publish 3100:7745 \
  --env TZ=Europe/Bucharest \
  --env HBOX_AUTH_API_KEY_PEPPER=$(cat hbox.pepper) \
  --volume /path/to/data/folder/:/data \
  ghcr.io/sysadminsmedia/homebox:latest
# ghcr.io/sysadminsmedia/homebox:latest-rootless
# ghcr.io/sysadminsmedia/homebox:latest-hardened
```

<!-- CONTRIBUTING -->

## Contributing

Contributions are what make the open source community such an amazing place to learn, inspire, and create. Any contributions you make are **greatly appreciated**.

To get started with code based contributions, please see our [contributing guide](https://homebox.software/en/contribute/).

If you are not a coder and can't help translate, you can still contribute financially. Financial contributions help us maintain the project and keep demos running.

## Help us Translate
We want to make sure that Homebox is available in as many languages as possible. If you are interested in helping us translate Homebox, please help us via our [Weblate instance](https://translate.sysadminsmedia.com/projects/homebox/).

[![Translation status](https://translate.sysadminsmedia.com/widget/homebox/multi-auto.svg)](https://translate.sysadminsmedia.com/engage/homebox/)

## Credits
- Original project by [@hay-kot](https://github.com/hay-kot)
- Logo by [@lakotelman](https://github.com/lakotelman)

### Contributors
<a href="https://github.com/sysadminsmedia/homebox/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=sysadminsmedia/homebox" />
</a>
