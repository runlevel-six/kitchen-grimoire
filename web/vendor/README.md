# Vendored frontend assets

Pinned third-party files, embedded into the binary from `web/static/vendor/`
(docs/DESIGN.md §5.2). Update by downloading the package tarball from the npm
registry, verifying its `integrity` against the registry metadata, and
recording the new checksums here.

| File | Package | Version | License | Source | SHA-256 of the file |
|---|---|---|---|---|---|
| `web/static/vendor/htmx-2.0.11.min.js` | `htmx.org` | 2.0.11 | 0BSD | `https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.11.tgz`, file `package/dist/htmx.min.js` | `d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717` |

Tarball integrity (npm `dist.integrity`) for htmx 2.0.11:
`sha512-Thx/WtpeOQqSrqBCw/A1cwGJGg4UrVa3+sW0GmrM3p4gJgO89ecH4qtbnyzDDWFvBTqjnIMCgELTNt636dtamA==`

Check the embedded copy with:

```sh
shasum -a 256 web/static/vendor/htmx-2.0.11.min.js
```
