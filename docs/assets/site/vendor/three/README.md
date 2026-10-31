# three.js (vendored)

The home page renders its 3D scenes with three.js, served from this directory:
no CDN, no external request.

| | |
|---|---|
| Version | 0.170.0 (r170) |
| File | `three.module.min.js`, copied unchanged from the npm package `three@0.170.0` (`build/three.module.min.js`) |
| SHA-256 | `08fd7545d13d2c7fb65ab691530a802dafefd638596501854f267d0fb13c39e7` |
| Licence | MIT, see `LICENSE` (copied from the same package) |

Check the file against the published package:

```sh
npm pack three@0.170.0 && tar -xzf three-0.170.0.tgz package/build/three.module.min.js
shasum -a 256 package/build/three.module.min.js docs/assets/site/vendor/three/three.module.min.js
```

To upgrade, replace both files from the new package version, update this table,
and check the home page scenes (`docs/assets/site/3d/`) still render.
