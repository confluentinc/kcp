# Topic-Based Migration Model

The source of the interactive model on the docs page
[Topic-Based Migration Model](../assets/technical-guides/migrations/gateway/visualizer.md).
It is an animated model of a topic-based migration through Confluent Gateway.
It doesn't run kcp or use real data.

It is TypeScript, built with Vite into plain static files under
`docs/assets/visualizer/`, which the docs page loads in an iframe.

## Commit the build output

The docs site publishes `docs/assets/visualizer/` exactly as it is in the
repository: nothing builds the model when the site is deployed. After any change
under `docs/visualizer/`, run the build and commit what it writes to
`docs/assets/visualizer/` in the same change. Otherwise the site keeps showing
the old model.

## Work on it

Needs Node.js 24 or later.

```bash
cd docs/visualizer
npm ci          # install the pinned dependencies
npm run dev     # serve the model on its own, with live reload
npm test        # run the model's tests
npm run build   # type-check, then build into docs/assets/visualizer/
```

To see it on the docs page, run `make docs-serve` from the repository root.

## Layout

| Path | What it is |
| --- | --- |
| `src/sim/model.ts` | The model: topics, clients, the Gateway, the cluster link and kcp's steps. Deterministic, advanced in fixed model steps. |
| `src/sim/timeline.ts` | Records every input with its time, so the timeline can rewind to any moment and replay. |
| `src/view/scene.ts` | Draws the model as SVG and places the moving messages each frame. |
| `src/main.ts` | The page: the clock, the controls, the timeline markers and the Migration steps panel. |
| `src/sim/*.test.ts` | Tests for the model and the timeline. |

Anime.js drives the clock. The build adds its MIT licence notice to the top of
the built script.
