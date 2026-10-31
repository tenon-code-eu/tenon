# tenon launch film: variants

Remotion project that renders the tenon launch film (`docs/assets/tenon-launch.mp4`, 37 s)
in the formats social networks expect, in English and French. The five scenes follow the
storyboard of `.tools/launch-video/index.html`:

| | Scene | |
|---|---|---|
| A | Shield and stars | the tenon shield in relief, the twelve EU stars flying in |
| B | Approval | a terminal where the write waits for approval (real TUI strings) |
| C | Where your data goes | masking, approval, audit log, then local model, EU providers, outside the EU blocked |
| D | Proof | the audit chain with tenon's real event types and real SHA-256 chaining |
| E | Install | `brew install tenon-code-eu/tap/tenon` |

Compositions (30 fps, 1110 frames):

| Id | Size | Use |
|---|---|---|
| `Launch-Square-EN` / `-FR` | 1080 × 1080 | LinkedIn |
| `Launch-Vertical-EN` / `-FR` | 1080 × 1920 | Reels, Shorts |
| `Launch-Landscape-EN` / `-FR` | 1920 × 1080 | site, YouTube |

Design system of the site (`docs/assets/site/tokens.css`): same colours, same self-hosted fonts
(copied into `public/fonts`, OFL licences alongside). Texts live in `src/i18n.ts`.

```sh
npm ci
npm run dev                 # Remotion Studio
npm run render:square:en    # out/tenon-launch-1080x1080-en.mp4
npm run render:all          # square and vertical EN/FR, landscape FR
```

The films are silent. Remotion is free for individuals and teams of up to three people;
larger companies need a Remotion company licence (https://remotion.pro/license).
