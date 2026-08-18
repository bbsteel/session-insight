# Session Insight landing page

This directory is a dependency-free static landing page for Session Insight.
It is designed to deploy directly to Cloudflare Pages.

## Local preview

From the repository root:

```bash
python3 -m http.server 4173 --directory site
```

Then open <http://127.0.0.1:4173/>.

## Cloudflare Pages

Use the repository as the Pages source with:

- **Root directory:** `/`
- **Build command:** none
- **Build output directory:** `site`

After the first deployment, attach the chosen custom domain in Cloudflare Pages.
The page currently links to the public GitHub repository and the `v0.7.1` release.

Product visuals live under `assets/screenshots/en` and `assets/screenshots/zh-CN`. They are sanitized captures of the real interface in dark theme, with English and Chinese session data kept in their matching locale.
