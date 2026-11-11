# KaTeX (vendored)

The blog renders its formulas with KaTeX, served from this directory:
no CDN, no external request.

| | |
|---|---|
| Version | 0.16.11 |
| Files | `katex.min.js`, `katex.min.css` (from `dist/`), `auto-render.min.js` (from `dist/contrib/`), `fonts/*.woff2` (from `dist/fonts/`), all copied unchanged from the npm package `katex@0.16.11` |
| SHA-256 `katex.min.js` | `e6bfe5deebd4c7ccd272055bab63bd3ab2c73b907b6e6a22d352740a81381fd4` |
| SHA-256 `katex.min.css` | `717bc9ae7853b61f0f76455dddf0ecd4f527a783f42de2ac24684899c1c46258` |
| SHA-256 `auto-render.min.js` | `7b57d427ac6270677daf8d8380ded2cc73336f9149a167b8e1fe0d6ef66604ae` |
| Licence | MIT, see `LICENSE` (copied from the same package) |

Only the `.woff2` fonts are kept. The stylesheet also lists `.woff` and `.ttf`
fallbacks; every browser the site supports picks `.woff2` first, so the
fallbacks are never requested.

Check the files against the published package:

```sh
npm pack katex@0.16.11 && tar -xzf katex-0.16.11.tgz
shasum -a 256 package/dist/katex.min.js package/dist/katex.min.css package/dist/contrib/auto-render.min.js
```

To upgrade, replace the files from the new package version, update this table,
and check the formulas on the blog posts still render.
